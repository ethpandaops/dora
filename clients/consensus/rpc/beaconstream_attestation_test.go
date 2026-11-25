package rpc

import (
	"encoding/json"
	"testing"
)

func TestSingleAttestationEventUnmarshal(t *testing.T) {
	input := `{"committee_index":"0","attester_index":"2300","data":{"slot":"7162","index":"1","beacon_block_root":"0xe2f4346fcd3303f0aec74f5bb0ae0106cd831c1f2427dcce3c53d04dac1f5472","source":{"epoch":"126","root":"0x00"},"target":{"epoch":"223","root":"0x00"}},"signature":"0xdf80"}`

	var evt SingleAttestationEvent
	if err := json.Unmarshal([]byte(input), &evt); err != nil {
		t.Fatal(err)
	}

	if evt.AttesterIndex != 2300 || evt.Slot != 7162 || evt.BeaconBlockRoot[0] != 0xe2 || evt.BeaconBlockRoot[31] != 0x72 {
		t.Fatalf("unexpected event: %+v", evt)
	}

	if err := json.Unmarshal([]byte(`{"attester_index":"1","data":{"slot":"1","beacon_block_root":"0x00"}}`), &evt); err == nil {
		t.Fatal("expected an error for a short root")
	}
}
