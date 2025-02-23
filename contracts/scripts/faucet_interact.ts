import dotenv from "dotenv";
import * as ethers from "ethers";
import TinyFaucet from "../artifacts/src/pre-deploy/Faucet.sol/Faucet.json";

dotenv.config({ path: __dirname + "/../.env" });

const main = async () => {
  const contractAddress = "0x0000000000000000000000000000000000000020";
  const provider = new ethers.providers.JsonRpcProvider(
    "http://localhost:4011"
  );

  const signer = new ethers.Wallet(
    `0x${process.env.SEQUENCER_PRIVATE_KEY}`,
    provider
  );
  const contract = new ethers.Contract(
    contractAddress,
    TinyFaucet.abi,
    provider
  );
  const gasPrice = await provider.getGasPrice();

  // initial balance of sequencer & faucet
  let signerbalance = await provider.getBalance(signer.address);
  console.log("sequencer Balance: ", ethers.utils.formatEther(signerbalance));

  let contractBalance = await provider.getBalance(contractAddress);
  console.log("contract Balance: ", ethers.utils.formatEther(contractBalance));

  const owner = await contract.owner();
  console.log(owner);

  const allowedAmount = await contract.amountAllowed();
  console.log("amount allowed: ", allowedAmount);

  // Transfers amountAllowed from faucet to signer address
  const requestFundTx = await contract
    .connect(signer)
    .requestFunds(signer.address, {
      gasLimit: "300000",
    });
  await requestFundTx.wait();

  // checks balance after requestFunds request
  signerbalance = await provider.getBalance(signer.address);
  console.log("sequencer Balance: ", ethers.utils.formatEther(signerbalance));

  contractBalance = await provider.getBalance(contractAddress);
  console.log("contract Balance: ", ethers.utils.formatEther(contractBalance));

  // Transfers faucet balance to sequencer
  const retrieve = await contract.connect(signer).retrieve({
    gasLimit: "260000",
    gasPrice,
  });
  await retrieve.wait();

  // checking balance after retrieving complete balance from faucet
  signerbalance = await provider.getBalance(signer.address);
  console.log("sequencer Balance: ", ethers.utils.formatEther(signerbalance));

  contractBalance = await provider.getBalance(contractAddress);
  console.log("contract Balance: ", ethers.utils.formatEther(contractBalance));
};

main()
  .then(() => process.exit(0))
  .catch((error) => {
    console.error(error);
    process.exit(1);
  });
