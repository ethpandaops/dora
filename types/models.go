package types

import "html/template"

// PageData is a struct to hold web page data
type PageData struct {
	Active                  string
	Meta                    *Meta
	Data                    interface{}
	Version                 string
	BuildTime               string
	ServerTime              int64
	Year                    int
	ExplorerLogo            string
	ExplorerTitle           string
	ExplorerSubtitle        string
	ChainSlotsPerEpoch      uint64
	ChainSlotDurationMs     uint64
	ChainGenesisTimestamp   uint64
	CurrentEpoch            uint64
	LatestFinalizedEpoch    uint64
	CurrentSlot             uint64
	FinalizationDelay       uint64
	IsReady                 bool
	Mainnet                 bool
	DepositContract         string
	InfoBanner              *template.HTML
	ClientsUpdated          bool
	Lang                    string
	NoAds                   bool
	Debug                   bool
	DebugTemplates          []string
	MainMenuItems           []MainMenuItem
	ApiEnabled              bool
	ExecutionIndexerEnabled bool
	EnsSearchEnabled        bool

	// ReplayControlUrl is the dora-replay control server this explorer is driven by.
	// It is only set in replay mode, and it is the whole of the explorer's knowledge
	// about the replay UI: the layout side-loads that UI from the replay process and
	// hands it this address to talk to.
	ReplayControlUrl string
}

type MainMenuItem struct {
	Label        string
	Path         string
	IsActive     bool
	HasBigGroups bool // if HasBigGroups is set to true then the NavigationGroups will be ordered horizontally and their Label will be shown
	Groups       []NavigationGroup
}

type NavigationGroup struct {
	Label string // only used for "BigGroups"
	Links []NavigationLink
}

type NavigationLink struct {
	Label         string
	Path          string
	CustomIcon    string
	Icon          string
	IsHidden      bool
	IsHighlighted bool
}

// Meta is a struct to hold metadata about the page
type Meta struct {
	Title       string
	Description string
	Domain      string
	Path        string
	Tlabel1     string
	Tdata1      string
	Tlabel2     string
	Tdata2      string
	Templates   string
}

type Empty struct {
}

type NamedValidator struct {
	Index uint64 `json:"index"`
	Name  string `json:"name"`
}

type SystemContract struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}
