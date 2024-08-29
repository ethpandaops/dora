package types

type NodeIdentity struct {
	PeerID             string   `json:"peer_id"`
	Enr                string   `json:"enr"`
	P2PAddresses       []string `json:"p2p_addresses"`
	DiscoveryAddresses []string `json:"discovery_addresses"`
	Metadata           struct {
		Attnets string `json:"attnets"`
		//SeqNumber string `json:"seq_number"` // BUG: Teku and Grandine have an int type for this field
	} `json:"metadata"`
}
