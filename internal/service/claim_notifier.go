package service

import "sync"

// claimNotifier broadcasts "a task may now be claimable" to in-flight
// ClaimTask long-polls so they re-check the queue immediately instead of
// sleeping out the safety-net poll interval.
//
// This provides zero-latency wake-up for same-replica submissions. Cross-
// replica notifications are handled by the claim_notify_counter DB row
// (bumped by notifyTaskClaimable on every submission): one process-wide
// poller (pollClaimNotifications) watches it about once per second and
// triggers this broadcast on change. The safety-net poll
// (claimPollInterval) catches work that bypassed both mechanisms.
type claimNotifier struct {
	mu sync.Mutex
	ch chan struct{}
}

func newClaimNotifier() *claimNotifier {
	return &claimNotifier{ch: make(chan struct{})}
}

// notify wakes all current waiters and installs a fresh channel for future
// waiters. Combined with the snapshot-before-poll ordering in ClaimTask, a
// submission can never be missed: a poller that snapshots before the notify
// is woken by the close; a poller that snapshots after the notify runs its
// poll after the submission committed, so it observes the claimable row.
func (n *claimNotifier) notify() {
	n.mu.Lock()
	defer n.mu.Unlock()
	close(n.ch)
	n.ch = make(chan struct{})
}

// waitCh returns the current notification channel. Callers must snapshot it
// BEFORE checking the queue, then wait on it after the check comes up empty.
func (n *claimNotifier) waitCh() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ch
}
