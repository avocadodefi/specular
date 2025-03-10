// Copyright 2022, Specular contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bindings

//go:generate go run github.com/ethereum/go-ethereum/cmd/abigen --abi ../../../contracts/abi/src/bridge/L1Oracle.sol/L1Oracle.json --type L1Oracle --pkg bindings --out L1Oracle.go
//go:generate go run github.com/ethereum/go-ethereum/cmd/abigen --abi ../../../contracts/abi/src/challenge/IChallenge.sol/ISymChallenge.json --type ISymChallenge --pkg bindings --out ISymChallenge.go
//go:generate go run github.com/ethereum/go-ethereum/cmd/abigen --abi ../../../contracts/abi/src/IRollup.sol/IRollup.json --pkg bindings --type IRollup --out IRollup.go
//go:generate go run github.com/ethereum/go-ethereum/cmd/abigen --abi ../../../contracts/abi/src/ISequencerInbox.sol/ISequencerInbox.json --pkg bindings --type ISequencerInbox --out ISequencerInbox.go
