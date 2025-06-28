package snooper

import (
	"sync"

	"github.com/ethereum/go-ethereum/common"
)

// BlockHashRingBuffer implements an efficient ring buffer for storing block hashes
type BlockHashRingBuffer struct {
	buffer   []common.Hash
	head     int
	size     int
	capacity int
	mu       sync.RWMutex
}

// NewBlockHashRingBuffer creates a new ring buffer with the specified capacity
func NewBlockHashRingBuffer(capacity int) *BlockHashRingBuffer {
	return &BlockHashRingBuffer{
		buffer:   make([]common.Hash, capacity),
		capacity: capacity,
	}
}

// Add adds a block hash to the ring buffer
func (rb *BlockHashRingBuffer) Add(hash common.Hash) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.buffer[rb.head] = hash
	rb.head = (rb.head + 1) % rb.capacity

	if rb.size < rb.capacity {
		rb.size++
	}
}

// Contains checks if a block hash exists in the ring buffer
func (rb *BlockHashRingBuffer) Contains(hash common.Hash) bool {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	for i := 0; i < rb.size; i++ {
		if rb.buffer[i] == hash {
			return true
		}
	}
	return false
}

// Size returns the current number of elements in the buffer
func (rb *BlockHashRingBuffer) Size() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.size
}

// Clear removes all elements from the buffer
func (rb *BlockHashRingBuffer) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.head = 0
	rb.size = 0
	for i := range rb.buffer {
		rb.buffer[i] = common.Hash{}
	}
}
