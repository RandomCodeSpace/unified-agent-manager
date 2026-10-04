package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	headerUAMInstance  = "X-UAM-Instance-ID"
	connectionFileName = "web-connections.json"
	maxConnectionFile  = 4 << 20
	maxConnections     = 64
	maxWorkloadGrants  = 256
)

// Connection is the owner-visible registration. Secrets never enter this DTO.
type Connection struct {
	ID            string   `json:"id"`
	InstanceID    string   `json:"instance_id"`
	Label         string   `json:"label"`
	BaseURL       string   `json:"base_url"`
	Enabled       bool     `json:"enabled"`
	Generation    uint64   `json:"generation"`
	HasKey        bool     `json:"has_key"`
	Version       string   `json:"version"`
	ProtocolMajor int      `json:"protocol_major"`
	Capabilities  []string `json:"capabilities"`
	AllowPrivate  bool     `json:"allow_private"`
}

// connectionTarget stays server-side; consumers must serialize Connection only.
type connectionTarget struct {
	Connection
	Credential       string   `json:"credential"`
	AllowedAddresses []string `json:"allowed_addresses,omitempty"`
}

type workloadGrant struct {
	ID               string    `json:"id"`
	ClientInstanceID string    `json:"client_instance_id"`
	Label            string    `json:"label"`
	CreatedAt        time.Time `json:"created_at"`
	Verifier         string    `json:"verifier"`
}

type connectionDisk struct {
	Version     int                         `json:"version"`
	InstanceID  string                      `json:"instance_id"`
	MasterHash  string                      `json:"master_hash"`
	Connections map[string]connectionTarget `json:"connections"`
	Grants      map[string]workloadGrant    `json:"grants"`
}

type connectionLifetime struct {
	ctx    context.Context
	cancel context.CancelFunc
}

type connectionRegistry struct {
	mu      sync.Mutex
	path    string
	root    *os.Root
	data    connectionDisk
	ctx     context.Context
	cancel  context.CancelFunc
	active  map[string]connectionLifetime
	grants  map[string]connectionLifetime
	changed chan struct{}
}

func connectionError(status int, code, message string) *Error {
	err := newError(status, "%s", message)
	err.Code = code
	return err
}

func credentialHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func openConnectionRegistry(ctx context.Context, dir, master string) (*connectionRegistry, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	root, err := openConnectionRoot(dir)
	if err != nil {
		return nil, err
	}
	keepRoot := false
	defer func() {
		if !keepRoot {
			_ = root.Close()
		}
	}()
	path := filepath.Join(root.Name(), connectionFileName)
	data, err := readConnectionDisk(root)
	if errors.Is(err, os.ErrNotExist) {
		id, idErr := uuid.NewRandom()
		if idErr != nil {
			return nil, idErr
		}
		data = connectionDisk{Version: 1, InstanceID: id.String(), MasterHash: credentialHash(master), Connections: map[string]connectionTarget{}, Grants: map[string]workloadGrant{}}
		encoded, _ := json.Marshal(data)
		err = writeConnectionDisk(root, encoded, true)
		if err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		data, err = readConnectionDisk(root)
	}
	if err != nil {
		return nil, err
	}
	if data.MasterHash != credentialHash(master) {
		// Like owner cookies, incoming grants cease to work after a master rotation.
		data.MasterHash, data.Grants = credentialHash(master), map[string]workloadGrant{}
		encoded, _ := json.Marshal(data)
		if err := writeConnectionDisk(root, encoded, false); err != nil {
			return nil, err
		}
	}
	life, cancel := context.WithCancel(ctx)
	keepRoot = true
	r := &connectionRegistry{path: path, root: root, data: data, ctx: life, cancel: cancel, active: map[string]connectionLifetime{}, grants: map[string]connectionLifetime{}, changed: make(chan struct{})}
	for id := range data.Connections {
		r.active[id] = newConnectionLifetime(life)
	}
	for id := range data.Grants {
		r.grants[id] = newConnectionLifetime(life)
	}
	return r, nil
}

