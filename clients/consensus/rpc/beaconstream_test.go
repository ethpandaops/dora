package rpc

import (
	"fmt"
	"testing"
)

// bidEventPayload is a SignedExecutionPayloadBid container as sent by Nimbus
// (captured from a glamsterdam-devnet-7 node), which omits the versioned
// envelope other clients wrap the event in.
const bidEventPayload = `{"message":{"parent_block_hash":"0xd82482d1aef3ee998e6a638ddc8ee7f7540aa121af3a1e89f65fed989f56e449","parent_block_root":"0xf9b85fb511fa7a8561fac828fc390e61a64804e851da4816d7a62cc4648d3e2e","block_hash":"0xd18d394228734ff07fd17542df9294c86b779999805e7895379b9f6699b68487","prev_randao":"0xf66c5be439378224329f796401778ab62f725192604ba4a4a5be5b4514d04817","fee_recipient":"0xf97e180c050e5ab072211ad2c213eb5aee4df134","gas_limit":"300000000","builder_index":"3","slot":"168775","value":"471708347","execution_payment":"0","blob_kzg_commitments":["0xa95caabd009e189b9f205e0328ff847ad886e4f8e719bd7219875fbb9688fb3fbe7704bb1dfa7e2993a3dea8d0cf767d"],"execution_requests_root":"0x87b69a306c8e430d0857f7c4ac5e27cecffa1108d43c2e5df7388056fea7a423"},"signature":"0xb399a174693747e9437ba1d94be3ad3e7556a388a6d3e4e7589cea7310735e04296086a957f9a3652d5e14731561151900f4532f72227b60f06449068d610794e8ee273bd00b3179030242096fb95a17cf0277528c7c6c8ddf5a408fca069ad2"}`

func TestParseExecutionPayloadBidEvent(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "versioned envelope",
			data: fmt.Sprintf(`{"version":"gloas","data":%s}`, bidEventPayload),
		},
		{
			name: "bare container (nimbus)",
			data: bidEventPayload,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bid, err := parseExecutionPayloadBidEvent([]byte(tt.data))
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if bid.Message == nil {
				t.Fatal("parsed bid has no message")
			}
			if uint64(bid.Message.Slot) != 168775 {
				t.Errorf("unexpected slot: %d", bid.Message.Slot)
			}
			if uint64(bid.Message.BuilderIndex) != 3 {
				t.Errorf("unexpected builder index: %d", bid.Message.BuilderIndex)
			}
			if uint64(bid.Message.Value) != 471708347 {
				t.Errorf("unexpected value: %d", bid.Message.Value)
			}
		})
	}
}
