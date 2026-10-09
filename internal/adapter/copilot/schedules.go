package copilot

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

const maxSchedules = 32

type scheduleSession interface {
	ListSchedules(context.Context) ([]rpc.ScheduleEntry, error)
}

func (a sdkSessionAdapter) ListSchedules(ctx context.Context) ([]rpc.ScheduleEntry, error) {
	result, err := a.s.RPC.Schedule.List(ctx)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("missing schedule list")
	}
	return result.Entries, nil
}

type scheduleReads struct {
	snapshot *agentapi.ScheduleSnapshot
	revision uint64
	reading  bool
	again    bool
	cancel   context.CancelFunc
}

// A native event only invalidates/kicks: requesting on the callback goroutine
// would stall the connection on which the RPC answer arrives.
func (c *conversation) checkSchedulesLocked() {
	if c.closed {
		return
	}
	c.schedules.revision++
	if c.schedules.snapshot != nil {
		c.scheduleSnapshotLocked(agentapi.ScheduleSnapshot{Supported: c.schedules.snapshot.Supported, Entries: []agentapi.ScheduleEntry{}, Reason: "refreshing"})
	}
	if c.sess == nil || c.schedules.reading {
		c.schedules.again = true
		return
	}
	runtime, ok := c.sess.(scheduleSession)
	if !ok {
		return
	}
	c.schedules.reading, c.schedules.again = true, false
	go c.readSchedules(runtime)
}

func (c *conversation) refreshActiveSchedulesLocked() {
	if s := c.schedules.snapshot; s != nil && len(s.Entries) > 0 {
		c.checkSchedulesLocked()
	}
}

func (c *conversation) readSchedules(runtime scheduleSession) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			cancel()
			return
		}
		revision := c.schedules.revision
		c.schedules.cancel = cancel
		c.mu.Unlock()
		entries, err := runtime.ListSchedules(ctx)
		cancel()
		snapshot := scheduleSnapshot(entries, err)
		c.mu.Lock()
		if !c.closed && revision == c.schedules.revision {
			c.scheduleSnapshotLocked(snapshot)
		}
		again := !c.closed && c.schedules.again
		c.schedules.reading, c.schedules.again, c.schedules.cancel = again, false, nil
		c.mu.Unlock()
		if !again {
			return
		}
	}
}

func (c *conversation) scheduleSnapshotLocked(snapshot agentapi.ScheduleSnapshot) {
	previous := c.schedules.snapshot
	if previous != nil && previous.Supported == snapshot.Supported && previous.Known == snapshot.Known && previous.Truncated == snapshot.Truncated && previous.Reason == snapshot.Reason && slices.Equal(previous.Entries, snapshot.Entries) {
		return
	}
	c.schedules.snapshot = &snapshot
	c.emitLocked(agentapi.Event{Kind: agentapi.EventSchedules, Schedules: &snapshot})
}

func (c *conversation) stopSchedulesLocked() {
	if c.schedules.cancel != nil {
		c.schedules.cancel()
	}
	c.schedules.snapshot, c.schedules.cancel = nil, nil
	c.schedules.again = false
}

func scheduleSnapshot(entries []rpc.ScheduleEntry, err error) agentapi.ScheduleSnapshot {
	out := agentapi.ScheduleSnapshot{Supported: true, Known: err == nil, Entries: []agentapi.ScheduleEntry{}}
	if err != nil {
		var response *copilot.RPCError
		if errors.As(err, &response) && response.Code == -32601 {
			out.Supported, out.Reason = false, "unsupported"
		} else {
			out.Reason = "unavailable"
		}
		return out
	}
	seen := map[int64]bool{}
	for i, entry := range entries {
		if i >= maxSchedules || entry.ID < 0 || seen[entry.ID] || entry.IntervalMs != nil && (*entry.IntervalMs < 0 || *entry.IntervalMs > 9007199254740991) || entry.Cron != nil && len(*entry.Cron) > 256 || entry.Tz != nil && len(*entry.Tz) > 128 || entry.NextRunAt.Year() < 1 || entry.NextRunAt.Year() > 9999 {
			out.Known, out.Truncated, out.Reason = false, true, "partial"
			if i >= maxSchedules {
				break
			}
			continue
		}
		seen[entry.ID] = true
		row := agentapi.ScheduleEntry{ID: strconv.FormatInt(entry.ID, 10), Recurring: entry.Recurring, NextRunAt: entry.NextRunAt, SelfPaced: entry.SelfPaced != nil && *entry.SelfPaced}
		if entry.IntervalMs != nil {
			row.IntervalMs = *entry.IntervalMs
		}
		if entry.Cron != nil {
			row.Cron = displaytext.Sanitize(*entry.Cron)
		}
		if entry.Tz != nil {
			row.Timezone = displaytext.Sanitize(*entry.Tz)
		}
		out.Entries = append(out.Entries, row)
	}
	return out
}
