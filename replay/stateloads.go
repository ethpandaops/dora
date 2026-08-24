package replay

import "sync"

// stateLoads counts the beacon states the explorer is currently pulling through the
// proxy. A full state is tens of megabytes and takes seconds to fetch, decompress and
// decode, during which the explorer cannot process anything else — so the replay uses
// this to hold its clock rather than running ahead of what the explorer has seen.
type stateLoads struct {
	mutex  sync.Mutex
	active int
	total  uint64

	// idle is closed while nothing is loading, and replaced with an open channel as
	// soon as a load starts, so a waiter can select on it.
	idle chan struct{}
}

func newStateLoads() *stateLoads {
	loads := &stateLoads{idle: make(chan struct{})}
	close(loads.idle)

	return loads
}

// begin records the start of a state load and returns the function that ends it.
func (s *stateLoads) begin() func() {
	s.mutex.Lock()

	if s.active == 0 {
		s.idle = make(chan struct{})
	}

	s.active++
	s.total++
	s.mutex.Unlock()

	ended := false

	return func() {
		if ended {
			return
		}

		ended = true

		s.mutex.Lock()
		defer s.mutex.Unlock()

		s.active--
		if s.active == 0 {
			close(s.idle)
		}
	}
}

// idleChan returns a channel that is closed once no state is being loaded. It is
// already closed when nothing is loading right now.
func (s *stateLoads) idleChan() <-chan struct{} {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return s.idle
}

func (s *stateLoads) count() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return s.active
}
