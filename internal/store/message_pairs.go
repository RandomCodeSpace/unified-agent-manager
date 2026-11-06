package store

import (
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Message pair states: the owner has not decided yet, allowed messages, or
// declined them (sticky until the owner resets the pair).
const (
	PairPending = "pending"
	PairAllowed = "allowed"
	PairDenied  = "denied"
)

// Bounds of the stored message pairs.
const (
	MaxMessagePairs    = 32
	MaxPairMessages    = 20
	MaxHeldMessageText = 16 << 10
)

// WebMessagePair is the owner's decision about messages between two Threads
// (uam_send): A and B are their IDs, sorted. Used counts deliveries since the
// owner last allowed the pair, Limit how many that allows. Held is the
// message waiting for the owner; Messages are the newest messages, oldest
// first.
type WebMessagePair struct {
	ID        string           `json:"id"`
	A         string           `json:"a"`
	B         string           `json:"b"`
	State     string           `json:"state"`
	Linked    bool             `json:"linked,omitempty"`
	Used      int              `json:"used"`
	Limit     int              `json:"limit"`
	Held      *WebHeldMessage  `json:"held,omitempty"`
	Messages  []WebPeerMessage `json:"messages"`
	UpdatedAt time.Time        `json:"updated_at"`
}

// WebHeldMessage is a message waiting for the owner: Reason is "first" (the
// pair's first message), "limit" (the pair used its deliveries) or "link"
// (a Thread is unattended, so the owner must link the pair).
type WebHeldMessage struct {
	ID     string    `json:"id"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	Text   string    `json:"text"`
	CallID string    `json:"call_id,omitempty"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// WebPeerMessage is one message between the Threads of a pair and what
// became of it (held, queued, working, replied, stopped, failed,
// interrupted, cancelled, declined or undelivered).
type WebPeerMessage struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	CallID    string    `json:"call_id,omitempty"`
	Status    string    `json:"status"`
	Detail    string    `json:"detail,omitempty"`
	At        time.Time `json:"at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TurnPeer marks a turn another Thread's message started: Message is that
// message's ID and From the sending Thread, with its name and Project as
// they were. Reply is set for the automatic reply to a message this Thread
// sent.
type TurnPeer struct {
	Pair        string `json:"pair"`
	Message     string `json:"message"`
	From        string `json:"from"`
	FromName    string `json:"from_name,omitempty"`
	FromProject string `json:"from_project,omitempty"`
	Reply       bool   `json:"reply,omitempty"`
}

// dropInvalidMessagePairs removes pairs whose untrusted on-disk fields fail
// the checks below and trims the rest to their bounds.
func dropInvalidMessagePairs(cfg *Config) {
	for key, p := range cfg.WebMessagePairs {
		reason := ""
		switch {
		case p.ID == "" || p.ID != key || isUnsafeArgv(p.ID):
			reason = "invalid id"
		case p.A == "" || p.B == "" || p.A >= p.B || isUnsafeArgv(p.A) || isUnsafeArgv(p.B):
			reason = "invalid threads"
		case p.State != PairPending && p.State != PairAllowed && p.State != PairDenied:
			reason = "invalid state"
		case p.Limit < 1 || p.Used < 0:
			reason = "invalid limits"
		case p.Held != nil && (len(p.Held.Text) > MaxHeldMessageText || p.Held.From != p.A && p.Held.From != p.B):
			reason = "invalid held message"
		}
		if reason != "" {
			log.Warn("dropping invalid message pair", "key", key, "reason", reason)
			delete(cfg.WebMessagePairs, key)
			continue
		}
		if n := len(p.Messages); n > MaxPairMessages {
			p.Messages = p.Messages[n-MaxPairMessages:]
			cfg.WebMessagePairs[key] = p
		}
	}
	if len(cfg.WebMessagePairs) == 0 {
		cfg.WebMessagePairs = nil
	}
}
