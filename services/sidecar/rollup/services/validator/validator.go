package validator

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/specularL2/specular/services/sidecar/rollup/rpc/bridge"
	"github.com/specularL2/specular/services/sidecar/rollup/rpc/eth"
	"github.com/specularL2/specular/services/sidecar/rollup/services/api"
	specularTypes "github.com/specularL2/specular/services/sidecar/rollup/types"
	"github.com/specularL2/specular/services/sidecar/utils/fmt"
	"github.com/specularL2/specular/services/sidecar/utils/log"
)

var transactTimeout = 10 * time.Minute

type unexpectedSystemStateError struct{ msg string }

func (e unexpectedSystemStateError) Error() string {
	return fmt.Sprintf("service entered unexpected state: %s", e.msg)
}

type Validator struct {
	cfg            Config
	l1TxMgr        TxManager
	l1BridgeClient BridgeClient
	l1State        EthState
	l2Client       L2Client

	lastCreatedAssertionAttrs assertionAttributes
}

type assertionAttributes struct {
	l2BlockNum        uint64
	l2StateCommitment specularTypes.Bytes32
}

func NewValidator(
	cfg Config,
	l1TxMgr TxManager,
	l1BridgeClient BridgeClient,
	l1State EthState,
	l2Client L2Client,
) *Validator {
	return &Validator{cfg: cfg, l1TxMgr: l1TxMgr, l1BridgeClient: l1BridgeClient, l1State: l1State, l2Client: l2Client}
}

func (v *Validator) Start(ctx context.Context, eg api.ErrGroup) error {
	log.Info("Starting validator...")
	if err := v.l2Client.EnsureDialed(ctx); err != nil {
		return fmt.Errorf("failed to create L2 client: %w", err)
	}
	eg.Go(func() error { return v.start(ctx) })
	log.Info("Validator started")
	return nil
}

// Advances validator step-by-step.
func (v *Validator) start(ctx context.Context) error {
	// TODO: Maybe we should change this to be event-based and listen for head advances on the chain
	var ticker = time.NewTicker(v.cfg.GetValidationInterval())
	defer ticker.Stop()
	// TODO: do this in the L2 consensus client, not here.
	if err := v.validateGenesis(ctx); err != nil {
		return fmt.Errorf("failed to validate genesis: %w", err)
	}
	if err := v.ensureStaked(ctx); err != nil {
		return fmt.Errorf("failed to ensure validator is staked: %w", err)
	}
	if err := v.rollback(ctx); err != nil {
		return fmt.Errorf("failed to initialize state: %w", err)
	}
	for {
		select {
		case <-ticker.C:
			if err := v.step(ctx); err != nil {
				log.Errorf("Failed to advance: %w", err)
				if errors.As(err, &unexpectedSystemStateError{}) {
					return fmt.Errorf("aborting: %w", err)
				}
			}
		case <-ctx.Done():
			log.Info("Aborting.")
			return nil
		}
	}
}

// Attempts to create a new assertion and confirm an existing assertion.
func (v *Validator) step(ctx context.Context) error {
	// Flush validators staked on confirmed prior assertion
	if err := v.flushValidator(ctx); err != nil {
		return fmt.Errorf("failed to flush validators: %w", err)
	}
	// Try to create a new assertion.
	// TODO: do this only if configured to be an active validator.
	if err := v.createAssertion(ctx); err != nil {
		return fmt.Errorf("failed to create assertion: %w", err)
	}
	if err := v.resolveFirstUnresolvedAssertion(ctx); err != nil {
		return fmt.Errorf("failed to resolve assertion: %w", err)
	}
	return nil
}

func (v *Validator) flushValidator(ctx context.Context) error {
	_, err := v.l1BridgeClient.GetLastConfirmedAssertionID(ctx)
	if err != nil {
		return err
	}
	// FIXME: iterate on the stakers and flush, do not flush own stake
	//stakedOnLast, err := v.l1BridgeClient.IsStakedOnAssertion(ctx, id, v.cfg.GetAccountAddr())
	//if err != nil {
	//	return err
	//}
	//if !stakedOnLast {
	//	_, err = v.l1TxMgr.RemoveStake(ctx, v.cfg.GetAccountAddr())
	//	if err != nil {
	//		return err
	//	}
	//}
	return nil
}

// If enough time has passed and txs have been sequenced to L1, create a new assertion.
// Add it to the queue for confirmation.
func (v *Validator) createAssertion(ctx context.Context) error {
	assertionAttrs, err := v.getNextAssertionAttrs(ctx)
	if err != nil {
		return fmt.Errorf("failed to get next assertion attrs: %w", err)
	}
	// TODO fix assumptions: not reorg-resistant. Other validators may have inserted new assertions.
	if assertionAttrs.l2BlockNum <= v.lastCreatedAssertionAttrs.l2BlockNum {
		log.Info("No new blocks to create assertion for yet.")
		return nil
	}
	cCtx, cancel := context.WithTimeout(ctx, transactTimeout)
	defer cancel()
	// TOOD: GasLimit: 0 ...?
	receipt, err := v.l1TxMgr.CreateAssertion(cCtx, assertionAttrs.l2StateCommitment, big.NewInt(0).SetUint64(assertionAttrs.l2BlockNum))
	if err != nil {
		return err
	}
	if receipt.Status == types.ReceiptStatusFailed {
		log.Error("Tx successfully published but reverted", "tx_hash", receipt.TxHash)
	} else {
		log.Info("Tx successfully published", "tx_hash", receipt.TxHash)
		log.Info("Created assertion", "l2Block#", assertionAttrs.l2BlockNum)
		v.lastCreatedAssertionAttrs = assertionAttrs
	}
	return nil
}

