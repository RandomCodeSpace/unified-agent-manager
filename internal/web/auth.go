package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

const (
	tokenFileName = "web-token"
	tokenBytes    = 32
	cookieName    = "uam_web"
	// signingKeyFileName holds the random secret cookies and file keys are
	// signed with (signingKey), next to the token and created the same way.
	signingKeyFileName = "web-signing-key"
	// fileCookieName is the companion of file keys over HTTPS (fileCookie).
	fileCookieName = "uam_file"
	cookieMaxAge   = 30 * 24 * 60 * 60
	// fileKeyTTL bounds how long a file key (fileKey) opens a Task's files.
	fileKeyTTL = 12 * time.Hour

	// An owner-set token (uam web token set) must fall within these bounds.
	minTokenLen = 24
	maxTokenLen = 256
)

// TokenPath is the access token file, next to sessions.json.
func TokenPath() string {
	return filepath.Join(filepath.Dir(store.DefaultPath()), tokenFileName)
}

// LoadOrCreateToken returns the access token stored at path, creating an
// owner-only file with a new random token when none exists.
func LoadOrCreateToken(path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create token directory: %w", err)
	}
	token, err := readToken(path)
	if err == nil {
		return token, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	raw := make([]byte, tokenBytes)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("generate access token: %w", err)
	}
	token = hex.EncodeToString(raw)
	// Publish atomically: write a private temp file, then hard-link it into
	// place. Link fails if another process created the token first, and then
	// that token wins.
	tmp, err := os.CreateTemp(filepath.Dir(path), tokenFileName+".tmp.*")
	if err != nil {
		return "", fmt.Errorf("create access token: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write access token: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write access token: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("write access token: %w", err)
	}
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("publish access token: %w", err)
	}
	return readToken(path)
}

func readToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("access token %s is not a regular file", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(path, 0o600); err != nil {
			return "", fmt.Errorf("restrict access token %s: %w", path, err)
		}
	}
	data, err := os.ReadFile(path) // #nosec G304 -- UAM's own token file next to its config.
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if ValidateToken(token) != nil {
		return "", fmt.Errorf("access token %s is malformed; delete it to create a new one", path)
	}
	return token, nil
}

// ValidateToken accepts 24 to 256 printable ASCII characters without
// whitespace. The generated token (64 hex characters) always qualifies. The
// error never contains the token.
func ValidateToken(token string) error {
	if len(token) < minTokenLen {
		return fmt.Errorf("the access token is too short: use at least %d characters", minTokenLen)
	}
	if len(token) > maxTokenLen {
		return fmt.Errorf("the access token is too long: use at most %d characters", maxTokenLen)
	}
	for i := 0; i < len(token); i++ {
		if token[i] <= ' ' || token[i] > '~' {
			return errors.New("the access token must be printable ASCII without whitespace")
		}
	}
	return nil
}

// SetToken validates token and atomically replaces the access token file at
// path with it: a private temp file, renamed over any existing file. A
// running service keeps its old token until it restarts; the new one then
// invalidates every session cookie, since cookies are derived from it.
func SetToken(path, token string) error {
	if err := ValidateToken(token); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), tokenFileName+".tmp.*")
	if err != nil {
		return fmt.Errorf("create access token: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write access token: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write access token: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write access token: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace access token: %w", err)
	}
	return nil
}

// signingKey is the HMAC key for session cookies, file keys and file
// cookies: the random secret, bound to the token so a new token still
// invalidates every cookie. Signing with the secret keeps the token itself
// out of every signed value.
func signingKey(secret, token string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("uam-web-signing-key-v1|" + token))
	return mac.Sum(nil)
}

// sessionCookie derives a session cookie bound to the request host and valid
// until exp (Unix seconds). The cookie never carries the token itself and
// survives service restarts; replacing the token file invalidates every
// cookie. Binding the host keeps a cookie that leaks to another origin (for
// example another service on 127.0.0.1) from being replayed at a different
// one, and the expiry bounds how long a leaked cookie stays usable. The
// nonce makes each sign-in its own session, so Log out can revoke one
// cookie (by its MAC, see revokedSessions) and no other.
func sessionCookie(key []byte, host string, exp int64) string {
	e := strconv.FormatInt(exp, 10)
	nonce := rand.Text()
	return e + "." + nonce + "." + cookieMAC(key, host, e, nonce)
}

// cookieMAC signs a session cookie. An empty nonce is a cookie issued before
// the nonce existed; those are still accepted until they expire, so an
// upgrade signs nobody out. Drop that label once they are all expired
// (cookieMaxAge after 2026-10-13).
func cookieMAC(key []byte, host, exp, nonce string) string {
	mac := hmac.New(sha256.New, key)
	if nonce == "" {
		mac.Write([]byte("uam-web-session-v3|" + strings.ToLower(host) + "|" + exp))
	} else {
		mac.Write([]byte("uam-web-session-v4|" + strings.ToLower(host) + "|" + exp + "|" + nonce))
	}
	return hex.EncodeToString(mac.Sum(nil))
}

