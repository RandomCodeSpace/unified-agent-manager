package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Saved custom model (BYOM) keys are stored encrypted with AES-256-GCM under
// a random key kept in customKeyFileName beside the state file, owner-only.
// A stored value starts with sealedKeyPrefix; in memory WebCustomModel.APIKey
// holds the plain key, or is empty with SealedKey and KeyError set when the
// stored value could not be decrypted.
const (
	customKeyFileName = "byom.key"
	sealedKeyPrefix   = "enc1:"
)

func (s *Store) keyPath() string { return filepath.Join(filepath.Dir(s.path), customKeyFileName) }

// loadKey reads the key file; create makes one when there is none.
func (s *Store) loadKey(create bool) ([]byte, error) {
	path := s.keyPath()
	raw, err := os.ReadFile(path) // #nosec G304 -- the store's own key file beside its configured path.
	switch {
	case err == nil:
		key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("%s is not a 32-byte hex key", path)
		}
		return key, nil
	case errors.Is(err, fs.ErrNotExist) && create:
	default:
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	// CreateTemp makes the file 0600; the rename publishes it whole.
	tmp, err := os.CreateTemp(filepath.Dir(path), customKeyFileName+".tmp.*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, err = tmp.WriteString(hex.EncodeToString(key) + "\n")
	if err == nil {
		err = tmp.Sync()
	}
	if err = errors.Join(err, tmp.Close()); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return nil, err
	}
	return key, nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

// sealKey encrypts plain with a fresh random nonce.
func sealKey(key []byte, plain string) (string, error) {
	aead, err := gcm(key)
	if err != nil {
		return "", err
	}
	return sealedKeyPrefix + base64.StdEncoding.EncodeToString(aead.Seal(nil, nil, []byte(plain), nil)), nil // #nosec G407 -- NewGCMWithRandomNonce draws a fresh nonce itself and requires an empty one here.
}

// openKey decrypts a value sealKey made.
func openKey(key []byte, sealed string) (string, error) {
	aead, err := gcm(key)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, sealedKeyPrefix))
	if err != nil {
		return "", err
	}
	plain, err := aead.Open(nil, nil, raw, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// sealCustomKeys returns list as the state file stores it: every saved key
// encrypted, and a key this process could not decrypt kept as it was stored.
func (s *Store) sealCustomKeys(list []WebCustomModel) ([]WebCustomModel, error) {
	var key []byte
	out := slices.Clone(list)
	for i := range out {
		m := &out[i]
		switch {
		case m.APIKey != "":
			if key == nil {
				var err error
				if key, err = s.loadKey(true); err != nil {
					return nil, fmt.Errorf("custom model key file: %w", err)
				}
			}
			sealed, err := sealKey(key, m.APIKey)
			if err != nil {
				return nil, err
			}
			m.APIKey = sealed
		case m.SealedKey != "":
			m.APIKey = m.SealedKey
		}
		m.SealedKey, m.KeyError = "", ""
	}
	return out, nil
}

// openCustomKeys decrypts the stored keys of w in place. A key it cannot
// decrypt is kept in SealedKey with KeyError saying why. It reports whether
// any key was stored in clear, as before encryption.
func (s *Store) openCustomKeys(w *WebSettings) (plain bool) {
	var key []byte
	var keyErr error
	for i := range w.CustomModels {
		m := &w.CustomModels[i]
		if m.APIKey == "" {
			continue
		}
		if !strings.HasPrefix(m.APIKey, sealedKeyPrefix) {
			plain = true
			continue
		}
		if key == nil && keyErr == nil {
			if key, keyErr = s.loadKey(false); errors.Is(keyErr, fs.ErrNotExist) {
				keyErr = fmt.Errorf("the key file %s is missing", s.keyPath())
			}
		}
		v, err := "", keyErr
		if err == nil {
			if v, err = openKey(key, m.APIKey); err != nil {
				err = fmt.Errorf("it could not be decrypted with %s", s.keyPath())
			}
		}
		if err != nil {
			m.SealedKey, m.APIKey = m.APIKey, ""
			m.KeyError = fmt.Sprintf("the saved API key cannot be used: %s; enter the key again", err)
			continue
		}
		m.APIKey = v
	}
	return plain
}