func readConnectionDisk(root *os.Root) (connectionDisk, error) {
	var data connectionDisk
	info, err := root.Lstat(connectionFileName)
	if err != nil {
		return data, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return data, errors.New("connection state must be a private regular file")
	}
	file, err := root.Open(connectionFileName)
	if err != nil {
		return data, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !privateConnectionFile(opened) {
		return data, errors.New("connection state must be an owned private regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxConnectionFile+1))
	if err != nil {
		return data, err
	}
	if len(raw) > maxConnectionFile {
		return data, errors.New("connection state exceeds its size limit")
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return data, errors.New("connection state is invalid JSON")
	}
	if data.Version != 1 || !validInstanceID(data.InstanceID) || data.Connections == nil || data.Grants == nil || len(data.Connections) > maxConnections || len(data.Grants) > maxWorkloadGrants {
		return data, errors.New("connection state has an unsupported or invalid schema")
	}
	seen := map[string]bool{}
	for id, c := range data.Connections {
		if id != c.ID || !validInstanceID(id) || !validInstanceID(c.InstanceID) || c.InstanceID == data.InstanceID || seen[c.InstanceID] || c.Generation == 0 || c.Credential == "" {
			return data, errors.New("connection state has an invalid registration")
		}
		seen[c.InstanceID] = true
	}
	for id, g := range data.Grants {
		if id != g.ID || !validInstanceID(id) || !validInstanceID(g.ClientInstanceID) || len(g.Verifier) != 64 {
			return data, errors.New("connection state has an invalid grant")
		}
	}
	return data, nil
}

func validInstanceID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}
func newConnectionLifetime(ctx context.Context) connectionLifetime {
	life, cancel := context.WithCancel(ctx)
	return connectionLifetime{life, cancel}
}
func cloneTarget(t connectionTarget) connectionTarget {
	t.Capabilities = slices.Clone(t.Capabilities)
	t.AllowedAddresses = slices.Clone(t.AllowedAddresses)
	return t
}
func (r *connectionRegistry) InstanceID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.data.InstanceID
}
func (r *connectionRegistry) Changed() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.changed
}
func (r *connectionRegistry) List() []Connection {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Connection, 0, len(r.data.Connections))
	for _, c := range r.data.Connections {
		item := cloneTarget(c).Connection
		item.HasKey = c.Credential != ""
		out = append(out, item)
	}
	slices.SortFunc(out, func(a, b Connection) int { return strings.Compare(a.ID, b.ID) })
	return out
}
func (r *connectionRegistry) lookup(id string) (connectionTarget, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.data.Connections[id]
	if !ok {
		return connectionTarget{}, connectionError(404, "connection_not_found", "connected instance not found")
	}
	return cloneTarget(c), nil
}
func (r *connectionRegistry) Acquire(id string) (connectionTarget, context.Context, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.data.Connections[id]
	if !ok {
		return connectionTarget{}, nil, connectionError(404, "connection_not_found", "connected instance not found")
	}
	if !c.Enabled || r.ctx.Err() != nil {
		return connectionTarget{}, nil, connectionError(409, "connection_disabled", "connected instance is disabled")
	}
	return cloneTarget(c), r.active[id].ctx, nil
}

// saveLocked publishes before replacing live state. A failed disk write leaves both unchanged.
func (r *connectionRegistry) saveLocked(data connectionDisk) error {
	raw, err := json.Marshal(data)
	if err == nil && len(raw) > maxConnectionFile {
		err = errors.New("connection state exceeds its size limit")
	}
	if err == nil {
		err = writeConnectionDisk(r.root, raw, false)
	}
	if err != nil {
		return errors.New("could not save connection state")
	}
	r.data = data
	close(r.changed)
	r.changed = make(chan struct{})
	return nil
}

