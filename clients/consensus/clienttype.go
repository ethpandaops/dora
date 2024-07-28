package consensus

import (
	"fmt"
	"regexp"
)

type ClientType int8

var (
	AnyClient        ClientType
	UnknownClient    ClientType = -1
	LighthouseClient ClientType = 1
	LodestarClient   ClientType = 2
	NimbusClient     ClientType = 3
	PrysmClient      ClientType = 4
	TekuClient       ClientType = 5
	GrandineClient   ClientType = 6
)
var clientTypePatterns = map[ClientType]*regexp.Regexp{
	LighthouseClient: regexp.MustCompile("(?i)^Lighthouse/.*"),
	LodestarClient:   regexp.MustCompile("(?i)^Lodestar/.*"),
	NimbusClient:     regexp.MustCompile("(?i)^Nimbus/.*"),
	PrysmClient:      regexp.MustCompile("(?i)^Prysm/.*"),
	TekuClient:       regexp.MustCompile("(?i)^teku/.*"),
	GrandineClient:   regexp.MustCompile("(?i)^Grandine/.*"),
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
	case "lighthouse":
		return LighthouseClient
	case "lodestar":
		return LodestarClient
	case "nimbus":
		return NimbusClient
	case "prysm":
		return PrysmClient
	case "teku":
		return TekuClient
	case "grandine":
		return GrandineClient
	default:
		return UnknownClient
	}
}

func (client *Client) GetClientType() ClientType {
	return client.clientType
}

func (clientType ClientType) String() string {
	switch clientType {
	case LighthouseClient:
		return "lighthouse"
	case LodestarClient:
		return "lodestar"
	case NimbusClient:
		return "nimbus"
	case PrysmClient:
		return "prysm"
	case TekuClient:
		return "teku"
	case GrandineClient:
		return "grandine"
	default:
		return fmt.Sprintf("unknown: %d", clientType)
	}
}
