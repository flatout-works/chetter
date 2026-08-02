package service

import "sync"

// claimNotifier broadcasts "a task may now be claimable" to in-flight
// ClaimTask long-polls so they re-check the queue immediately instead of
// sleeping out the safety-net poll interval.
//
// Chetter runs the server as a single replica (see AGENTS.md), and every
// path that makes work claimable (MCP submit, webhook trigger, schedule,
// reaper requeue, session resume/rerun) executes inside this process, so the
// notifier covers the common case completely: while the queue is idle the
// database is only touched by one slow safety-net poll per runner instead of
// continuous polling. Submissions from other replicas or direct database
// writes bypass the notifier and are picked up by the safety-net poll.
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
