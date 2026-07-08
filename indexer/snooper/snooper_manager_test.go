package snooper

import (
	"sync"
	"testing"
)

// TestGetClientInfoConcurrentWithWrites exercises the client lookup on the
// event-listener path while clients are added and removed. The lookup and the
// mutations touch the same map, so without synchronization this is a concurrent
// map access that crashes the process; run with -race to catch a regression
// deterministically.
func TestGetClientInfoConcurrentWithWrites(t *testing.T) {
	sm := &SnooperManager{clients: make(map[uint16]*snooperClientInfo)}

	done := make(chan struct{})
	var wg sync.WaitGroup

	// Reader: mirrors HandleExecutionTimeEvent looking up the client per event.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				sm.getClientInfo(1)
			}
		}
	}()

	// Writer: mirrors AddClient/RemoveClient mutating the map under the lock.
	for i := 0; i < 100000; i++ {
		id := uint16(i)
		sm.mu.Lock()
		sm.clients[id] = &snooperClientInfo{clientID: id}
		delete(sm.clients, id)
		sm.mu.Unlock()
	}

	close(done)
	wg.Wait()
}
