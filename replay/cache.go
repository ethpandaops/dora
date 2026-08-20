package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

// artifactCache records the immutable artifacts a replay fetched, so a slot range is
// downloaded from the devnet once and replays offline and reproducibly afterwards.
type artifactCache struct {
	logger logrus.FieldLogger
	dir    string
}

func newArtifactCache(logger logrus.FieldLogger, dir string) (*artifactCache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("could not create cache dir %v: %w", dir, err)
	}

	return &artifactCache{logger: logger, dir: dir}, nil
}

// isImmutablePath reports whether a response for this path can never change. Only
// root-addressed artifacts qualify: slot-addressed ones would go stale across a reorg,
// and everything under /node or /config reflects the upstream's live state.
func isImmutablePath(path string) bool {
	segments := strings.Split(strings.Trim(path, "/"), "/")

	for _, segment := range segments {
		if strings.HasPrefix(segment, "0x") && len(segment) == 66 {
			return true
		}
	}

	return false
}

func (c *artifactCache) key(path, accept string) string {
	sum := sha256.Sum256([]byte(path + "\x00" + accept))

	return hex.EncodeToString(sum[:])
}

type cacheMeta struct {
	Header http.Header `json:"header"`
}

func (c *artifactCache) paths(key string) (string, string) {
	dir := filepath.Join(c.dir, key[:2])

	return filepath.Join(dir, key+".bin"), filepath.Join(dir, key+".json")
}

// load returns a cached artifact, or nil when it is not recorded.
func (c *artifactCache) load(key string) *artifact {
	bodyPath, metaPath := c.paths(key)

	body, err := os.ReadFile(bodyPath)
	if err != nil {
		return nil
	}

	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return nil
	}

	meta := cacheMeta{}
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return nil
	}

	return &artifact{body: body, header: meta.Header}
}

func (c *artifactCache) store(key string, art *artifact) {
	bodyPath, metaPath := c.paths(key)

	if err := os.MkdirAll(filepath.Dir(bodyPath), 0o755); err != nil {
		c.logger.WithError(err).Debugf("could not create cache subdir for %v", key)
		return
	}

	metaData, err := json.Marshal(cacheMeta{Header: art.header})
	if err != nil {
		c.logger.WithError(err).Debugf("could not encode cache meta for %v", key)
		return
	}

	// write the body first: a body without meta is simply treated as a cache miss,
	// while meta without a body would be too
	if err := os.WriteFile(bodyPath, art.body, 0o644); err != nil {
		c.logger.WithError(err).Debugf("could not write cache body for %v", key)
		return
	}

	if err := os.WriteFile(metaPath, metaData, 0o644); err != nil {
		c.logger.WithError(err).Debugf("could not write cache meta for %v", key)
	}
}
