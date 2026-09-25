package web

import (
	"strconv"
	"sync"
	"time"
)

// defaultExpensiveCooldown is how often a session earns another expensive
// operation. Both of them are O(store) work on the request path — verify-all
// re-reads and re-hashes every object, a metadata rebuild re-reads a header per
// object — so the budget caps how often one operator or script holding a
// session cookie can ask for another sweep.
const defaultExpensiveCooldown = 5 * time.Second

// expensiveBurst is how many expensive operations a session may start before
// the cooldown applies. It leaves room for an operator's retry — a sweep whose
// response was lost, a second look — while still bounding a script: beyond the
// burst, one operation per cooldown.
const expensiveBurst = 3

// busyRetryAfter is what a refused request is told to wait when another session
// already holds the single expensive-operation slot. It is a floor, not a
// promise about the running sweep: the limiter does not estimate work it did
// not start.
const busyRetryAfter = time.Second

// expensiveOps bounds the authenticated routes that do unbounded work per
// request. The bound has two halves:
//
//   - at most one expensive operation runs at a time, so a burst of requests
//     cannot multiply CPU and disk work; and
//   - one session holds a token bucket — expensiveBurst operations, then one
//     more per cooldown — so a single session cannot monopolize the viewer by
//     refreshing.
//
// A refused request is answered immediately and never queued: verify-all
// answers 429 with Retry-After, and a metadata rebuild serves the published
// snapshot (stale, not wrong) when one exists (viewer-design §3).
//
// A cooldown of zero admits every request. That is a test's way of keeping the
// limiter out of the way when its subject is something else (the login
// throttle's tests construct their own window the same way).
type expensiveOps struct {
	mu       sync.Mutex
	cooldown time.Duration
	burst    int
	now      func() time.Time
	busy     bool
	budgets  map[string]*budget // session id → token bucket
}

// budget is one session's token bucket: the tokens left and when the bucket was
// last refilled, so a refill is computed from elapsed time rather than a timer.
type budget struct {
	tokens   int
	refilled time.Time
}

// newExpensiveOps returns the limiter the viewer runs with.
func newExpensiveOps() *expensiveOps {
	return newExpensiveOpsWith(defaultExpensiveCooldown, time.Now)
}

// newExpensiveOpsWith takes the cooldown and clock explicitly, so a test drives
// the refusal without sleeping.
func newExpensiveOpsWith(cooldown time.Duration, now func() time.Time) *expensiveOps {
	return &expensiveOps{
		cooldown: cooldown,
		burst:    expensiveBurst,
		now:      now,
		budgets:  make(map[string]*budget),
	}
}

// begin asks for a token on behalf of session id. It reports whether the
// operation may run and, when it may not, how long the caller should wait: the
// time until the session's next token, or busyRetryAfter when another session
// holds the slot (a busy refusal never spends a token). An admitted begin MUST
// be paired with end.
func (l *expensiveOps) begin(id string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.cooldown > 0 {
		l.sweepLocked(now)
		b := l.budgetLocked(id, now)
		if b.tokens == 0 {
			return l.refillIn(b, now), false
		}
		if l.busy {
			return busyRetryAfter, false
		}
		b.tokens--
		l.busy = true
		return 0, true
	}
	if l.busy {
		return busyRetryAfter, false
	}
	l.busy = true
	return 0, true
}

// end releases the slot.
func (l *expensiveOps) end() {
	l.mu.Lock()
	l.busy = false
	l.mu.Unlock()
}

// budgetLocked returns the session's bucket, refilled for the time that passed
// since the last call.
func (l *expensiveOps) budgetLocked(id string, now time.Time) *budget {
	b, ok := l.budgets[id]
	if !ok {
		b = &budget{tokens: l.burst, refilled: now}
		l.budgets[id] = b
		return b
	}
	if gained := int(now.Sub(b.refilled) / l.cooldown); gained > 0 {
		b.tokens = min(l.burst, b.tokens+gained)
		// Advance by whole cooldowns only, so the remainder is not lost.
		b.refilled = b.refilled.Add(time.Duration(gained) * l.cooldown)
	}
	return b
}

// refillIn reports how long the session waits for its next token.
func (l *expensiveOps) refillIn(b *budget, now time.Time) time.Duration {
	next := b.refilled.Add(l.cooldown)
	if !next.After(now) {
		return l.cooldown
	}
	return next.Sub(now)
}

// sweepLocked drops idle sessions once the map has grown past sweepThreshold
// (the same threshold the login throttle uses), so a viewer serving many
// sessions cannot accumulate one bucket per session forever.
func (l *expensiveOps) sweepLocked(now time.Time) {
	if len(l.budgets) <= sweepThreshold {
		return
	}
	for id, b := range l.budgets {
		if b.tokens >= l.burst && now.Sub(b.refilled) >= l.cooldown {
			delete(l.budgets, id)
		}
	}
}

// retryAfterSeconds renders d for an HTTP Retry-After header: whole seconds,
// rounded up, never zero (api-design §5 — the rule the login throttle's
// refusal follows too).
func retryAfterSeconds(d time.Duration) int {
	seconds := int((d + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return seconds
}

// retryAfterHeader is retryAfterSeconds as the header value.
func retryAfterHeader(d time.Duration) string {
	return strconv.Itoa(retryAfterSeconds(d))
}
