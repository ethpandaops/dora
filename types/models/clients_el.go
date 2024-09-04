package models

import (
	"time"
)

// ClientsELPageData is a struct to hold info for the clients page
type ClientsELPageData struct {
	Clients                []*ClientsELPageDataClient `json:"clients"`
	ClientCount            uint64                     `json:"client_count"`
	PeerMap                *ClientELPageDataPeerMap   `json:"peer_map"`
	ShowSensitivePeerInfos bool                       `json:"show_sensitive_peer_infos"`
}

type ClientsELPageDataClient struct {
	Index                int                            `json:"index"`
	Name                 string                         `json:"name"`
	Version              string                         `json:"version"`
	HeadSlot             uint64                         `json:"head_slot"`
	HeadRoot             []byte                         `json:"head_root"`
	Status               string                         `json:"status"`
	LastRefresh          time.Time                      `json:"refresh"`
	LastError            string                         `json:"error"`
	PeerID               string                         `json:"peer_id"`
	PeerName             string                         `json:"peer_name"`
	Enode                string                         `json:"enode"`
	IPAddr               string                         `json:"ip_addr"`
	ListenAddr           string                         `json:"listen_addr"`
	Peers                []*ClientELPageDataClientPeers `json:"peers"`
	DidFetchPeers        bool                           `json:"peers_fetched"`
	PeersInboundCounter  uint32                         `json:"peers_inbound_counter"`
	PeersOutboundCounter uint32                         `json:"peers_outbound_counter"`
}

type ClientELPageDataClientPeers struct {
	ID        string                 `json:"id"`
	Alias     string                 `json:"alias"`
	Enode     string                 `json:"enode"`
	Name      string                 `json:"name"`
	Type      string                 `json:"type"`
	State     string                 `json:"state"`
	Direction string                 `json:"direction"`
	Caps      []string               `json:"caps"`
	Protocols map[string]interface{} `json:"protocols"`
}
type ClientELPageDataPeerMap struct {
	ClientPageDataMapNode []*ClientELPageDataPeerMapNode `json:"nodes"`
	ClientDataMapEdges    []*ClientELDataMapPeerMapEdge  `json:"edges"`
}

type ClientELPageDataPeerMapNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Group string `json:"group"`
	Shape string `json:"shape"`
	Value int    `json:"value"`
}

type ClientELDataMapPeerMapEdge struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Interaction string `json:"interaction"`
}
