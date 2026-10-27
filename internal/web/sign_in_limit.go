package web

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	// signInWindow is how long attempts count against a client.
	signInWindow = 10 * time.Minute
	// signInPerClient is the attempts one client address may make per window;
	// an owner mistyping the token a few times stays well below it.
	signInPerClient = 10
	// signInGlobal is the attempts all clients together may make per window,
	// so many addresses (or a proxy that hides them) cannot guess at speed.
	signInGlobal = 100
)

// signInLimiter counts token checks (sign-in and pairing) per client address
// and in total, in fixed windows. A blocked request is not checked at all.
// Entries are created only while the global window has room, and expired
// ones are swept, so the map stays bounded.
type signInLimiter struct {
	mu      sync.Mutex
	now     func() time.Time
	clients map[string]*signInCount
	global  signInCount
}

type signInCount struct {
	start time.Time
	n     int
}

func newSignInLimiter() *signInLimiter {
	return &signInLimiter{now: time.Now, clients: map[string]*signInCount{}}
}

// wait returns how long until the window has room, or 0 when it has.
func (c *signInCount) wait(now time.Time, limit int) time.Duration {
	if now.Sub(c.start) >= signInWindow {
		c.start, c.n = now, 0
	}
	if c.n < limit {
		return 0
	}
	return c.start.Add(signInWindow).Sub(now)
}

// signInClient keys a request by its peer address; the server reads no
// forwarding headers, so behind a proxy every client shares one key.
func signInClient(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// admit records an attempt by r's client and returns 0, or returns how long
// to wait when the client or all clients are over the limit.
func (l *signInLimiter) admit(r *http.Request) time.Duration {
	key := signInClient(r)
	l.mu.Lock()
	now := l.now()
	wait := l.global.wait(now, signInGlobal)
	client := l.clients[key]
	if wait == 0 && client == nil {
		if len(l.clients) >= signInGlobal {
			for k, c := range l.clients {
				if now.Sub(c.start) >= signInWindow {
					delete(l.clients, k)
				}
			}
		}
		client = &signInCount{start: now}
		l.clients[key] = client
	}
	if wait == 0 {
		wait = client.wait(now, signInPerClient)
	}
	if wait == 0 {
		l.global.n++
		client.n++
	}
	l.mu.Unlock()
	return wait
}

// setRetryAfter sets Retry-After to wait in whole seconds, rounded up.
func setRetryAfter(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
}

// succeeded clears r's client after a correct token.
func (l *signInLimiter) succeeded(r *http.Request) {
	l.mu.Lock()
	delete(l.clients, signInClient(r))
	l.mu.Unlock()
}
