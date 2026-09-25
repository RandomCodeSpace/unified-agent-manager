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
	cookieMaxAge  = 30 * 24 * 60 * 60
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

// sessionCookie derives a session cookie bound to the request host and valid
// until exp (Unix seconds). The cookie never carries the token itself and
// survives service restarts; replacing the token file invalidates every
// cookie. Binding the host keeps a cookie that leaks to another origin (for
// example another service on 127.0.0.1) from being replayed at a different
// one, and the expiry bounds how long a leaked cookie stays usable.
func sessionCookie(token, host string, exp int64) string {
	e := strconv.FormatInt(exp, 10)
	return e + "." + cookieMAC(token, host, e)
}

func cookieMAC(token, host, exp string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("uam-web-session-v2|" + strings.ToLower(host) + "|" + exp))
	return hex.EncodeToString(mac.Sum(nil))
}

// fileKey derives the key the file key route takes in place of the cookie,
// for one Task on the request host until exp (Unix seconds). A page opened
// from the Task's directory is sandboxed, so its requests for its sibling
// files carry no cookie; under a key in the path its relative links still
// resolve, and the key opens no other route and no other Task.
func fileKey(token, host, id string, exp int64) string {
	e := strconv.FormatInt(exp, 10)
	return e + "." + fileKeyMAC(token, host, id, e)
}

func fileKeyMAC(token, host, id, exp string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("uam-web-file-v1|" + strings.ToLower(host) + "|" + id + "|" + exp))
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
	return subtle.ConstantTimeCompare([]byte(mac), []byte(fileKeyMAC(s.token, host, id, exp))) == 1
}

func (s *Server) authenticated(r *http.Request) bool {
	if s.noAuth {
		return true
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	exp, mac, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	until, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() >= until {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(mac), []byte(cookieMAC(s.token, r.Host, exp))) == 1
}

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is set for TLS/proxy HTTPS; loopback/SSH HTTP is supported, with HttpOnly and SameSiteStrict always set.
		Name: cookieName, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secureRequest(r),
	})
}
