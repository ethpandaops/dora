package services

import (
	"errors"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/utils"
)

func TestMain(m *testing.M) {
	utils.Config = &types.Config{}
	os.Exit(m.Run())
}

func TestFrontendCache_ProcessPageCall_Timeout(t *testing.T) {
	fc := &FrontendCacheService{
		processingDict:  make(map[string]*FrontendCacheProcessingPage),
		callStackBuffer: make([]byte, 1024*1024*1),
		cachingEnabled:  false,
	}

	// Set a very short timeout for page calls in the global config
	utils.Config.Frontend.PageCallTimeout = 20 * time.Millisecond
	defer func() {
		utils.Config.Frontend.PageCallTimeout = 0
	}()

	startGoroutines := runtime.NumGoroutine()

	// Trigger a page call with a builder that sleeps longer than the timeout
	val, err := fc.processPageCall("test-timeout-key", false, nil, func(pageCall *FrontendCacheProcessingPage) interface{} {
		// Simulate a slow DB query/processing
		select {
		case <-pageCall.CallCtx.Done():
			// Context canceled properly
			return nil
		case <-time.After(100 * time.Millisecond):
			return "slow-result"
		}
	}, &FrontendCacheProcessingPage{})

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	var cacheErr *FrontendCachePageError
	if !errors.As(err, &cacheErr) || cacheErr.Name() != "page timeout" {
		t.Fatalf("expected page timeout error, got %v", err)
	}

	if val != nil {
		t.Fatalf("expected nil result on timeout, got %v", val)
	}

	// Wait for the slow goroutine's internal timer to expire/return
	time.Sleep(150 * time.Millisecond)

	// Ensure the spawned goroutine has exited and not leaked
	endGoroutines := runtime.NumGoroutine()
	if endGoroutines > startGoroutines+3 {
		t.Errorf("suspected goroutine leak: start goroutines = %d, end goroutines = %d", startGoroutines, endGoroutines)
	}
}

func TestFrontendCache_ProcessPageCall_Success(t *testing.T) {
	fc := &FrontendCacheService{
		processingDict:  make(map[string]*FrontendCacheProcessingPage),
		callStackBuffer: make([]byte, 1024*1024*1),
		cachingEnabled:  false,
	}

	utils.Config.Frontend.PageCallTimeout = 500 * time.Millisecond
	defer func() {
		utils.Config.Frontend.PageCallTimeout = 0
	}()

	val, err := fc.processPageCall("test-success-key", false, nil, func(pageCall *FrontendCacheProcessingPage) interface{} {
		return "success-result"
	}, &FrontendCacheProcessingPage{})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if val != "success-result" {
		t.Fatalf("expected 'success-result', got %v", val)
	}
}

func TestFrontendCache_ProcessPageCall_Panic(t *testing.T) {
	fc := &FrontendCacheService{
		processingDict:  make(map[string]*FrontendCacheProcessingPage),
		callStackBuffer: make([]byte, 1024*1024*1),
		cachingEnabled:  false,
	}

	utils.Config.Frontend.PageCallTimeout = 500 * time.Millisecond
	defer func() {
		utils.Config.Frontend.PageCallTimeout = 0
	}()

	_, err := fc.processPageCall("test-panic-key", false, nil, func(pageCall *FrontendCacheProcessingPage) interface{} {
		panic("db connection lost")
	}, &FrontendCacheProcessingPage{})

	if err == nil {
		t.Fatal("expected panic recovery error, got nil")
	}

	var cacheErr *FrontendCachePageError
	if !errors.As(err, &cacheErr) || cacheErr.Name() != "page panic" {
		t.Fatalf("expected page panic error, got %v", err)
	}
}

func TestFrontendCache_ProcessPageCall_ConcurrencyLimit(t *testing.T) {
	// Setup concurrency limit of 1
	concurrencySem := make(chan struct{}, 1)
	fc := &FrontendCacheService{
		processingDict:  make(map[string]*FrontendCacheProcessingPage),
		callStackBuffer: make([]byte, 1024*1024*1),
		cachingEnabled:  false,
		concurrencySem:  concurrencySem,
	}

	utils.Config.Frontend.PageCallTimeout = 500 * time.Millisecond
	defer func() {
		utils.Config.Frontend.PageCallTimeout = 0
	}()

	// Acquire the only slot in the semaphore manually
	concurrencySem <- struct{}{}

	// The call should fail immediately with ErrTooManyPageRequests
	_, err := fc.processPageCall("test-limit-key", false, nil, func(pageCall *FrontendCacheProcessingPage) interface{} {
		return "result"
	}, &FrontendCacheProcessingPage{})

	if !errors.Is(err, ErrTooManyPageRequests) {
		t.Fatalf("expected ErrTooManyPageRequests, got %v", err)
	}
}
