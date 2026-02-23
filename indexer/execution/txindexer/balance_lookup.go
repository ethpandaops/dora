package txindexer

import (
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/lru"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
)

const (
	// maxBalanceLookupsPerBlock is the maximum number of balance lookups per block processing cycle.
	maxBalanceLookupsPerBlock = 100

	// nativeTokenID is the token ID used for native ETH balances.
	nativeTokenID = 0

	// balanceLookupCooldown is the minimum time between full RPC lookups for the same account.
	balanceLookupCooldown = 10 * time.Minute

	// lastLookupCacheSize is the size of the LRU cache for rate limiting.
	lastLookupCacheSize = 1000
)

// BalanceLookupPriority defines the priority level for balance lookups.
type BalanceLookupPriority uint8

const (
	// LowPriority is for background verification lookups.
	LowPriority BalanceLookupPriority = 0
	// HighPriority is for user-facing address page lookups.
	HighPriority BalanceLookupPriority = 1
)

// BalanceLookupRequest represents a request to look up a balance.
// AccountID can be 0 for addresses not yet in the database.
type BalanceLookupRequest struct {
	AccountID uint64         // 0 if address not in DB
	TokenID   uint64         // 0 for native ETH
	Address   common.Address // Required for RPC calls
	Contract  common.Address // Token contract (zero for native ETH)
	Decimals  uint8          // Token decimals
	Priority  BalanceLookupPriority
	Timestamp time.Time // When request was added
}

// pendingKey is used to deduplicate pending lookups.
// For known accounts (AccountID > 0), uses accountID + tokenID.
// For unknown accounts (AccountID = 0), uses address + tokenID.
type pendingKey struct {
	accountID uint64
	address   common.Address
	tokenID   uint64
}

// BalanceLookupService manages balance lookup requests with priority queuing.
type BalanceLookupService struct {
	logger  logrus.FieldLogger
	indexer *TxIndexer

	mu             sync.Mutex
	highPrioQueue  []*BalanceLookupRequest
	lowPrioQueue   []*BalanceLookupRequest
	pendingLookups map[pendingKey]*BalanceLookupRequest
	lastLookupTime *lru.Cache[common.Address, time.Time] // LRU cache for rate limiting by address
}

// NewBalanceLookupService creates a new balance lookup service.
func NewBalanceLookupService(logger logrus.FieldLogger, indexer *TxIndexer) *BalanceLookupService {
	return &BalanceLookupService{
		logger:         logger.WithField("component", "balance-lookup"),
		indexer:        indexer,
		highPrioQueue:  make([]*BalanceLookupRequest, 0, 64),
		lowPrioQueue:   make([]*BalanceLookupRequest, 0, 256),
		pendingLookups: make(map[pendingKey]*BalanceLookupRequest, 128),
		lastLookupTime: lru.NewCache[common.Address, time.Time](lastLookupCacheSize),
	}
}

// makePendingKey creates a dedup key for a request.
func makePendingKey(req *BalanceLookupRequest) pendingKey {
	return pendingKey{
		accountID: req.AccountID,
		address:   req.Address,
		tokenID:   req.TokenID,
	}
}

// QueueBalanceLookup adds a balance lookup request to the queue.
// High priority requests are processed before low priority ones.
// Duplicate requests are deduplicated, with priority upgraded if needed.
func (s *BalanceLookupService) QueueBalanceLookup(req *BalanceLookupRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := makePendingKey(req)

	// Check for duplicate - skip if already pending
	if existing, exists := s.pendingLookups[key]; exists {
		// Upgrade priority if needed
		if req.Priority > existing.Priority {
			s.upgradePriority(existing, req.Priority)
		}
		return
	}

	// Add to appropriate queue
	req.Timestamp = time.Now()
	s.pendingLookups[key] = req

	if req.Priority == HighPriority {
		s.highPrioQueue = append(s.highPrioQueue, req)
	} else {
		s.lowPrioQueue = append(s.lowPrioQueue, req)
	}
}

