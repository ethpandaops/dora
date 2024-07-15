package models

import (
	"time"
)

// ClientsPageData is a struct to hold info for the clients page
type ClientsPageData struct {
	Clients     []*ClientsPageDataClient `json:"clients"`
	ClientCount uint64                   `json:"client_count"`
	PeerMap     *ClientPageDataPeerMap   `json:"peer_map"`
}

type ClientsPageDataClient struct {
	Index                int                          `json:"index"`
	Name                 string                       `json:"name"`
	Version              string                       `json:"version"`
	HeadSlot             uint64                       `json:"head_slot"`
	HeadRoot             []byte                       `json:"head_root"`
	Status               string                       `json:"status"`
	LastRefresh          time.Time                    `json:"refresh"`
	LastError            string                       `json:"error"`
	PeerID               string                       `json:"peer_id"`
	Peers                []*ClientPageDataClientPeers `json:"peers"`
	PeersInboundCounter  uint32                       `json:"peers_inbound_counter"`
	PeersOutboundCounter uint32                       `json:"peers_outbound_counter"`
}

type ClientPageDataClientPeers struct {
	ID        string `json:"id"`
	Alias     string `json:"alias"`
	Type      string `json:"type"`
	State     string `json:"state"`
	Direction string `json:"direction"`
}
type ClientPageDataPeerMap struct {
	ClientPageDataMapNode []*ClientPageDataPeerMapNode `json:"nodes"`
	ClientDataMapEdges    []*ClientDataMapPeerMapEdge  `json:"edges"`
}

type ClientPageDataPeerMapNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Group string `json:"group"`
	Image string `json:"image"`
	Shape string `json:"shape"`
	Value int    `json:"value"`
}

type ClientDataMapPeerMapEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Dashes bool   `json:"dashes"`
}
