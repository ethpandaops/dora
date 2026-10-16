// Package executionproof interprets the EIP-8025 execution proofs the indexer
// collects: the registry of proof types, the public input a proof commits to,
// the extraction of that commitment from the zkVM-specific proof encodings,
// and the prover signature of a proof envelope. It does not verify the zero
// knowledge proofs themselves; that is left to the zkVM verifiers.
package executionproof

import (
	"encoding/hex"
	"fmt"
)

// ZkVM identifies the proof system of a proof type.
type ZkVM string

const (
	ZkVMOpenVM ZkVM = "openvm"
	ZkVMSP1    ZkVM = "sp1"
	ZkVMZisk   ZkVM = "zisk"
)

// Type describes one EIP-8025 proof type: an immutable combination of proof
// system, guest program and version. The assignments mirror the devnet
// registry of the clients (Lighthouse `optional-proofs`), including the program
// verification keys the proofs are verified against.
type Type struct {
	ID    uint8
	Guest string
	ZkVM  ZkVM
	// ProgramVK is the verification key of the guest program in the encoding
	// the zkVM's verifier takes: SP1 and Zisk 32 bytes, OpenVM the bitcode
	// encoded verification baseline.
	ProgramVK []byte
}

// Name returns the display name of the proof type.
func (t *Type) Name() string {
	zkvm := map[ZkVM]string{ZkVMOpenVM: "OpenVM", ZkVMSP1: "SP1", ZkVMZisk: "Zisk"}[t.ZkVM]
	return fmt.Sprintf("%s + %s", zkvm, t.Guest)
}

var types = map[uint8]*Type{
	1: {ID: 1, Guest: "ethrex", ZkVM: ZkVMOpenVM, ProgramVK: mustHex(ethrexOpenVMProgramVK)},
	2: {ID: 2, Guest: "ethrex", ZkVM: ZkVMSP1, ProgramVK: mustHex("00662ca6c9db4ecb22d9ac4c320612caeac9c320f92e3ab019af4f544e35c88b")},
	3: {ID: 3, Guest: "ethrex", ZkVM: ZkVMZisk, ProgramVK: mustHex("be8b29b013077f82a411403e9389ae7464b152db764e4333fdbd2c81fa157b0d")},
	4: {ID: 4, Guest: "reth", ZkVM: ZkVMOpenVM, ProgramVK: mustHex(rethOpenVMProgramVK)},
	5: {ID: 5, Guest: "reth", ZkVM: ZkVMSP1, ProgramVK: mustHex("00a03cbfa95559cfee3b45ef925f3f7a631181e35e774e92b040277d893511dd")},
	6: {ID: 6, Guest: "reth", ZkVM: ZkVMZisk, ProgramVK: mustHex("7b0f7b082966c8155b496c2e1a371b3824b461ad2e1c2931f222c63a10004a14")},
	7: {ID: 7, Guest: "zesu", ZkVM: ZkVMZisk, ProgramVK: mustHex("0e85a61f8d928667d8a771506348809f054360b27b97595eeaba611fa5760ba7")},
}

const (
	ethrexOpenVMProgramVK = "005793a02300aef9526800ee5a881400a80e7d2600ba7f8b4500f002973500b2c72d61005e74577006030619068000a2" +
		"c21b53000a2fee4f0036169c2800aaaa8d6c0087fbce5b00328dc26f009fe7de5a004686562400e77e894500a128f20f" +
		"00674f7c2400b38df01800309c530900a487cf0400725bac510051af497500e4abff6e00a58ac939000775b41a001a76" +
		"e84100c5e8944400c94e8e1600330e6b39001cacbc5a00ca47cd51001b418e02000fe02a480009a32070002554164500" +
		"d7069403007d07bf3000290ccf21008726523b00e5fd1112003d03bd4c001c6831680016a3fe4200ad7ec6300028529e" +
		"3c005710de1700349b6a77004b13962f00cff00054001483f65100ab05ce6b0034174b6000bc041c0900a9b5a11a00b2" +
		"6f160300615de46100935f922800d39e4a2700596ea87000ca5764770023df7b57000b1ee85e004c456d61000bdad13b" +
		"003de28a5f008584cc2a00033ab1020025f59e4a00c3a9f64a00b8ef166500"
	rethOpenVMProgramVK = "0025e8d0440012375702004a22c350005a7cef2700f3654950004ba3266800e754e517007b9ca23d06030619068000a2" +
		"c21b53000a2fee4f0036169c2800aaaa8d6c0087fbce5b00328dc26f009fe7de5a004686562400e77e894500a128f20f" +
		"00674f7c2400b38df01800309c530900a487cf0400725bac510051af497500e4abff6e00a58ac939000775b41a001a76" +
		"e84100c5e8944400c94e8e1600330e6b39001cacbc5a00ca47cd51001b418e02000fe02a480009a32070002554164500" +
		"d7069403007d07bf3000290ccf21008726523b00e5fd1112003d03bd4c001c6831680016a3fe4200ad7ec6300028529e" +
		"3c005710de1700349b6a77004b13962f00cff00054001483f65100ab05ce6b0034174b6000bc041c0900a9b5a11a00b2" +
		"6f160300615de46100935f922800d39e4a2700596ea87000ca5764770023df7b57000b1ee85e004c456d61000bdad13b" +
		"003de28a5f008584cc2a00033ab1020025f59e4a00c3a9f64a00b8ef166500"
)

func mustHex(s string) []byte {
	data, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return data
}

// Lookup returns the registered proof type, or nil for an unassigned one.
func Lookup(id uint8) *Type {
	return types[id]
}
