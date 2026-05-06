package upload

import "sync"

var defaultChunkLocks = NewProcessLocalChunkLockManager()

type ProcessLocalChunkLockManager struct {
	mu     sync.Mutex
	active map[string]struct{}
}

func NewProcessLocalChunkLockManager() *ProcessLocalChunkLockManager {
	return &ProcessLocalChunkLockManager{active: map[string]struct{}{}}
}

func (m *ProcessLocalChunkLockManager) TryLock(uploadID string) (func(), bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.active[uploadID]; exists {
		return func() {}, false
	}
	m.active[uploadID] = struct{}{}

	var once sync.Once
	release := func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			delete(m.active, uploadID)
		})
	}
	return release, true
}
