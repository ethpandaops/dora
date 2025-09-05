package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/core"
	"github.com/sirupsen/logrus"
)

// LoadGenesisFromPathOrURL loads a genesis config from either a local file path or HTTP/HTTPS URL
func LoadGenesisFromPathOrURL(pathOrURL string) (*core.Genesis, error) {
	if pathOrURL == "" {
		return nil, nil
	}

	genesisLogger := logrus.StandardLogger().WithField("module", "genesis_loader")

	var data []byte
	var err error

	if strings.HasPrefix(pathOrURL, "http://") || strings.HasPrefix(pathOrURL, "https://") {
		genesisLogger.WithField("url", pathOrURL).Info("loading genesis config from URL")
		data, err = loadFromURL(pathOrURL)
	} else {
		genesisLogger.WithField("path", pathOrURL).Info("loading genesis config from file")
		data, err = loadFromFile(pathOrURL)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load genesis config: %w", err)
	}

	// Parse the genesis config using geth's structure
	genesis := &core.Genesis{}
	if err := json.Unmarshal(data, genesis); err != nil {
		return nil, fmt.Errorf("failed to parse genesis config: %w", err)
	}

	genesisLogger.Info("genesis config loaded successfully")
	return genesis, nil
}

func loadFromFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}
	return data, nil
}

func loadFromURL(url string) ([]byte, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d from URL %s", resp.StatusCode, url)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body from URL %s: %w", url, err)
	}

	return data, nil
}