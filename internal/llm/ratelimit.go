package llm

import (
	"context"
	"strings"
	"sync"
	"time"
)

type InMemoryRateLimiter struct {
	mu         sync.Mutex
	now        func() time.Time
	sleep      func(context.Context, time.Duration) error
	limits     map[string]RateLimitConfig
	states     map[string]rateLimitState
	defaultCfg RateLimitConfig
}

type RateLimitConfig struct {
	Requests int
	Window   time.Duration
}

type rateLimitState struct {
	windowStart time.Time
	count       int
}

func NewInMemoryRateLimiter(defaultCfg RateLimitConfig, now func() time.Time, sleep func(context.Context, time.Duration) error) *InMemoryRateLimiter {
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = sleepContext
	}
	return &InMemoryRateLimiter{now: now, sleep: sleep, limits: make(map[string]RateLimitConfig), states: make(map[string]rateLimitState), defaultCfg: defaultCfg}
}

func (l *InMemoryRateLimiter) SetLimit(scope string, cfg RateLimitConfig) {
	if l == nil {
		return
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "global"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limits[scope] = cfg
}

func (l *InMemoryRateLimiter) Wait(ctx context.Context, scope string) error {
	if l == nil {
		return nil
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "global"
	}
	for {
		now := l.now()
		waitFor := time.Duration(0)

		l.mu.Lock()
		cfg := l.defaultCfg
		if scoped, ok := l.limits[scope]; ok {
			cfg = scoped
		}
		if cfg.Requests <= 0 || cfg.Window <= 0 {
			l.mu.Unlock()
			return nil
		}
		state := l.states[scope]
		if state.windowStart.IsZero() || now.Sub(state.windowStart) >= cfg.Window {
			state = rateLimitState{windowStart: now, count: 0}
		}
		if state.count < cfg.Requests {
			state.count++
			l.states[scope] = state
			l.mu.Unlock()
			return nil
		}
		waitFor = state.windowStart.Add(cfg.Window).Sub(now)
		l.mu.Unlock()

		if waitFor <= 0 {
			continue
		}
		if err := l.sleep(ctx, waitFor); err != nil {
			return err
		}
	}
}
