package extra

import (
	"sync"
	"time"

	"github.com/dmundt/go-cask/cas"
)

// CacheSnapshot is the periodic observation a CacheMonitor emits: the
// cache's hit/miss/load/evict counters, hit rate, and current size.
type CacheSnapshot = cas.CacheStats

// CacheMonitor[T] observes a CachedStore[T] and emits CacheSnapshot values
// through onSnapshot on a fixed interval, starting at construction, until
// Stop is called. It is used for observability (logging, metrics) of cache
// behavior.
type CacheMonitor[T any] struct {
	store      *cas.CachedStore[T]
	interval   time.Duration
	onSnapshot func(CacheSnapshot)

	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// NewCacheMonitor starts monitoring store: every interval, onSnapshot is
// called with the current cache stats. Call Stop to end monitoring (it
// blocks until the monitor goroutine has exited).
func NewCacheMonitor[T any](store *cas.CachedStore[T], interval time.Duration, onSnapshot func(CacheSnapshot)) *CacheMonitor[T] {
	m := &CacheMonitor[T]{
		store:      store,
		interval:   interval,
		onSnapshot: onSnapshot,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	go m.run()
	return m
}

// Stop ends monitoring and waits for the monitor goroutine to exit. Calling
// Stop more than once is safe (subsequent calls return immediately).
func (m *CacheMonitor[T]) Stop() {
	m.once.Do(func() {
		close(m.stop)
		<-m.done
	})
}

func (m *CacheMonitor[T]) run() {
	defer close(m.done)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.onSnapshot(m.store.CacheStats())
		case <-m.stop:
			return
		}
	}
}
