// Package webhook handles GitHub webhook events for the Devfleet service.
package webhook

import (
	"sync"
	"time"
)

// RecentDeliveries tracks recent X-GitHub-Delivery IDs to prevent duplicate
// processing of the same webhook delivery. Not persisted — if the process
// restarts mid-review, the review is simply lost (acceptable; GitHub will
// not redeliver on 2xx).
type RecentDeliveries struct {
	mu       sync.Mutex
	entries  map[string]time.Time
	ttl      time.Duration
	maxSize  int
}

// NewRecentDeliveries creates a tracker that holds IDs for ttl and caps the
// map at maxSize entries (evicting oldest by expiry).
func NewRecentDeliveries(ttl time.Duration, maxSize int) *RecentDeliveries {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if maxSize <= 0 {
		maxSize = 1024
	}
	return &RecentDeliveries{
		entries: make(map[string]time.Time),
		ttl:     ttl,
		maxSize: maxSize,
	}
}

// Seen returns true if the delivery ID was already recorded (and not expired).
// As a side effect, it records the ID with the current time.
func (r *RecentDeliveries) Seen(id string) bool {
	if id == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.evictExpired(now)
	if prev, ok := r.entries[id]; ok && now.Sub(prev) < r.ttl {
		return true
	}
	r.entries[id] = now
	if len(r.entries) > r.maxSize {
		r.evictExpired(now)
	}
	return false
}

func (r *RecentDeliveries) evictExpired(now time.Time) {
	for id, t := range r.entries {
		if now.Sub(t) >= r.ttl {
			delete(r.entries, id)
		}
	}
}

// Size returns the current number of tracked IDs (for tests/debugging).
func (r *RecentDeliveries) Size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}
