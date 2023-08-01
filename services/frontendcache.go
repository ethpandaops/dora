package services

import (
	"fmt"
	"sync"
	"time"

	"github.com/pk910/light-beaconchain-explorer/cache"
	"github.com/pk910/light-beaconchain-explorer/utils"
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
	CacheTimeout time.Duration
}

var GlobalFrontendCache *FrontendCacheService

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

func (fc *FrontendCacheService) GetFrontendCache(pageKey string, returnValue interface{}) error {
	_, err := fc.tieredCache.Get(pageKey, returnValue)
	return err
}

func (fc *FrontendCacheService) SetFrontendCache(pageKey string, value interface{}, timeout time.Duration) error {
	return fc.tieredCache.Set(pageKey, value, timeout)
}

func (fc *FrontendCacheService) ProcessCachedPage(pageKey string, caching bool, returnValue interface{}, buildFn func(pageCall *FrontendCacheProcessingPage) interface{}) interface{} {
	fc.processingMutex.Lock()
	processingPage := fc.processingDict[pageKey]
	if processingPage != nil {
		fc.processingMutex.Unlock()
		logrus.Printf("page already processing: %v", pageKey)

		processingPage.modelMutex.RLock()
		defer processingPage.modelMutex.RUnlock()
		return processingPage.pageModel
	}

	processingPage = &FrontendCacheProcessingPage{
		CacheTimeout: -1,
	}
	fc.processingDict[pageKey] = processingPage
	processingPage.modelMutex.Lock()
	defer fc.completePageLoad(pageKey, processingPage)
	fc.processingMutex.Unlock()

	returnValue = fc.processCachedPageData(pageKey, caching, returnValue, buildFn, processingPage)
	processingPage.pageModel = returnValue
	return returnValue
}

func (fc *FrontendCacheService) completePageLoad(pageKey string, processingPage *FrontendCacheProcessingPage) {
	processingPage.modelMutex.Unlock()
	fc.processingMutex.Lock()
	delete(fc.processingDict, pageKey)
	fc.processingMutex.Unlock()
}

func (fc *FrontendCacheService) processCachedPageData(pageKey string, caching bool, pageData interface{}, buildFn func(pageCall *FrontendCacheProcessingPage) interface{}, pageCall *FrontendCacheProcessingPage) interface{} {
	// check cache
	if !utils.Config.Frontend.Debug && caching && fc.GetFrontendCache(pageKey, pageData) == nil {
		logrus.Printf("page served from cache: %v", pageKey)
		return pageData
	}

	pageData = buildFn(pageCall)

	if !utils.Config.Frontend.Debug && caching && pageCall.CacheTimeout >= 0 {
		fc.SetFrontendCache(pageKey, pageData, pageCall.CacheTimeout)
	}
	return pageData
}