// If the first unresolved assertion is eligible for confirmation, trigger its confirmation. Otherwise, wait.
func (v *Validator) resolveFirstUnresolvedAssertion(ctx context.Context) error {
	err := v.l1BridgeClient.RequireFirstUnresolvedAssertionIsConfirmable(ctx)
	if err != nil {
		// It is not confirmable.
		// Let's explore the reasons.
		errStr := err.Error()
		if errStr == bridge.NoUnresolvedAssertionErr {
			log.Trace("No unresolved assertion to resolve.")
		} else if errStr == bridge.ConfirmationPeriodPendingErr {
			log.Trace("Too early to confirm first unresolved assertion.")
		} else {
			return &unexpectedSystemStateError{"failed to validate assertion (breaks current assumptions): " + err.Error()}
		}

		// If not confirmable could still be rejectable
		err := v.l1BridgeClient.RequireFirstUnresolvedAssertionIsRejectable(ctx, v.cfg.GetAccountAddr())
		if err != nil {
			// It is not rejectable
			log.Trace("No unresolved assertion to be rejected.")
		} else {
			// It is not confirmable, but it is rejectable so let's try to reject
			_, err = v.l1TxMgr.RejectFirstUnresolvedAssertion(ctx, v.cfg.GetAccountAddr())
			if err != nil {
				// It should be rejectable, but it failed to reject
				log.Warn("rejectable assertion failed to reject")
				return err
			}
		}

		return nil
	}

	// At this point we know it's confirmable
	cCtx, cancel := context.WithTimeout(ctx, transactTimeout)
	defer cancel()

	// TODO: check we are in sync by e.g. block number

	_, err = v.l1TxMgr.ConfirmFirstUnresolvedAssertion(cCtx)
	if err != nil {
		return fmt.Errorf("failed to confirm assertion: %w", err)
	}
	log.Info("Confirmed assertion")
	return nil
}

// Rolls back local validator state, using the current L1 contract state as a checkpoint.
func (v *Validator) rollback(ctx context.Context) error {
	staker, err := v.l1BridgeClient.GetStaker(ctx, v.cfg.GetAccountAddr())
	if err != nil {
		return fmt.Errorf("failed to get staker: %w", err)
	}
	assertion, err := v.l1BridgeClient.GetAssertion(ctx, staker.AssertionID)
	if err != nil {
		return fmt.Errorf("failed to get assertion: %w", err)
	}
	v.lastCreatedAssertionAttrs = assertionAttributes{assertion.BlockNum.Uint64(), assertion.StateCommitment}
	return nil
}

// Gets the next assertion's attributes.
// We can relax this to get the next safe assertion's attributes but need to handle reorgs.
func (v *Validator) getNextAssertionAttrs(ctx context.Context) (assertionAttributes, error) {
	header, err := v.l2Client.HeaderByTag(ctx, eth.Safe)
	if err != nil {
		return assertionAttributes{}, fmt.Errorf("failed to get finalized assertion attrs: %w", err)
	}

	log.Info("creating new assertion", "stateRoot", header.Hash(), "stateCommitment", StateCommitment(&StateCommitmentV0{header.Hash(), header.Root}))
	return assertionAttributes{header.Number.Uint64(), StateCommitment(&StateCommitmentV0{header.Hash(), header.Root})}, nil
}

func (v *Validator) ensureStaked(ctx context.Context) error {
	staker, err := v.l1BridgeClient.GetStaker(ctx, v.cfg.GetAccountAddr())
	if err != nil {
		return fmt.Errorf("failed to get staker: %w", err)
	}
	if staker.IsStaked {
		log.Info("Already staked.")
		return nil
	}
	amount, err := v.l1BridgeClient.GetRequiredStakeAmount(ctx)
	if err != nil {
		return fmt.Errorf("failed to get stake amount: %w", err)
	}
	_, err = v.l1TxMgr.Stake(ctx, amount)
	if err != nil {
		return fmt.Errorf("failed to stake: %w", err)
	}
	log.Info("Staked successfully.", "amount", amount)
	return nil
}

// TODO: refactor.
func (v *Validator) validateGenesis(ctx context.Context) error {
	assertion, err := v.l1BridgeClient.GetAssertion(ctx, common.Big0)
	if err != nil {
		return fmt.Errorf("failed to get genesis assertion: %w", err)
	}
	stateCommitment := assertion.StateCommitment
	// Check that the genesis assertion is correct.
	genesisBlock, err := v.l2Client.BlockByNumber(ctx, common.Big0)
	if err != nil {
		return fmt.Errorf("failed to get L2 genesis block: %w", err)
	}
	genesisStateCommitment := StateCommitment(&StateCommitmentV0{genesisBlock.Header().Hash(), genesisBlock.Header().Root})
	if stateCommitment != genesisStateCommitment {
		return fmt.Errorf("mismatching genesis on L1=%s vs L2=%s", &stateCommitment, &genesisStateCommitment)
	}
	return nil
}
