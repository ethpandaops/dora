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

func TestAggregateObserversMatchTruncatedBits(t *testing.T) {
	full := &aggregateObservers{members: bitlistMembers("0xffbffffffffffffffffffffbffffffffffffff1f"), observers: 11}
	index := indexAggregateObservers([]*aggregateObservers{full})

	if index[membersKey(bitlistMembers("0xffbffffffffffffffffffffbffffffffffffff0f"))] != full {
		t.Fatalf("truncated gossip bits not matched")
	}

	if index[membersKey(full.members)] != full {
		t.Fatalf("full bits not matched")
	}
}
