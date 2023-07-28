package cache

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/coocood/freecache"
	"github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/utils"
)

// Tiered cache is a cache implementation combining a local & remote cache
type TieredCache struct {
	localGoCache *freecache.Cache
	remoteCache  RemoteCache
}

type cachedValue struct {
	Version uint64      `json:"i"`
	Timeout uint64      `json:"t"`
	Value   interface{} `json:"v"`
}

var CacheMissError error = errors.New("cache miss")

type RemoteCache interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) error
	SetBytes(ctx context.Context, key string, value []byte, expiration time.Duration) error
	SetString(ctx context.Context, key, value string, expiration time.Duration) error
	SetUint64(ctx context.Context, key string, value uint64, expiration time.Duration) error
	SetBool(ctx context.Context, key string, value bool, expiration time.Duration) error

	Get(ctx context.Context, key string, returnValue any) (any, error)
	GetBytes(ctx context.Context, key string) ([]byte, error)
	GetString(ctx context.Context, key string) (string, error)
	GetUint64(ctx context.Context, key string) (uint64, error)
	GetBool(ctx context.Context, key string) (bool, error)
}

func NewTieredCache(cacheSize int, redisAddress string, redisPrefix string) (*TieredCache, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	var remoteCache RemoteCache
	if redisAddress != "" {
		var err error
		remoteCache, err = InitRedisCache(ctx, redisAddress, redisPrefix)
		if err != nil {
			logrus.WithError(err).Errorf("error initializing remote redis cache. address: %v", redisAddress)
			return nil, err
		}
	}

	return &TieredCache{
		remoteCache:  remoteCache,
		localGoCache: freecache.NewCache(cacheSize * 1024 * 1024), // 100 MB
	}, nil
}

func (cache *TieredCache) Set(key string, value interface{}, expiration time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()
	cacheValue := cachedValue{
		Version: 1,
		Value:   value,
	}
	if expiration > 0 {
		cacheValue.Timeout = uint64(time.Now().Add(expiration).Unix())
	}

	valueMarshal, err := json.Marshal(cacheValue)
	if err != nil {
		return err
	}
	cache.localGoCache.Set([]byte(key), valueMarshal, int(expiration.Seconds()))
	if cache.remoteCache != nil {
		return cache.remoteCache.SetBytes(ctx, key, valueMarshal, expiration)
	}
	return nil
}

func (cache *TieredCache) Get(key string, returnValue interface{}) (interface{}, error) {
	cacheValue := &cachedValue{
		Value: returnValue,
	}

	// try to retrieve the key from the local cache
	wanted, err := cache.localGoCache.Get([]byte(key))
	if err == nil {
		err = json.Unmarshal([]byte(wanted), cacheValue)
		if err != nil {
			utils.LogError(err, "error unmarshalling data for key", 0, map[string]interface{}{"key": key})
			return nil, err
		}

		return returnValue, nil
	}

	if cache.remoteCache == nil {
		return nil, CacheMissError
	}

	// retrieve the key from the remote cache
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	_, err = cache.remoteCache.Get(ctx, key, cacheValue)
	if err != nil {
		return nil, err
	}

	if cacheValue.Timeout == 0 || cacheValue.Timeout > uint64(time.Now().Add(2*time.Second).Unix()) {
		valueMarshal, err := json.Marshal(cacheValue)
		if err != nil {
			return nil, err
		}
		var timeout uint64
		if cacheValue.Timeout == 0 {
			timeout = 0
		} else {
			timeout = cacheValue.Timeout - uint64(time.Now().Unix())
		}
		cache.localGoCache.Set([]byte(key), valueMarshal, int(timeout))
	}
	return returnValue, nil
}
