package types

import "time"

// Config is a struct to hold the configuration data
type Config struct {
	Server struct {
		Port string `yaml:"port" envconfig:"FRONTEND_SERVER_PORT"`
		Host string `yaml:"host" envconfig:"FRONTEND_SERVER_HOST"`
	} `yaml:"server"`

	Chain struct {
		Name             string `yaml:"name" envconfig:"CHAIN_NAME"`
		DisplayName      string `yaml:"displayName" envconfig:"CHAIN_DISPLAY_NAME"`
		GenesisTimestamp uint64 `yaml:"genesisTimestamp" envconfig:"CHAIN_GENESIS_TIMESTAMP"`
		ConfigPath       string `yaml:"configPath" envconfig:"CHAIN_CONFIG_PATH"`
		Config           ChainConfig
	} `yaml:"chain"`

	Frontend struct {
		Enabled bool `yaml:"enabled" envconfig:"FRONTEND_ENABLED"`
		Debug   bool `yaml:"debug" envconfig:"FRONTEND_DEBUG"`

		SiteDomain   string `yaml:"siteDomain" envconfig:"FRONTEND_SITE_DOMAIN"`
		SiteName     string `yaml:"siteName" envconfig:"FRONTEND_SITE_NAME"`
		SiteSubtitle string `yaml:"siteSubtitle" envconfig:"FRONTEND_SITE_SUBTITLE"`

		EthExplorerLink         string `yaml:"ethExplorerLink" envconfig:"FRONTEND_ETH_EXPLORER_LINK"`
		ValidatorNamesYaml      string `yaml:"validatorNamesYaml" envconfig:"FRONTEND_VALIDATOR_NAMES_YAML"`
		ValidatorNamesInventory string `yaml:"validatorNamesInventory" envconfig:"FRONTEND_VALIDATOR_NAMES_INVENTORY"`

		HttpReadTimeout  time.Duration `yaml:"httpReadTimeout" envconfig:"FRONTEND_HTTP_READ_TIMEOUT"`
		HttpWriteTimeout time.Duration `yaml:"httpWriteTimeout" envconfig:"FRONTEND_HTTP_WRITE_TIMEOUT"`
		HttpIdleTimeout  time.Duration `yaml:"httpIdleTimeout" envconfig:"FRONTEND_HTTP_IDLE_TIMEOUT"`
	} `yaml:"frontend"`

	BeaconApi struct {
		Endpoint string `yaml:"endpoint" envconfig:"BEACONAPI_ENDPOINT"`

		LocalCacheSize       int    `yaml:"localCacheSize" envconfig:"BEACONAPI_LOCAL_CACHE_SIZE"`
		AssignmentsCacheSize int    `yaml:"assignmentsCacheSize" envconfig:"BEACONAPI_ASSIGNMENTS_CACHE_SIZE"`
		RedisCacheAddr       string `yaml:"redisCacheAddr" envconfig:"BEACONAPI_REDIS_CACHE_ADDR"`
		RedisCachePrefix     string `yaml:"redisCachePrefix" envconfig:"BEACONAPI_REDIS_CACHE_PREFIX"`
	} `yaml:"beaconapi"`

	Indexer struct {
		PrepopulateEpochs    uint16 `yaml:"prepopulateEpochs" envconfig:"INDEXER_PREPOPULATE_EPOCHS"`
		InMemoryEpochs       uint16 `yaml:"inMemoryEpochs" envconfig:"INDEXER_IN_MEMORY_EPOCHS"`
		EpochProcessingDelay uint16 `yaml:"epochProcessingDelay" envconfig:"INDEXER_EPOCH_PROCESSING_DELAY"`
		DisableIndexWriter   bool   `yaml:"disableIndexWriter" envconfig:"INDEXER_DISABLE_INDEX_WRITER"`
		SyncEpochCooldown    uint   `yaml:"syncEpochCooldown" envconfig:"INDEXER_SYNC_EPOCH_COOLDOWN"`
	} `yaml:"indexer"`

	ReaderDatabase struct {
		Username     string `yaml:"user" envconfig:"READER_DB_USERNAME"`
		Password     string `yaml:"password" envconfig:"READER_DB_PASSWORD"`
		Name         string `yaml:"name" envconfig:"READER_DB_NAME"`
		Host         string `yaml:"host" envconfig:"READER_DB_HOST"`
		Port         string `yaml:"port" envconfig:"READER_DB_PORT"`
		MaxOpenConns int    `yaml:"maxOpenConns" envconfig:"READER_DB_MAX_OPEN_CONNS"`
		MaxIdleConns int    `yaml:"maxIdleConns" envconfig:"READER_DB_MAX_IDLE_CONNS"`
	} `yaml:"readerDatabase"`
	WriterDatabase struct {
		Username     string `yaml:"user" envconfig:"WRITER_DB_USERNAME"`
		Password     string `yaml:"password" envconfig:"WRITER_DB_PASSWORD"`
		Name         string `yaml:"name" envconfig:"WRITER_DB_NAME"`
		Host         string `yaml:"host" envconfig:"WRITER_DB_HOST"`
		Port         string `yaml:"port" envconfig:"WRITER_DB_PORT"`
		MaxOpenConns int    `yaml:"maxOpenConns" envconfig:"WRITER_DB_MAX_OPEN_CONNS"`
		MaxIdleConns int    `yaml:"maxIdleConns" envconfig:"WRITER_DB_MAX_IDLE_CONNS"`
	} `yaml:"writerDatabase"`
}

type DatabaseConfig struct {
	Username     string
	Password     string
	Name         string
	Host         string
	Port         string
	MaxOpenConns int
	MaxIdleConns int
}
