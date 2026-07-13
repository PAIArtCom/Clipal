package oauth

import "sync"

// storeTransferMu serializes ordinary credential store operations with data
// transfer transactions that snapshot, clear, and restore the on-disk oauth
// directory as a whole. Ordinary Store methods hold the shared side, so they
// stay as concurrent with each other as before; a data transfer holds the
// exclusive side for its full apply-or-rollback window so that concurrent
// token refreshes cannot land between its snapshot and restore (which would
// resurrect deleted credentials or roll back a rotated refresh token to a
// value the upstream has already invalidated).
var storeTransferMu sync.RWMutex

// LockStoreForTransfer grants the caller exclusive ownership of the on-disk
// credential store across every Store instance in this process. All other
// Store operations block until the returned release function is called.
// Stores created with NewTransferStore bypass the lock and must only be used
// while it is held.
func LockStoreForTransfer() (release func()) {
	storeTransferMu.Lock()
	var once sync.Once
	return func() { once.Do(storeTransferMu.Unlock) }
}

// lockStoreForRefresh joins an OAuth refresh to the transfer barrier for its
// complete Load -> upstream Refresh -> Save lifecycle. The caller must use a
// Store with the per-method lock bypassed while this lock is held; otherwise a
// pending transfer writer could block a nested RLock and deadlock the refresh.
func lockStoreForRefresh() (release func()) {
	storeTransferMu.RLock()
	var once sync.Once
	return func() { once.Do(storeTransferMu.RUnlock) }
}

// NewTransferStore returns a Store for use inside a LockStoreForTransfer
// session. Its operations skip the shared lock; using it without holding the
// transfer lock forfeits the serialization guarantee.
func NewTransferStore(configDir string) *Store {
	return NewStore(configDir).withTransferLockBypass()
}

func (s *Store) lockShared() (release func()) {
	if s != nil && s.bypassTransferLock {
		return func() {}
	}
	storeTransferMu.RLock()
	return storeTransferMu.RUnlock
}

func (s *Store) withTransferLockBypass() *Store {
	if s == nil {
		return nil
	}
	clone := *s
	clone.bypassTransferLock = true
	return &clone
}
