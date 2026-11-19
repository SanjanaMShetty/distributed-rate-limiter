package engine

import (
	"sync"
	"time"
)

// circuitBreaker permits one half-open probe after the recovery delay.
type circuitBreaker struct {
	mu          sync.Mutex
	threshold   int
	openFor     time.Duration
	failures    int
	openUntil   time.Time
	probeActive bool
}

func newCircuitBreaker(threshold int, openFor time.Duration) *circuitBreaker {
	return &circuitBreaker{threshold: threshold, openFor: openFor}
}

func (b *circuitBreaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return true
	}
	if time.Now().Before(b.openUntil) {
		return false
	}
	if b.probeActive {
		return false
	}
	b.probeActive = true
	return true
}

func (b *circuitBreaker) success() {
	b.mu.Lock()
	b.failures = 0
	b.openUntil = time.Time{}
	b.probeActive = false
	b.mu.Unlock()
}

func (b *circuitBreaker) failure() {
	b.mu.Lock()
	b.failures++
	b.probeActive = false
	if b.failures >= b.threshold {
		b.openUntil = time.Now().Add(b.openFor)
	}
	b.mu.Unlock()
}

func (b *circuitBreaker) abandonProbe() {
	b.mu.Lock()
	b.probeActive = false
	b.mu.Unlock()
}
