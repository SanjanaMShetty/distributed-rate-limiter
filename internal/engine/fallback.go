package engine

import (
	"container/list"
	"math"
	"sync"
	"time"
)

// localFallback is intentionally process-local. It bounds memory and limits
// each replica independently during Redis outages; it does not preserve the
// normal shared-quota guarantee.
type localFallback struct {
	mu       sync.Mutex
	capacity int
	idleTTL  time.Duration
	entries  map[string]*list.Element
	lru      *list.List
}

type localEntry struct {
	key       string
	tokens    float64
	updatedAt time.Time
	lastSeen  time.Time
}

type localResult struct {
	allowed    bool
	remaining  int
	retryAfter time.Duration
	resetAfter time.Duration
}

func newLocalFallback(maxEntries int, idleTTL time.Duration) *localFallback {
	return &localFallback{capacity: maxEntries, idleTTL: idleTTL, entries: make(map[string]*list.Element), lru: list.New()}
}

func (f *localFallback) check(key string, bucketCapacity, refillPerSec float64) localResult {
	now := time.Now()
	f.mu.Lock()
	defer f.mu.Unlock()

	for element := f.lru.Back(); element != nil; {
		previous := element.Prev()
		entry := element.Value.(*localEntry)
		if now.Sub(entry.lastSeen) <= f.idleTTL {
			break
		}
		delete(f.entries, entry.key)
		f.lru.Remove(element)
		element = previous
	}

	element := f.entries[key]
	var entry *localEntry
	if element == nil {
		if f.lru.Len() >= f.capacity {
			oldest := f.lru.Back()
			delete(f.entries, oldest.Value.(*localEntry).key)
			f.lru.Remove(oldest)
		}
		entry = &localEntry{key: key, tokens: bucketCapacity, updatedAt: now, lastSeen: now}
		element = f.lru.PushFront(entry)
		f.entries[key] = element
	} else {
		entry = element.Value.(*localEntry)
		f.lru.MoveToFront(element)
		elapsed := now.Sub(entry.updatedAt).Seconds()
		entry.tokens = math.Min(bucketCapacity, entry.tokens+elapsed*refillPerSec)
		entry.updatedAt = now
		entry.lastSeen = now
	}

	resetAfter := time.Duration(math.Ceil((bucketCapacity - entry.tokens) / refillPerSec * float64(time.Second)))
	if resetAfter < 0 {
		resetAfter = 0
	}
	if entry.tokens >= 1 {
		entry.tokens--
		resetAfter = time.Duration(math.Ceil((bucketCapacity - entry.tokens) / refillPerSec * float64(time.Second)))
		return localResult{allowed: true, remaining: int(math.Floor(entry.tokens)), resetAfter: resetAfter}
	}
	wait := time.Duration(math.Ceil((1 - entry.tokens) / refillPerSec * float64(time.Second)))
	if wait < time.Millisecond {
		wait = time.Millisecond
	}
	return localResult{remaining: 0, retryAfter: wait, resetAfter: resetAfter}
}
