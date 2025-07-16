package models

import (
	"time"
)

// ClientsCLPageData is a struct to hold info for the clients page
type ClientsCLPageData struct {
	Clients                []*ClientsCLPageDataClient       `json:"clients"`
	ClientCount            uint64                           `json:"client_count"`
	PeerMap                *ClientCLPageDataPeerMap         `json:"peer_map"`
	ShowSensitivePeerInfos bool                             `json:"show_sensitive_peer_infos"`
	ShowPeerDASInfos       bool                             `json:"show_peer_das_infos"`
	PeerDASInfos           *ClientCLPagePeerDAS             `json:"peer_das"`
	Nodes                  map[string]*ClientCLPageDataNode `json:"nodes"`
	Sorting                string                           `json:"sorting"`
	IsDefaultSorting       bool                             `json:"is_default_sorting"`
	CurrentForkDigest      []byte                           `json:"current_fork_digest"`
}

// ## Peer graph data

// ClientCLPageDataPeerMap represents the data required to draw the network graph
type ClientCLPageDataPeerMap struct {
	ClientPageDataMapNode []*ClientCLPageDataPeerMapNode `json:"nodes"`
	ClientDataMapEdges    []*ClientCLDataMapPeerMapEdge  `json:"edges"`
}

// ClientCLPageDataPeerMapNode represents a node in the peer graph
type ClientCLPageDataPeerMapNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Group string `json:"group"`
	Shape string `json:"shape"`
	Value int    `json:"value"`
}

// ClientCLDataMapPeerMapEdge represents an edge in the peer graph
type ClientCLDataMapPeerMapEdge struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Interaction string `json:"interaction"`
}

// ## PeerDAS data

// ClientCLPagePeerDAS represents the DAS information from all clients and peers.
// Used to construct the PeerDAS column custody view.
type ClientCLPagePeerDAS struct {
	ColumnDistribution           map[uint64][]string             `json:"column_distribution"`              // Column index -> list of peer IDs             // Peer ID -> Peer info
	TotalRows                    int                             `json:"total_rows"`                       // Amount of rows to show on the webpage. Each row has 32 columns
	NumberOfColumns              uint64                          `json:"number_of_columns"`                // Should match NUMBER_OF_COLUMNS from spec
	CustodyRequirement           uint64                          `json:"custody_requirement"`              // Should match CUSTODY_REQUIREMENT from spec
	DataColumnSidecarSubnetCount uint64                          `json:"data_column_sidecar_subnet_count"` // Should match DATA_COLUMN_SIDECAR_SUBNET_COUNT from spec
	Warnings                     ClientCLPageDataPeerDASWarnings `json:"warnings"`
}

type ClientCLPageDataPeerDASWarnings struct {
	HasWarnings bool `json:"has_warnings"`
	// MissingENRs indicates that the client is missing ENRs for some peers
	MissingENRs      bool     `json:"missing_enrs"`
	MissingENRsPeers []string `json:"missing_enrs_peers"`
	// MissingCGCFromENR indicates that the client is missing the CGC from the ENR for some peers
	MissingCGCFromENR      bool     `json:"missing_cgc_from_enr"`
	MissingCGCFromENRPeers []string `json:"missing_cgc_from_enr_peers"`
	// MissingSpecValues indicates that wer were unable to parse the spec values, thus using defaults
	MissingSpecValues bool `json:"missing_spec_values"`
	// MissingPeersOnColumn
	EmptyColumns []uint64 `json:"missing_peers_on_column"`
}

// ## Client list data

// ClientsCLPageDataClient represents a configured endpoint CL client
type ClientsCLPageDataClient struct {
	Index                int       `json:"index"`
	Name                 string    `json:"name"`
	Version              string    `json:"version"`
	HeadSlot             uint64    `json:"head_slot"`
	HeadRoot             []byte    `json:"head_root"`
	Status               string    `json:"status"`
	LastRefresh          time.Time `json:"refresh"`
	LastError            string    `json:"error"`
	PeerID               string    `json:"peer_id"`
	NodeENR              string    `json:"node_enr"`
	PeerCount            uint32    `json:"peer_count"`
	PeersInboundCounter  uint32    `json:"peers_inbound_counter"`
	PeersOutboundCounter uint32    `json:"peers_outbound_counter"`
}

// ClientCLPageDataNode represents a generic node on the CL network. Can be a client or a peer of a client
// This is useful to generate a generic view of all nodes we know about in the network.
type ClientCLPageDataNode struct {
	PeerID            string                          `json:"peer_id"`
	NodeID            string                          `json:"node_id"`
	Type              string                          `json:"type"`  // "internal" or "external" . internal nodes are clients, external nodes are peers of clients
	Alias             string                          `json:"alias"` // only relevant for internal peers (clients)
	ENR               string                          `json:"enr"`
	ENRKeyValues      []*ClientCLPageDataNodeENRValue `json:"enr_kv"`
	MetadataKeyValues []*ClientCLPageDataNodeENRValue `json:"metadata_kv"`
	Peers             []*ClientCLPageDataNodePeers    `json:"peers"` // only relevant for internal peers
	PeerDAS           *ClientCLPageDataNodePeerDAS    `json:"peer_das"`
	PeersIn           []string                        `json:"peers_in"`
	PeersOut          []string                        `json:"peers_out"`
	Metadata          *ClientCLPageDataNodeMetadata   `json:"metadata,omitempty"`
}

// ClientCLPageDataNodeMetadata represents the metadata from the node identity
type ClientCLPageDataNodeMetadata struct {
	Attnets           string `json:"attnets,omitempty"`
	Syncnets          string `json:"syncnets,omitempty"`
	SeqNumber         string `json:"seq_number,omitempty"`
	CustodyGroupCount string `json:"custody_group_count,omitempty"` // MetadataV3 field for Fulu
}

// ClientCLPageDataNodePeers represents the peers of a client
type ClientCLPageDataNodePeers struct {
	PeerID             string                          `json:"peer_id"`
	State              string                          `json:"state"`
	Direction          string                          `json:"direction"`
	ENR                string                          `json:"enr"`
	ENRKeyValues       []*ClientCLPageDataNodeENRValue `json:"enr_kv"`
	LastSeenP2PAddress string                          `json:"last_seen_p2p_address"`
}

type ClientCLPageDataNodeENRValue struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
}

type ClientCLPageDataNodePeerDAS struct {
	CGC         uint64   `json:"cgc"`
	Columns     []uint64 `json:"columns"`
	Subnets     []uint64 `json:"subnets"`
	IsSuperNode bool     `json:"is_super_node"`
}