// put uses the generation read before network I/O; stale pairing results cannot resurrect removed entries.
func (r *connectionRegistry) put(target connectionTarget, expected uint64) (Connection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx.Err() != nil {
		return Connection{}, connectionError(409, "connection_changed", "connection state is closed")
	}
	old, exists := r.data.Connections[target.ID]
	if exists && old.Generation != expected || !exists && expected != 0 {
		return Connection{}, connectionError(409, "connection_changed", "connection changed; reload before saving")
	}
	if target.InstanceID == r.data.InstanceID {
		return Connection{}, connectionError(409, "duplicate_instance", "cannot attach this instance to itself")
	}
	for id, c := range r.data.Connections {
		if id != target.ID && c.InstanceID == target.InstanceID {
			return Connection{}, connectionError(409, "duplicate_instance", "this instance is already connected through another URL")
		}
	}
	if exists && target.InstanceID != old.InstanceID {
		return Connection{}, connectionError(409, "identity_mismatch", "the target instance identity changed; add it as a new connection")
	}
	if !exists && len(r.data.Connections) >= maxConnections {
		return Connection{}, connectionError(409, "invalid_connection", "too many connected instances")
	}
	target.Generation = expected + 1
	target.HasKey = target.Credential != ""
	data := r.data
	data.Connections = maps.Clone(data.Connections)
	data.Connections[target.ID] = cloneTarget(target)
	if err := r.saveLocked(data); err != nil {
		return Connection{}, err
	}
	if life, ok := r.active[target.ID]; ok {
		life.cancel()
	}
	r.active[target.ID] = newConnectionLifetime(r.ctx)
	return cloneTarget(target).Connection, nil
}
func (r *connectionRegistry) remove(id string, expected uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.data.Connections[id]
	if !ok {
		return connectionError(404, "connection_not_found", "connected instance not found")
	}
	if c.Generation != expected {
		return connectionError(409, "connection_changed", "connection changed; reload before removing")
	}
	data := r.data
	data.Connections = maps.Clone(data.Connections)
	delete(data.Connections, id)
	if err := r.saveLocked(data); err != nil {
		return err
	}
	r.active[id].cancel()
	delete(r.active, id)
	return nil
}
func (r *connectionRegistry) issue(clientID, label string) (workloadGrant, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx.Err() != nil {
		return workloadGrant{}, "", errors.New("connection state is closed")
	}
	if len(r.data.Grants) >= maxWorkloadGrants {
		return workloadGrant{}, "", connectionError(409, "invalid_connection", "too many workload grants")
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return workloadGrant{}, "", err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return workloadGrant{}, "", err
	}
	token := id.String() + "." + hex.EncodeToString(raw[:])
	grant := workloadGrant{ID: id.String(), ClientInstanceID: clientID, Label: label, CreatedAt: time.Now().UTC(), Verifier: credentialHash(token)}
	data := r.data
	data.Grants = maps.Clone(data.Grants)
	data.Grants[grant.ID] = grant
	if err := r.saveLocked(data); err != nil {
		return workloadGrant{}, "", err
	}
	r.grants[grant.ID] = newConnectionLifetime(r.ctx)
	return grant, token, nil
}
func (r *connectionRegistry) authenticate(token string) (workloadGrant, context.Context, error) {
	id, _, ok := strings.Cut(token, ".")
	r.mu.Lock()
	defer r.mu.Unlock()
	grant, exists := r.data.Grants[id]
	if !ok || !exists || r.ctx.Err() != nil || subtle.ConstantTimeCompare([]byte(credentialHash(token)), []byte(grant.Verifier)) != 1 {
		return workloadGrant{}, nil, connectionError(401, "remote_auth_required", "workload authentication required")
	}
	return grant, r.grants[id].ctx, nil
}
func (r *connectionRegistry) revoke(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data.Grants[id]; !ok {
		return nil
	}
	data := r.data
	data.Grants = maps.Clone(data.Grants)
	delete(data.Grants, id)
	if err := r.saveLocked(data); err != nil {
		return err
	}
	r.grants[id].cancel()
	delete(r.grants, id)
	return nil
}
func (r *connectionRegistry) close() {
	r.cancel()
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.root.Close()
}
