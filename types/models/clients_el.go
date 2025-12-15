package models

import (
	"time"
)

// ClientsELPageData is a struct to hold info for the clients page
type ClientsELPageData struct {
	Clients                []*ClientsELPageDataClient        `json:"clients"`
	ClientCount            uint64                            `json:"client_count"`
	PeerMap                *ClientELPageDataPeerMap          `json:"peer_map"`
	ShowSensitivePeerInfos bool                              `json:"show_sensitive_peer_infos"`
	Nodes                  map[string]*ClientsELPageDataNode `json:"nodes"`
	Sorting                string                            `json:"sorting"`
	IsDefaultSorting       bool                              `json:"is_default_sorting"`
	ExpectedEthConfig      *ClientELPageDataForkConfig       `json:"expected_eth_config"`
}

type ClientsELPageDataClient struct {
	Index                int                         `json:"index"`
	Name                 string                      `json:"name"`
	Version              string                      `json:"version"`
	HeadSlot             uint64                      `json:"head_slot"`
	HeadRoot             []byte                      `json:"head_root"`
	Status               string                      `json:"status"`
	LastRefresh          time.Time                   `json:"refresh"`
	LastError            string                      `json:"error"`
	PeerCount            uint32                      `json:"peer_count"`
	DidFetchPeers        bool                        `json:"peers_fetched"`
	PeersInboundCounter  uint32                      `json:"peers_inbound_counter"`
	PeersOutboundCounter uint32                      `json:"peers_outbound_counter"`
	PeerID               string                      `json:"peer_id"`
	ConfigWarnings       []string                    `json:"config_warnings"`
	ClientConfig         *ClientELPageDataForkConfig `json:"client_config"`
}

type ClientsELPageDataNode struct {
	PeerID        string                       `json:"peer_id"`
	Name          string                       `json:"name"`
	Version       string                       `json:"version"`
	Status        string                       `json:"status"`
	PeerName      string                       `json:"peer_name"`
	Enode         string                       `json:"enode"`
	IPAddr        string                       `json:"ip_addr"`
	ListenAddr    string                       `json:"listen_addr"`
	Peers         []*ClientELPageDataNodePeers `json:"peers"`
	DidFetchPeers bool                         `json:"peers_fetched"`
	ForkConfig    *ClientELPageDataForkConfig  `json:"fork_config"`
}

type ClientELPageDataForkConfig struct {
	Current *EthConfigObject `json:"current"`
	Next    *EthConfigObject `json:"next"`
	Last    *EthConfigObject `json:"last"`
}

type EthConfigObject struct {
	ActivationTime uint64 `json:"activationTime"`
	BlobSchedule   struct {
		Max                   uint64 `json:"max"`
		Target                uint64 `json:"target"`
		BaseFeeUpdateFraction uint64 `json:"baseFeeUpdateFraction"`
	} `json:"blobSchedule"`
	ChainId         string            `json:"chainId"`
	ForkId          string            `json:"forkId"`
	Precompiles     map[string]string `json:"precompiles"`
	SystemContracts map[string]string `json:"systemContracts"`
}

type ClientELPageDataNodePeers struct {
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
	Value int    `json:"value"`
}

type ClientELDataMapPeerMapEdge struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Interaction string `json:"interaction"`
}