// fileKey derives the key the file key route takes in place of the cookie,
// for one Task on the request host until exp (Unix seconds). A page opened
// from the Task's directory is sandboxed, so its requests for its sibling
// files carry no cookie; under a key in the path its relative links still
// resolve, and the key opens no other route and no other Task.
func fileKey(key []byte, host, id string, exp int64) string {
	e := strconv.FormatInt(exp, 10)
	return e + "." + fileKeyMAC(key, host, id, e)
}

func fileKeyMAC(key []byte, host, id, exp string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("uam-web-file-v2|" + strings.ToLower(host) + "|" + id + "|" + exp))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) validFileKey(key, host, id string) bool {
	exp, mac, ok := strings.Cut(key, ".")
	if !ok {
		return false
	}
	until, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() >= until {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(mac), []byte(fileKeyMAC(s.signKey, host, id, exp))) == 1
}

// fileCookie derives the companion a file key needs over HTTPS: the view route
// sets it next to the redirect, HttpOnly and scoped to the Task's file-key
// path, so the key in a view URL opens nothing without the browser that
// opened it. SameSite=None lets the sandboxed page's own requests carry it;
// plain HTTP cannot set such a cookie, so there the key alone is checked.
func fileCookie(key []byte, host, id string, exp int64) string {
	e := strconv.FormatInt(exp, 10)
	return e + "." + fileCookieMAC(key, host, id, e)
}

func fileCookieMAC(key []byte, host, id, exp string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("uam-web-file-cookie-v1|" + strings.ToLower(host) + "|" + id + "|" + exp))
	return hex.EncodeToString(mac.Sum(nil))
}

// validFileCookie reports whether r carries an unexpired companion cookie for
// Task id on its host.
func (s *Server) validFileCookie(r *http.Request, id string) bool {
	for _, c := range r.CookiesNamed(fileCookieName) {
		exp, mac, ok := strings.Cut(c.Value, ".")
		if !ok {
			continue
		}
		until, err := strconv.ParseInt(exp, 10, 64)
		if err != nil || time.Now().Unix() >= until {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(mac), []byte(fileCookieMAC(s.signKey, r.Host, id, exp))) == 1 {
			return true
		}
	}
	return false
}

func (s *Server) authenticated(r *http.Request) bool {
	_, _, ok := s.sessionMAC(r)
	return ok
}

// sessionMAC finds r's first session cookie that verifies and is not
// revoked, and returns its MAC, which names the session, and its expiry
// (Unix seconds). Every uam_web cookie is checked, since a browser may send
// more than one.
func (s *Server) sessionMAC(r *http.Request) (mac string, exp int64, ok bool) {
	for _, c := range r.CookiesNamed(cookieName) {
		if mac, exp, ok := s.verifySession(r.Host, c.Value); ok && !s.revoked.revoked(mac) {
			return mac, exp, true
		}
	}
	return "", 0, false
}

// verifySession checks one session cookie value for host. ok is false for a
// malformed, expired or forged value.
func (s *Server) verifySession(host, value string) (mac string, exp int64, ok bool) {
	e, rest, ok := strings.Cut(value, ".")
	if !ok {
		return "", 0, false
	}
	exp, err := strconv.ParseInt(e, 10, 64)
	if err != nil || time.Now().Unix() >= exp {
		return "", 0, false
	}
	nonce, mac, ok := strings.Cut(rest, ".")
	if !ok {
		nonce, mac = "", rest
	}
	if subtle.ConstantTimeCompare([]byte(mac), []byte(cookieMAC(s.signKey, host, e, nonce))) != 1 {
		return "", 0, false
	}
	return mac, exp, true
}

// secureRequest reports whether the browser sent r over HTTPS: its own
// TLS, a Host that is one of the https public origins, or X-Forwarded-Proto
// from a reverse proxy on loopback. Another peer's header is ignored, as
// signInClient ignores its X-Forwarded-For.
func (s *Server) secureRequest(r *http.Request) bool {
	if r.TLS != nil || s.mcpCallbackOrigins[strings.ToLower(r.Host)] != "" {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	return peer != nil && peer.IsLoopback() && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func setFileCookie(w http.ResponseWriter, value, path string, maxAge int) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- SameSite=None is required: the sandboxed page's own requests are cross-site; Secure and HttpOnly are always set and the path is one Task's file-key route.
		Name: fileCookieName, Value: value, Path: path, MaxAge: maxAge,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode,
	})
}

func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is set for TLS/proxy HTTPS; loopback/SSH HTTP is supported, with HttpOnly and SameSiteStrict always set.
		Name: cookieName, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.secureRequest(r),
	})
}
