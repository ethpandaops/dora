package types

import "time"

// Config is a struct to hold the configuration data
type Config struct {
	Server struct {
		Port string `yaml:"port" envconfig:"FRONTEND_SERVER_PORT"`
		Host string `yaml:"host" envconfig:"FRONTEND_SERVER_HOST"`
	} `yaml:"server"`

	Chain struct {
		Name                  string `yaml:"name" envconfig:"CHAIN_NAME"`
		GenesisTimestamp      uint64 `yaml:"genesisTimestamp" envconfig:"CHAIN_GENESIS_TIMESTAMP"`
		GenesisValidatorsRoot string `yaml:"genesisValidatorsRoot" envconfig:"CHAIN_GENESIS_VALIDATORS_ROOT"`
		ConfigPath            string `yaml:"configPath" envconfig:"CHAIN_CONFIG_PATH"`
		Config                ChainConfig
	} `yaml:"chain"`

	Frontend struct {
		Enabled bool `yaml:"enabled" envconfig:"FRONTEND_ENABLED"`
		Debug   bool `yaml:"debug" envconfig:"FRONTEND_DEBUG"`

		SiteDomain   string `yaml:"siteDomain" envconfig:"FRONTEND_SITE_DOMAIN"`
		SiteName     string `yaml:"siteName" envconfig:"FRONTEND_SITE_NAME"`
		SiteSubtitle string `yaml:"siteSubtitle" envconfig:"FRONTEND_SITE_SUBTITLE"`

		HttpReadTimeout  time.Duration `yaml:"httpReadTimeout" envconfig:"FRONTEND_HTTP_READ_TIMEOUT"`
		HttpWriteTimeout time.Duration `yaml:"httpWriteTimeout" envconfig:"FRONTEND_HTTP_WRITE_TIMEOUT"`
		HttpIdleTimeout  time.Duration `yaml:"httpIdleTimeout" envconfig:"FRONTEND_HTTP_IDLE_TIMEOUT"`
	} `yaml:"frontend"`

	BeaconApi struct {
		Endpoint string `yaml:"endpoint" envconfig:"BEACONAPI_ENDPOINT"`
	} `yaml:"beaconapi"`
}
