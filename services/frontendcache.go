package services

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pk910/dora-the-explorer/cache"
	"github.com/pk910/dora-the-explorer/utils"
	"github.com/sirupsen/logrus"
)

type FrontendCacheService struct {
	tieredCache *cache.TieredCache

	processingMutex sync.Mutex
	processingDict  map[string]*FrontendCacheProcessingPage
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
var FrontendCacheTimeoutError = errors.New("page processing timeout")

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
		tieredCache:    tieredCache,
		processingDict: make(map[string]*FrontendCacheProcessingPage),
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
	isTimedOut := false
	go func() {
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
	}()

	callTimeout := utils.Config.Frontend.PageCallTimeout
	if callTimeout == 0 {
		callTimeout = 60 * time.Second
	}

	select {
	case returnValue := <-returnChan:
		return returnValue, nil
	case <-time.After(callTimeout):
		isTimedOut = true
		return nil, FrontendCacheTimeoutError
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
