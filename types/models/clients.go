package models

import (
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
)

// ClientsPageData is a struct to hold info for the clients page
type ClientsPageData struct {
	Clients     []*ClientsPageDataClient `json:"clients"`
	ClientCount uint64                   `json:"client_count"`
	PeerMap     *ClientPageDataPeerMap   `json:"peer_map"`
}

type ClientsPageDataClient struct {
	Index       int        `json:"index"`
	Name        string     `json:"name"`
	Version     string     `json:"version"`
	HeadSlot    uint64     `json:"head_slot"`
	HeadRoot    []byte     `json:"head_root"`
	Status      string     `json:"status"`
	LastRefresh time.Time  `json:"refresh"`
	LastError   string     `json:"error"`
	PeerId      string     `json:"peer_id"`
	Peers       []*v1.Peer `json:"peers"`
}

type ClientPageDataPeerMap struct {
	ClientPageDataMapNode []*ClientPageDataPeerMapNode `json:"nodes"`
	ClientDataMapEdges    []*ClientDataMapPeerMapEdge  `json:"edges"`
}

type ClientPageDataPeerMapNode struct {
	Id    string `json:"id"`
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
