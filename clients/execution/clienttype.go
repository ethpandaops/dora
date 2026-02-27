package execution

import (
	"fmt"
	"regexp"
)

type ClientType int8

var (
	AnyClient        ClientType
	UnknownClient    ClientType = -1
	BesuClient       ClientType = 1
	ErigonClient     ClientType = 2
	EthjsClient      ClientType = 3
	GethClient       ClientType = 4
	NethermindClient ClientType = 5
	RethClient       ClientType = 6
	NimbusELClient   ClientType = 7
	EthrexClient     ClientType = 8
)
var clientTypePatterns = map[ClientType]*regexp.Regexp{
	BesuClient:       regexp.MustCompile("(?i)^Besu/.*"),
	ErigonClient:     regexp.MustCompile("(?i)^Erigon/.*"),
	EthjsClient:      regexp.MustCompile("(?i)^Ethereumjs/.*"),
	GethClient:       regexp.MustCompile("(?i)^Geth/.*"),
	NethermindClient: regexp.MustCompile("(?i)^Nethermind/.*"),
	RethClient:       regexp.MustCompile("(?i)^Reth/.*"),
	NimbusELClient:   regexp.MustCompile("(?i)^(nimbus-eth1|NimbusExecutionClient)/.*"),
	EthrexClient:     regexp.MustCompile("(?i)^ethrex/.*"),
}

func (t ClientType) Uint8() uint8 {
	if t == UnknownClient {
		return 0
	}

	return uint8(t)
}

func (client *Client) parseClientVersion(version string) {
	for clientType, versionPattern := range clientTypePatterns {
		if versionPattern.MatchString(version) {
			client.clientType = clientType
			return
		}
	}

	client.clientType = UnknownClient
}

func ParseClientType(name string) ClientType {
	switch name {
	case "besu":
		return BesuClient
	case "erigon":
		return ErigonClient
	case "ethjs":
		return EthjsClient
	case "geth":
		return GethClient
	case "nethermind":
		return NethermindClient
	case "reth":
		return RethClient
	case "nimbusel":
		return NimbusELClient
	case "ethrex":
		return EthrexClient
	default:
		return UnknownClient
	}
}

func (client *Client) GetClientType() ClientType {
	return client.clientType
}

func (clientType ClientType) String() string {
	switch clientType {
	case BesuClient:
		return "besu"
	case ErigonClient:
		return "erigon"
	case EthjsClient:
		return "ethjs"
	case GethClient:
		return "geth"
	case NethermindClient:
		return "nethermind"
	case RethClient:
		return "reth"
	case NimbusELClient:
		return "nimbusel"
	case EthrexClient:
		return "ethrex"
	default:
		return fmt.Sprintf("unknown: %d", clientType)
	}
}