// QueueAddressBalanceLookups queues balance lookups for an address page view.
// accountID should be 0 if the address is not in the database.
// Rate limited to prevent excessive RPC calls.
func (s *BalanceLookupService) QueueAddressBalanceLookups(accountID uint64, address []byte) {
	addr := common.BytesToAddress(address)

	s.mu.Lock()
	// Check rate limit for this address
	if lastTime, ok := s.lastLookupTime.Get(addr); ok {
		if time.Since(lastTime) < balanceLookupCooldown {
			s.mu.Unlock()
			return
		}
	}

	// Update last lookup time
	s.lastLookupTime.Add(addr, time.Now())
	s.mu.Unlock()

	// Queue ETH balance lookup (high priority)
	s.QueueBalanceLookup(&BalanceLookupRequest{
		AccountID: accountID,
		TokenID:   nativeTokenID,
		Address:   addr,
		Contract:  common.Address{},
		Decimals:  18,
		Priority:  HighPriority,
	})

	// For known accounts, also queue token balance lookups
	if accountID > 0 {
		balances, _, err := db.GetElBalancesByAccountID(s.indexer.ctx, accountID, 0, 100)
		if err != nil {
			s.logger.WithError(err).Debug("failed to get balances for address page lookups")
			return
		}

		for _, balance := range balances {
			if balance.TokenID == nativeTokenID {
				continue
			}

			token, err := db.GetElTokenByID(s.indexer.ctx, balance.TokenID)
			if err != nil || token == nil {
				continue
			}

			s.QueueBalanceLookup(&BalanceLookupRequest{
				AccountID: accountID,
				TokenID:   balance.TokenID,
				Address:   addr,
				Contract:  common.BytesToAddress(token.Contract),
				Decimals:  token.Decimals,
				Priority:  HighPriority,
			})
		}
	}
}

// upgradePriority moves a request from low to high priority queue.
// Must be called with lock held.
func (s *BalanceLookupService) upgradePriority(req *BalanceLookupRequest, newPriority BalanceLookupPriority) {
	if req.Priority >= newPriority {
		return
	}

	// Remove from low priority queue
	for i, r := range s.lowPrioQueue {
		if r == req {
			s.lowPrioQueue = append(s.lowPrioQueue[:i], s.lowPrioQueue[i+1:]...)
			break
		}
	}

	// Add to high priority queue
	req.Priority = newPriority
	s.highPrioQueue = append(s.highPrioQueue, req)
}

// GetPendingLookups returns up to maxCount lookups to process.
// High priority requests are returned first.
func (s *BalanceLookupService) GetPendingLookups(maxCount int) []*BalanceLookupRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]*BalanceLookupRequest, 0, maxCount)

	// Take from high priority first
	for len(result) < maxCount && len(s.highPrioQueue) > 0 {
		req := s.highPrioQueue[0]
		s.highPrioQueue = s.highPrioQueue[1:]
		result = append(result, req)

		// Remove from pending map
		delete(s.pendingLookups, makePendingKey(req))
	}

	// Fill remainder from low priority
	for len(result) < maxCount && len(s.lowPrioQueue) > 0 {
		req := s.lowPrioQueue[0]
		s.lowPrioQueue = s.lowPrioQueue[1:]
		result = append(result, req)

		// Remove from pending map
		delete(s.pendingLookups, makePendingKey(req))
	}

	return result
}

// GetQueueStats returns current queue sizes.
func (s *BalanceLookupService) GetQueueStats() (highPrio, lowPrio int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.highPrioQueue), len(s.lowPrioQueue)
}

// HasPendingLookups returns true if there are any pending lookups in the queues.
func (s *BalanceLookupService) HasPendingLookups() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.highPrioQueue) > 0 || len(s.lowPrioQueue) > 0
}
