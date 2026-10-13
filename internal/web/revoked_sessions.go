package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"
)

// signedOutFileName is the signed-out session file, next to sessions.json.
const signedOutFileName = "web-signed-out.json"

// revokedSessions is the set of session cookies that were signed out, by
// their MAC, each kept until the cookie would have expired on its own. A
// cookie is stateless otherwise, so without this set a copy of it would keep
// working after Log out. The file keeps the set across restarts.
type revokedSessions struct {
	path  string
	mu    sync.Mutex
	until map[string]int64 // cookie MAC -> expiry (Unix seconds)
}

// openRevokedSessions reads the set at path, dropping entries whose cookie
// has expired. A missing file is an empty set; an unreadable one is an error
// that names it.
func openRevokedSessions(path string) (*revokedSessions, error) {
	r := &revokedSessions{path: path, until: map[string]int64{}}
	data, err := os.ReadFile(path) // #nosec G304 -- UAM's own file next to its config.
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &r.until)
	}
	if err != nil {
		return nil, fmt.Errorf("signed-out sessions %s: %w (delete the file to start over)", path, err)
	}
	r.pruneLocked(time.Now().Unix())
	return r, nil
}

// revoke records the cookie with MAC mac as signed out until exp and saves
// the set, without the entries already expired at now.
func (r *revokedSessions) revoke(mac string, exp, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(now)
	r.until[mac] = exp
	data, err := json.Marshal(r.until)
	if err != nil {
		return err
	}
	return writeFileAtomic(r.path, signedOutFileName+".tmp.*", data)
}

// revoked reports whether the cookie with MAC mac was signed out. Expiry is
// the caller's check: an expired cookie never authenticates anyway.
func (r *revokedSessions) revoked(mac string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.until[mac]
	return ok
}

func (r *revokedSessions) pruneLocked(now int64) {
	for mac, exp := range r.until {
		if exp <= now {
			delete(r.until, mac)
		}
	}
}
