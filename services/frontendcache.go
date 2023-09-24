package services

import (
	"bufio"
	"bytes"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/pk910/dora-the-explorer/cache"
	"github.com/pk910/dora-the-explorer/utils"
	"github.com/sirupsen/logrus"
)

type FrontendCacheService struct {
	pageCallCounter      uint64
	pageCallCounterMutex sync.Mutex
	tieredCache          *cache.TieredCache
	processingMutex      sync.Mutex
	processingDict       map[string]*FrontendCacheProcessingPage
	callStackMutex       sync.RWMutex
	callStackBuffer      []byte
}

type FrontendCacheProcessingPage struct {
	modelMutex   sync.RWMutex
	pageModel    interface{}
	pageError    error
	PageKey      string
	CacheTimeout time.Duration
}

type PageDataHandlerFn = func(pageCall *FrontendCacheProcessingPage) interface{}

var GlobalFrontendCache *FrontendCacheService

type FrontendCachePageError struct {
	err   error
	name  string
	stack string
}

func (e FrontendCachePageError) Error() string {
	return e.err.Error()
}
func (e FrontendCachePageError) Name() string {
	return e.name
}
func (e FrontendCachePageError) Stack() string {
	return e.stack
}

// StartFrontendCache is used to start the global frontend cache service
func StartFrontendCache() error {
	if GlobalFrontendCache != nil {
		return nil
	}

	cachePrefix := fmt.Sprintf("%sgui-", utils.Config.BeaconApi.RedisCachePrefix)
	tieredCache, err := cache.NewTieredCache(utils.Config.BeaconApi.LocalCacheSize, utils.Config.BeaconApi.RedisCacheAddr, cachePrefix)
	if err != nil {
		return err
	}

	GlobalFrontendCache = &FrontendCacheService{
		tieredCache:     tieredCache,
		processingDict:  make(map[string]*FrontendCacheProcessingPage),
		callStackBuffer: make([]byte, 1024*1024*5),
	}
	return nil
}

func (fc *FrontendCacheService) ProcessCachedPage(pageKey string, caching bool, returnValue interface{}, buildFn PageDataHandlerFn) (interface{}, error) {
	//fmt.Printf("page call %v (goid: %v)\n", pageKey, utils.Goid())

	fc.processingMutex.Lock()
	processingPage := fc.processingDict[pageKey]
	if processingPage != nil {
		fc.processingMutex.Unlock()
		logrus.Debugf("page already processing: %v", pageKey)

		processingPage.modelMutex.RLock()
		defer processingPage.modelMutex.RUnlock()
		return processingPage.pageModel, processingPage.pageError
	}
	processingPage = &FrontendCacheProcessingPage{
		PageKey:      pageKey,
		CacheTimeout: -1,
	}
	fc.processingDict[pageKey] = processingPage
	processingPage.modelMutex.Lock()
	defer fc.completePageLoad(pageKey, processingPage)
	fc.processingMutex.Unlock()

	var returnError error
	returnValue, returnError = fc.processPageCall(pageKey, caching, returnValue, buildFn, processingPage)
	processingPage.pageModel = returnValue
	processingPage.pageError = returnError
	return returnValue, returnError
}

func (fc *FrontendCacheService) processPageCall(pageKey string, caching bool, pageData interface{}, buildFn PageDataHandlerFn, pageCall *FrontendCacheProcessingPage) (interface{}, error) {
	// process page call with timeout
	returnChan := make(chan interface{})
	errorChan := make(chan error)
	isTimedOut := false

	fc.pageCallCounterMutex.Lock()
	fc.pageCallCounter++
	callIdx := fc.pageCallCounter
	fc.pageCallCounterMutex.Unlock()

	go func(callIdx uint64) {
		defer func() {
			if err := recover(); err != nil {
				errorChan <- &FrontendCachePageError{
					name:  "page panic",
					err:   fmt.Errorf("page call %v panic: %v", callIdx, err),
					stack: string(debug.Stack()),
				}
			}
		}()

		// check cache
		if !utils.Config.Frontend.Debug && caching && fc.getFrontendCache(pageKey, pageData) == nil {
			logrus.Debugf("page served from cache: %v", pageKey)
			if !isTimedOut {
				returnChan <- pageData
			}
			return
		}

		// process page call
		pageData = buildFn(pageCall)

		if isTimedOut {
			return
		}
		if !utils.Config.Frontend.Debug && caching && pageCall.CacheTimeout >= 0 {
			fc.setFrontendCache(pageKey, pageData, pageCall.CacheTimeout)
		}
		if !isTimedOut {
			returnChan <- pageData
		}
	}(callIdx)

	callTimeout := utils.Config.Frontend.PageCallTimeout
	if callTimeout == 0 {
		callTimeout = 60 * time.Second
	}

	select {
	case returnValue := <-returnChan:
		return returnValue, nil
	case returnError := <-errorChan:
		return nil, returnError
	case <-time.After(callTimeout):
		isTimedOut = true
		return nil, &FrontendCachePageError{
			name:  "page timeout",
			err:   fmt.Errorf("page call %v timeout", callIdx),
			stack: fc.extractPageCallStack(callIdx),
		}
	}
}

func (fc *FrontendCacheService) getFrontendCache(pageKey string, returnValue interface{}) error {
	_, err := fc.tieredCache.Get(pageKey, returnValue)
	return err
}

func (fc *FrontendCacheService) setFrontendCache(pageKey string, value interface{}, timeout time.Duration) error {
	return fc.tieredCache.Set(pageKey, value, timeout)
}

func (fc *FrontendCacheService) completePageLoad(pageKey string, processingPage *FrontendCacheProcessingPage) {
	processingPage.modelMutex.Unlock()
	fc.processingMutex.Lock()
	delete(fc.processingDict, pageKey)
	fc.processingMutex.Unlock()
}

func (fc *FrontendCacheService) extractPageCallStack(callIdx uint64) string {
	if fc.callStackMutex.TryLock() {
		runtime.Stack(fc.callStackBuffer, true)
		fc.callStackMutex.Unlock()
	}
	fc.callStackMutex.RLock()
	defer fc.callStackMutex.RUnlock()

	callFnName := fmt.Sprintf("processPageCall.func1(0x%x)", callIdx)
	scanner := bufio.NewScanner(bytes.NewReader(fc.callStackBuffer))
	lastBlock := []string{}
	isPageCall := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "goroutine ") {
			if isPageCall {
				break
			}
			lastBlock = []string{}
		} else {
			lastBlock = append(lastBlock, line)

			if strings.Contains(line, callFnName) {
				isPageCall = true
			}
		}
	}
	if !isPageCall {
		return "call stack not found"
	}
	return strings.Join(lastBlock, "\n")
}
