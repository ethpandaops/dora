package inclusionlists

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTargetAttemptsBlocked(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		attempts  targetAttempts
		listCount int
		blocked   bool
	}{
		{
			name:      "next attempt not due yet",
			attempts:  targetAttempts{count: 1, nextTry: now.Add(time.Second), listCount: 3},
			listCount: 3,
			blocked:   true,
		},
		{
			name:      "next attempt due",
			attempts:  targetAttempts{count: 1, nextTry: now.Add(-time.Second), listCount: 3},
			listCount: 3,
			blocked:   false,
		},
		{
			name:      "attempts used up",
			attempts:  targetAttempts{count: maxStateAttempts, nextTry: now.Add(-time.Minute), listCount: 3},
			listCount: 3,
			blocked:   true,
		},
		{
			name:      "new list after the attempts were used up",
			attempts:  targetAttempts{count: maxStateAttempts, nextTry: now.Add(-time.Minute), listCount: 3},
			listCount: 4,
			blocked:   false,
		},
		{
			name:      "new list before the next attempt is due",
			attempts:  targetAttempts{count: 1, nextTry: now.Add(time.Second), listCount: 3},
			listCount: 4,
			blocked:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.blocked, tt.attempts.blocked(tt.listCount, now))
		})
	}
}
