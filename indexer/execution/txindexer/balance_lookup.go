package txindexer

import (
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
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
type BalanceLookupRequest struct {
	AccountID uint64
	TokenID   uint64
	Address   common.Address // Cached for RPC calls
	Contract  common.Address // Token contract (zero for native ETH)
	Decimals  uint8          // Token decimals
	Priority  BalanceLookupPriority
	Timestamp time.Time // When request was added
}

// BalanceLookupService manages balance lookup requests with priority queuing.
type BalanceLookupService struct {
	logger  logrus.FieldLogger
	indexer *TxIndexer

	mu             sync.Mutex
	highPrioQueue  []*BalanceLookupRequest
	lowPrioQueue   []*BalanceLookupRequest
	pendingLookups map[uint64]map[uint64]*BalanceLookupRequest // [accountID][tokenID]
	lastLookupTime map[uint64]time.Time                        // Rate limit: accountID -> last full lookup time
}

// NewBalanceLookupService creates a new balance lookup service.
func NewBalanceLookupService(logger logrus.FieldLogger, indexer *TxIndexer) *BalanceLookupService {
	return &BalanceLookupService{
		logger:         logger.WithField("component", "balance-lookup"),
		indexer:        indexer,
		highPrioQueue:  make([]*BalanceLookupRequest, 0, 64),
		lowPrioQueue:   make([]*BalanceLookupRequest, 0, 256),
		pendingLookups: make(map[uint64]map[uint64]*BalanceLookupRequest, 128),
		lastLookupTime: make(map[uint64]time.Time, 128),
	}
}

// QueueBalanceLookup adds a balance lookup request to the queue.
// High priority requests are processed before low priority ones.
// Duplicate requests for the same account/token pair are deduplicated,
// with priority being upgraded if the new request has higher priority.
func (s *BalanceLookupService) QueueBalanceLookup(req *BalanceLookupRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check for duplicate - skip if already pending
	if accountLookups, exists := s.pendingLookups[req.AccountID]; exists {
		if existing, exists := accountLookups[req.TokenID]; exists {
			// Upgrade priority if needed
			if req.Priority > existing.Priority {
				s.upgradePriority(existing, req.Priority)
			}
			return
		}
	} else {
		s.pendingLookups[req.AccountID] = make(map[uint64]*BalanceLookupRequest, 8)
	}

	// Add to appropriate queue
	req.Timestamp = time.Now()
	s.pendingLookups[req.AccountID][req.TokenID] = req

	if req.Priority == HighPriority {
		s.highPrioQueue = append(s.highPrioQueue, req)
	} else {
		s.lowPrioQueue = append(s.lowPrioQueue, req)
	}
}

// QueueAddressPageLookups queues all balance lookups for an address page view.
// This includes ETH + all known token balances for the account.
// Rate limited to prevent excessive RPC calls - skips if last lookup was within cooldown period.
func (s *BalanceLookupService) QueueAddressPageLookups(accountID uint64, address []byte) {
	s.mu.Lock()

	// Check rate limit for this account
	if lastTime, exists := s.lastLookupTime[accountID]; exists {
		if time.Since(lastTime) < balanceLookupCooldown {
			s.mu.Unlock()
			return
		}
	}

	// Update last lookup time
	s.lastLookupTime[accountID] = time.Now()
	s.mu.Unlock()

	addr := common.BytesToAddress(address)

	// Queue ETH balance lookup (high priority)
	s.QueueBalanceLookup(&BalanceLookupRequest{
		AccountID: accountID,
		TokenID:   nativeTokenID,
		Address:   addr,
		Contract:  common.Address{},
		Decimals:  18,
		Priority:  HighPriority,
	})

	// Get all known token balances for this account and queue them
	balances, _, err := db.GetElBalancesByAccountID(accountID, 0, 100)
	if err != nil {
		s.logger.WithError(err).Debug("failed to get balances for address page lookups")
		return
	}

	for _, balance := range balances {
		if balance.TokenID == nativeTokenID {
			continue
		}

		token, err := db.GetElTokenByID(balance.TokenID)
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
		if accountLookups, exists := s.pendingLookups[req.AccountID]; exists {
			delete(accountLookups, req.TokenID)
			if len(accountLookups) == 0 {
				delete(s.pendingLookups, req.AccountID)
			}
		}
	}

	// Fill remainder from low priority
	for len(result) < maxCount && len(s.lowPrioQueue) > 0 {
		req := s.lowPrioQueue[0]
		s.lowPrioQueue = s.lowPrioQueue[1:]
		result = append(result, req)

		// Remove from pending map
		if accountLookups, exists := s.pendingLookups[req.AccountID]; exists {
			delete(accountLookups, req.TokenID)
			if len(accountLookups) == 0 {
				delete(s.pendingLookups, req.AccountID)
			}
		}
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
