package handlers

import "testing"

func TestBitlistMembers(t *testing.T) {
	members := bitlistMembers("0xff5f7ffffbfffffffffffdffb9fffffeffeffd0f")
	if len(members) != 155 {
		t.Fatalf("length %d, expected 155", len(members))
	}

	set := 0
	for _, member := range members {
		if member {
			set++
		}
	}

	if set != 144 || members[13] || !members[0] {
		t.Fatalf("unexpected members: %d set, bit 13 %v", set, members[13])
	}

	if bitlistMembers("0x00") != nil || len(bitlistMembers("0x01")) != 0 || bitlistMembers("zz") != nil {
		t.Fatalf("empty or invalid bitlists not handled")
	}
}

func TestGossipBitsMatchBeaconAPIBitlist(t *testing.T) {
	beaconAPI := &aggregateObservers{members: bitlistMembers("0xffbffffffffffffffffffffbffffffffffffff1f"), observers: 11}
	index := indexAggregateObservers([]*aggregateObservers{beaconAPI})

	if len(beaconAPI.members) != 156 {
		t.Fatalf("beacon api bitlist length %d, expected 156", len(beaconAPI.members))
	}

	for _, gossip := range []string{"0xffbffffffffffffffffffffbffffffffffffff0f", "0xffbffffffffffffffffffffbffffffffffffff1f"} {
		if index[membersKey(rawBitsMembers(gossip, 156))] != beaconAPI {
			t.Fatalf("gossip bits %s not matched", gossip)
		}
	}

	trimmed := rawBitsMembers("0xff", 12)
	if len(trimmed) != 12 || !trimmed[7] || trimmed[8] || trimmed[11] {
		t.Fatalf("trimmed bits not padded: %v", trimmed)
	}
}
