package copilot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func (s *fakeSession) ReadTaskOutput(_ context.Context, id, cursor string) (*rpc.TasksReadOutputResult, error) {
	return s.readOutput(id, cursor)
}

// shellLog answers reads of text the way CLI 1.0.108 does: a byte-offset
// cursor, at most perRead bytes a read ending at a line break, and the exit
// once a read of an ended shell reaches the end.
func shellLog(text *string, ended *bool, perRead int, reads *[]string) func(id, cursor string) (*rpc.TasksReadOutputResult, error) {
	return func(id, cursor string) (*rpc.TasksReadOutputResult, error) {
		*reads = append(*reads, id+"@"+cursor)
		at := len(strings.TrimPrefix(cursor, "at:"))
		if cursor != "" && !strings.HasPrefix(cursor, "at:") || at > len(*text) {
			return nil, rejectedError{errors.New("JSON-RPC Error -32602: invalid tasks.readOutput cursor")}
		}
		end := len(*text)
		if at+perRead < end {
			end = at + strings.LastIndexByte((*text)[at:at+perRead], '\n') + 1
		}
		res := &rpc.TasksReadOutputResult{Text: (*text)[at:end], Cursor: "at:" + strings.Repeat("x", end)}
		if *ended && end == len(*text) {
			res.Exit = &rpc.TaskShellOutputExit{Status: rpc.TaskStatusCompleted}
		}
		return res, nil
	}
}

func TestWebBackgroundOutputReadsInBoundedChunks(t *testing.T) {
	h := openWeb(t)
	reader := h.conv.(agentapi.BackgroundOutputReader)
	ctx := context.Background()
	// 36 KB of 12-byte lines with control sequences, read 20 KB at a time by the CLI.
	text := strings.Repeat("\x1b[31mé\x1b[0m\n", 3000)
	ended := false
	var reads []string
	h.fs.readOutput = shellLog(&text, &ended, 20000, &reads)

	var got strings.Builder
	cursor, calls := "", 0
	for {
		out, err := reader.ReadBackgroundOutput(ctx, "server", cursor)
		if err != nil {
			t.Fatal(err)
		}
		calls++
		if len(out.Text) > backgroundOutputCap || !utf8.ValidString(out.Text) || strings.ContainsRune(out.Text, '\x1b') {
			t.Fatalf("chunk %d: %d bytes, unsafe or over the cap", calls, len(out.Text))
		}
		if out.Text != "" && !strings.HasSuffix(out.Text, "\n") {
			t.Fatalf("chunk %d ends inside a line: %q", calls, out.Text[len(out.Text)-8:])
		}
		if out.Done {
			t.Fatal("a running shell's output reported done")
		}
		got.WriteString(out.Text)
		cursor = out.Cursor
		if !out.More && out.Text == "" {
			break
		}
		if calls > 60 {
			t.Fatal("the cursor does not advance")
		}
	}
	if want := strings.Repeat("é\n", 3000); got.String() != want {
		t.Fatalf("read %d bytes, want %d", got.Len(), len(want))
	}
	if reads[0] != "server@" {
		t.Fatalf("first read = %q, want the start of server's output", reads[0])
	}

	// The shell prints once more and ends: the next read has the new text, the one after says done.
	text += "last\n"
	ended = true
	out, err := reader.ReadBackgroundOutput(ctx, "server", cursor)
	if err != nil || out.Text != "last\n" || out.More {
		t.Fatalf("after more output = %+v, %v", out, err)
	}
	if out, err = reader.ReadBackgroundOutput(ctx, "server", out.Cursor); err != nil || !out.Done || out.Text != "" || out.More {
		t.Fatalf("at the end = %+v, %v", out, err)
	}
}

func TestWebBackgroundOutputFailures(t *testing.T) {
	h := openWeb(t)
	reader := h.conv.(agentapi.BackgroundOutputReader)
	ctx := context.Background()
	text, ended := "one\n", true
	var reads []string
	h.fs.readOutput = shellLog(&text, &ended, 100, &reads)
	// A cursor uam did not write, and one the CLI's log no longer reaches.
	for _, cursor := range []string{"nonsense", "-1:at:", "x:at:", "9:", "0:elsewhere"} {
		if _, err := reader.ReadBackgroundOutput(ctx, "server", cursor); !errors.Is(err, agentapi.ErrBackgroundOutputUnavailable) {
			t.Fatalf("cursor %q = %v, want unavailable", cursor, err)
		}
	}
	h.fs.readOutput = func(string, string) (*rpc.TasksReadOutputResult, error) {
		return nil, rejectedError{errors.New("JSON-RPC Error -32602: No detached shell task with id 'server'")}
	}
	if _, err := reader.ReadBackgroundOutput(ctx, "server", ""); !errors.Is(err, agentapi.ErrBackgroundOutputUnavailable) {
		t.Fatalf("refused read = %v, want unavailable", err)
	}
	h.fs.readOutput = func(string, string) (*rpc.TasksReadOutputResult, error) {
		return nil, errors.New("pipe \x1b[31mclosed")
	}
	if _, err := reader.ReadBackgroundOutput(ctx, "server", ""); err == nil || errors.Is(err, agentapi.ErrBackgroundOutputUnavailable) || strings.ContainsRune(err.Error(), '\x1b') {
		t.Fatalf("failed read = %v, want a plain error", err)
	}
	// The read holds no conversation lock: a blocked one leaves the conversation usable.
	entered, release := make(chan struct{}), make(chan struct{})
	h.fs.readOutput = func(string, string) (*rpc.TasksReadOutputResult, error) {
		close(entered)
		<-release
		return &rpc.TasksReadOutputResult{Cursor: "at:"}, nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := reader.ReadBackgroundOutput(ctx, "server", "")
		done <- err
	}()
	<-entered
	if h.conv.(*conversation).isClosed() {
		t.Fatal("conversation closed")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := h.conv.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadBackgroundOutput(ctx, "server", ""); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("closed read = %v, want ErrClosed", err)
	}
}

// The CLI keeps readable output only for a detached shell, so the snapshot
// marks an attached one; a shell of another or unknown mode stays readable.
func TestWebBackgroundTasksMarkAttachedShellOutputUnreadable(t *testing.T) {
	h := openWeb(t)
	h.fs.setTasks(
		&rpc.TaskShellInfo{ID: "0", Command: "make test", AttachmentMode: rpc.TaskShellInfoAttachmentModeAttached, Status: rpc.TaskStatusRunning},
		&rpc.TaskShellInfo{ID: "1", Command: "sleep 120", AttachmentMode: rpc.TaskShellInfoAttachmentModeDetached, Status: rpc.TaskStatusRunning},
		&rpc.TaskShellInfo{ID: "2", Command: "true", Status: rpc.TaskStatusCompleted},
	)
	h.fs.onEvent(ev("background", &rpc.SessionBackgroundTasksChangedData{}))
	settleTasks(t, h, 1)
	got := h.sink.last().BackgroundTasks
	if got == nil || len(got.Tasks) != 3 || !got.Tasks[0].OutputUnreadable || got.Tasks[1].OutputUnreadable || got.Tasks[2].OutputUnreadable {
		t.Fatalf("background tasks = %+v", got)
	}
}
