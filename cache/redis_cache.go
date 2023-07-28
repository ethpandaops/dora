package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/pk910/light-beaconchain-explorer/utils"
)

type RedisCache struct {
	redisRemoteCache *redis.Client
	keyPrefix        string
}

func InitRedisCache(ctx context.Context, redisAddress string, keyPrefix string) (*RedisCache, error) {
	rdc := redis.NewClient(&redis.Options{
		Addr:        redisAddress,
		ReadTimeout: time.Second * 20,
	})

	if err := rdc.Ping(ctx).Err(); err != nil {
		return nil, err
	}

	r := &RedisCache{
		redisRemoteCache: rdc,
		keyPrefix:        keyPrefix,
	}
	return r, nil
}

func (cache *RedisCache) SetString(ctx context.Context, key, value string, expiration time.Duration) error {
	return cache.redisRemoteCache.Set(ctx, fmt.Sprintf("%s%s", cache.keyPrefix, key), value, expiration).Err()
}

func (cache *RedisCache) GetString(ctx context.Context, key string) (string, error) {

	value, err := cache.redisRemoteCache.Get(ctx, fmt.Sprintf("%s%s", cache.keyPrefix, key)).Result()
	if err != nil {
		return "", err
	}

	return value, nil
}

func (cache *RedisCache) SetUint64(ctx context.Context, key string, value uint64, expiration time.Duration) error {
	return cache.redisRemoteCache.Set(ctx, fmt.Sprintf("%s%s", cache.keyPrefix, key), fmt.Sprintf("%d", value), expiration).Err()
}

func (cache *RedisCache) GetUint64(ctx context.Context, key string) (uint64, error) {

	value, err := cache.redisRemoteCache.Get(ctx, fmt.Sprintf("%s%s", cache.keyPrefix, key)).Result()
	if err != nil {
		return 0, err
	}

	returnValue, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, err
	}
	return returnValue, nil
}

func (cache *RedisCache) SetBool(ctx context.Context, key string, value bool, expiration time.Duration) error {
	return cache.redisRemoteCache.Set(ctx, fmt.Sprintf("%s%s", cache.keyPrefix, key), fmt.Sprintf("%t", value), expiration).Err()
}

func (cache *RedisCache) GetBool(ctx context.Context, key string) (bool, error) {

	value, err := cache.redisRemoteCache.Get(ctx, key).Result()
	if err != nil {
		return false, err
	}

	returnValue, err := strconv.ParseBool(value)
	if err != nil {
		return false, err
	}
	return returnValue, nil
}

func (cache *RedisCache) SetBytes(ctx context.Context, key string, value []byte, expiration time.Duration) error {
	return cache.redisRemoteCache.Set(ctx, fmt.Sprintf("%s%s", cache.keyPrefix, key), value, expiration).Err()
}

func (cache *RedisCache) GetBytes(ctx context.Context, key string) ([]byte, error) {
	value, err := cache.redisRemoteCache.Get(ctx, fmt.Sprintf("%s%s", cache.keyPrefix, key)).Result()
	if err != nil {
		return nil, err
	}
	return []byte(value), nil
}

func (cache *RedisCache) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	valueMarshal, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return cache.redisRemoteCache.Set(ctx, fmt.Sprintf("%s%s", cache.keyPrefix, key), valueMarshal, expiration).Err()
}

func (cache *RedisCache) Get(ctx context.Context, key string, returnValue interface{}) (interface{}, error) {
	value, err := cache.redisRemoteCache.Get(ctx, fmt.Sprintf("%s%s", cache.keyPrefix, key)).Result()
	if err != nil {
		return nil, err
	}

	err = json.Unmarshal([]byte(value), returnValue)
	if err != nil {
		cache.redisRemoteCache.Del(ctx, fmt.Sprintf("%s%s", cache.keyPrefix, key)).Err()
		utils.LogError(err, "error unmarshalling data for key", 0, map[string]interface{}{"key": key})
		return nil, err
	}

	return returnValue, nil
}
