package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// sessionList is a recorded session.list answer for a cwd filter, with a
// second remote entry and one from a sub-directory added.
const sessionList = `[
 {"sessionId":"e5cc1da2-d847-4205-8e3c-c27a133f0127","startTime":"2026-09-24T17:29:16.888Z","modifiedTime":"2026-09-24T17:29:28.869Z","summary":"Use the task tool to start exactly one explore subagent","isRemote":false,"context":{"cwd":"/work/project"}},
 {"sessionId":"5036d6d8-b2dd-4964-b423-72ef7baf4884","startTime":"2026-09-24T17:30:20.100Z","modifiedTime":"2026-09-24T17:30:38.246Z","isRemote":false,"context":{"cwd":"/work/project"}},
 {"sessionId":"11111111-2222-4333-8444-555555555555","startTime":"2026-09-24T17:31:00Z","modifiedTime":"2026-09-24T17:31:05Z","summary":"remote","isRemote":true,"context":{"cwd":"/work/project"}},
 {"sessionId":"66666666-7777-4888-8999-aaaaaaaaaaaa","startTime":"2026-09-24T17:32:00Z","modifiedTime":"2026-09-24T17:32:05Z","summary":"sub","isRemote":false,"context":{"cwd":"/work/project/sub"}}
]`

func TestPreviousListsTheExactDirectoryNewestFirst(t *testing.T) {
	var listed []copilot.SessionMetadata
	if err := json.Unmarshal([]byte(sessionList), &listed); err != nil {
		t.Fatal(err)
	}
	fc := &fakeClient{listed: listed}
	got, err := readerProvider(fc).Previous(context.Background(), "/work/project")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fc.listDirs, []string{"/work/project"}) {
		t.Fatalf("listed %v", fc.listDirs)
	}
	if len(got) != 2 || got[0].ID != "5036d6d8-b2dd-4964-b423-72ef7baf4884" || got[0].Title != "" ||
		got[1].ID != "e5cc1da2-d847-4205-8e3c-c27a133f0127" || got[1].Title != "Use the task tool to start exactly one explore subagent" ||
		!got[1].CreatedAt.Equal(time.Date(2026, 9, 24, 17, 29, 16, 888e6, time.UTC)) || !got[1].UpdatedAt.Equal(time.Date(2026, 9, 24, 17, 29, 28, 869e6, time.UTC)) {
		t.Fatalf("previous = %+v", got)
	}
	if _, err := readerProvider(&fakeClient{listErr: errors.New("boom")}).Previous(context.Background(), "/work/project"); err == nil {
		t.Fatal("a list failure must be reported")
	}
}

func TestInUseAsksTheCLI(t *testing.T) {
	// Recorded sessions.checkInUse answer while an SDK process held the session.
	var res rpc.SessionsCheckInUseResult
	if err := json.Unmarshal([]byte(`{"inUse":["5036d6d8-b2dd-4964-b423-72ef7baf4884"]}`), &res); err != nil {
		t.Fatal(err)
	}
	fc := &fakeClient{inUse: res.InUse}
	p := readerProvider(fc)
	ids := []string{"5036d6d8-b2dd-4964-b423-72ef7baf4884", "e5cc1da2-d847-4205-8e3c-c27a133f0127"}
	held, err := p.InUse(context.Background(), ids)
	if err != nil || !slices.Equal(held, res.InUse) || len(fc.checked) != 1 || !slices.Equal(fc.checked[0], ids) {
		t.Fatalf("held = %v, %v; asked %v", held, err, fc.checked)
	}
	if held, err := p.InUse(context.Background(), nil); err != nil || held != nil || len(fc.checked) != 1 {
		t.Fatalf("no ids = %v, %v; asked %v", held, err, fc.checked)
	}
	if _, err := readerProvider(&fakeClient{inUseErr: errors.New("method not found")}).InUse(context.Background(), ids); err == nil {
		t.Fatal("a failed check must be reported, never taken as not in use")
	}
	if !p.Capabilities().Import {
		t.Fatal("Copilot imports")
	}
}

func TestImportCapabilityIsProbedOnceAndCached(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(fmt.Sprint(supported), func(t *testing.T) {
			fc := &fakeClient{importSupport: &supported}
			p := readerProvider(fc)
			t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
			if p.Capabilities().Import {
				t.Fatal("import advertised before probe")
			}
			for range 2 {
				if _, err := p.Models(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			if p.Capabilities().Import != supported || fc.importProbes != 1 {
				t.Fatalf("import %v; probes %d", p.Capabilities().Import, fc.importProbes)
			}
		})
	}
}
