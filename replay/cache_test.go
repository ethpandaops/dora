package replay

import (
	"net/http"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestIsImmutablePath(t *testing.T) {
	root := "0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

	tests := []struct {
		path string
		want bool
	}{
		{path: "/eth/v2/debug/beacon/states/" + root, want: true},
		{path: "/eth/v2/beacon/blocks/" + root, want: true},
		{path: "/eth/v1/beacon/states/" + root + "/finality_checkpoints", want: true},
		{path: "/eth/v1/beacon/headers/49152", want: false},
		{path: "/eth/v1/beacon/headers/head", want: false},
		{path: "/eth/v1/config/spec", want: false},
		{path: "/eth/v1/node/syncing", want: false},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			require.Equal(t, test.want, isImmutablePath(test.path))
		})
	}
}

func TestArtifactCacheRoundTrip(t *testing.T) {
	cache, err := newArtifactCache(logrus.New(), t.TempDir())
	require.NoError(t, err)

	key := cache.key("/eth/v2/beacon/blocks/0xabc", "application/octet-stream")

	require.Nil(t, cache.load(key), "an unrecorded artifact must be a cache miss")

	stored := &artifact{
		body: []byte{0x01, 0x02, 0x03},
		header: http.Header{
			"Content-Type":          []string{"application/octet-stream"},
			"Eth-Consensus-Version": []string{"gloas"},
		},
	}

	cache.store(key, stored)

	loaded := cache.load(key)
	require.NotNil(t, loaded)
	require.Equal(t, stored.body, loaded.body)
	require.Equal(t, "gloas", loaded.header.Get("Eth-Consensus-Version"),
		"the fork header must survive the cache, or SSZ responses cannot be decoded")
}

func TestArtifactCacheKeyIncludesEncoding(t *testing.T) {
	cache, err := newArtifactCache(logrus.New(), t.TempDir())
	require.NoError(t, err)

	json := cache.key("/eth/v2/beacon/blocks/0xabc", "application/json")
	ssz := cache.key("/eth/v2/beacon/blocks/0xabc", "application/octet-stream")

	require.NotEqual(t, json, ssz, "the same artifact in two encodings must not share a cache entry")
}
