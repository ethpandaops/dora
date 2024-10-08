package models

import (
	"time"
)

// ClientsCLPageData is a struct to hold info for the clients page
type ClientsCLPageData struct {
	Clients                []*ClientsCLPageDataClient `json:"clients"`
	ClientCount            uint64                     `json:"client_count"`
	PeerMap                *ClientCLPageDataPeerMap   `json:"peer_map"`
	ShowSensitivePeerInfos bool                       `json:"show_sensitive_peer_infos"`
}

type ClientsCLPageDataClient struct {
	Index                 int                            `json:"index"`
	Name                  string                         `json:"name"`
	Version               string                         `json:"version"`
	HeadSlot              uint64                         `json:"head_slot"`
	HeadRoot              []byte                         `json:"head_root"`
	Status                string                         `json:"status"`
	LastRefresh           time.Time                      `json:"refresh"`
	LastError             string                         `json:"error"`
	PeerID                string                         `json:"peer_id"`
	ENR                   string                         `json:"enr"`
	ENRKeyValues          map[string]interface{}         `json:"enr_kv"`
	NodeID                string                         `json:"node_id"`
	P2PAddresses          []string                       `json:"p2p_addresses"`
	DisoveryAddresses     []string                       `json:"discovery_addresses"`
	AttestationSubnetSubs string                         `json:"attestation_subnets_subs"`
	Peers                 []*ClientCLPageDataClientPeers `json:"peers"`
	PeersInboundCounter   uint32                         `json:"peers_inbound_counter"`
	PeersOutboundCounter  uint32                         `json:"peers_outbound_counter"`
}

type ClientCLPageDataClientPeers struct {
	ID                 string                 `json:"id"`
	Alias              string                 `json:"alias"`
	Type               string                 `json:"type"`
	State              string                 `json:"state"`
	Direction          string                 `json:"direction"`
	ENR                string                 `json:"enr"`
	ENRKeyValues       map[string]interface{} `json:"enr_kv"`
	NodeID             string                 `json:"node_id"`
	LastSeenP2PAddress string                 `json:"last_seen_p2p_address"`
}
type ClientCLPageDataPeerMap struct {
	ClientPageDataMapNode []*ClientCLPageDataPeerMapNode `json:"nodes"`
	ClientDataMapEdges    []*ClientCLDataMapPeerMapEdge  `json:"edges"`
}

type ClientCLPageDataPeerMapNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Group string `json:"group"`
	Shape string `json:"shape"`
	Value int    `json:"value"`
}

type ClientCLDataMapPeerMapEdge struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Interaction string `json:"interaction"`
}
