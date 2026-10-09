package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

// Every operation on a Task that does not exist reports 404.
func TestOperationsOnAMissingTaskReportNotFound(t *testing.T) {
	m, _, _ := newTestManager(t)
	ctx := context.Background()
	for name, op := range map[string]func() error{
		"summary":  func() error { _, err := m.Summary("missing"); return err },
		"subagent": func() error { _, err := m.Subagent("missing", "agent"); return err },
		"commands": func() error { _, err := m.Commands(ctx, "missing"); return err },
		"command": func() error {
			_, err := m.Command("missing", CommandRequest{RequestID: mustUUID(t), Name: "review"})
			return err
		},
		"cancel":       func() error { _, err := m.Cancel("missing"); return err },
		"close":        func() error { _, err := m.Close("missing"); return err },
		"resume queue": func() error { return m.ResumeQueue("missing") },
		"answer": func() error {
			_, err := m.Answer("missing", "p1", agentapi.Answer{Decision: "allow"})
			return err
		},
	} {
		if err := op(); statusOf(err) != http.StatusNotFound || err.Error() != "session not found" {
			t.Fatalf("%s of a missing task = %v, want 404", name, err)
		}
	}
}

// Once the service is shutting down, Task and Project changes are refused
// with 503 and nothing reaches the provider.
func TestChangesAfterShutdownAreRefused(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := idleSubagent(t, m, prov)
	archived, _ := createSession(t, m, prov)
	if _, err := m.Archive(archived.ID); err != nil {
		t.Fatal(err)
	}
	opens := len(prov.Opens())
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for name, op := range map[string]func() error{
		"prompt": func() error {
			_, err := m.Submit(sum.ID, PromptRequest{Text: "more", RequestID: mustUUID(t)})
			return err
		},
		"commands":        func() error { _, err := m.Commands(ctx, sum.ID); return err },
		"cancel queued":   func() error { return m.CancelQueued(sum.ID, mustUUID(t)) },
		"clear queue":     func() error { return m.ClearQueue(sum.ID) },
		"resume queue":    func() error { return m.ResumeQueue(sum.ID) },
		"subagent prompt": func() error { _, err := m.PromptSubagent(sum.ID, "target", "more", mustUUID(t)); return err },
		"settle":          func() error { _, err := m.Settle(sum.ID); return err },
		"delete":          func() error { return m.Delete(archived.ID) },
		"remove project":  func() error { return m.RemoveProject(archived.ProjectID) },
	} {
		if err := op(); !errors.Is(err, errShuttingDown) {
			t.Fatalf("%s after shutdown = %v, want %v", name, err, errShuttingDown)
		}
	}
	if len(prov.Opens()) != opens || len(conv.Sends()) != 0 || len(conv.SubagentPrompts()) != 0 {
		t.Fatalf("refused changes reached the provider: opens %d -> %d, sends %v, follow-ups %v", opens, len(prov.Opens()), conv.Sends(), conv.SubagentPrompts())
	}
	if _, err := m.Summary(archived.ID); err != nil {
		t.Fatalf("archived task after a refused delete: %v", err)
	}
}

// A settled Task lists no commands, cancels nothing and keeps its mode until
// it is reopened.
func TestSettledTaskRefusesCommandsCancelAndModeChanges(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	if _, err := m.Settle(sum.ID); err != nil {
		t.Fatal(err)
	}
	const settled = "the task is settled; reopen it first"
	_, err := m.Commands(context.Background(), sum.ID)
	wantConflict(t, "commands of a settled task", err, settled)
	wantConflict(t, "cancel a settled task", errOf(m.Cancel(sum.ID)), settled)
	wantConflict(t, "yolo for a settled task", errOf(m.SetMode(sum.ID, "yolo")), settled)
	if s := mustSummary(t, m, sum.ID); s.Mode != "safe" || s.Stage != StageSettled || len(prov.Opens()) != 1 {
		t.Fatalf("settled task changed: %+v, opens %d", s, len(prov.Opens()))
	}
}

// Malformed prompts and commands, and requests a provider or subagent cannot
// take, are refused before anything reaches the provider.
func TestRequestsTheTaskCannotTakeAreRefused(t *testing.T) {
	prov := agenttest.NewProvider("fake", allCaps)
	prov.SetModels([]agentapi.Model{{ID: "a", Name: "A"}}, nil)
	caps := allCaps
	caps.Cancel = false
	uncancellable := agenttest.NewProvider("plain", caps)
	m := startManager(t, openTestStore(t), prov, uncancellable)
	project := addProject(t, m, t.TempDir())
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Model: "a"})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	conv.EmitSubagent(agentapi.Subagent{ID: "helper", Status: agentapi.SubagentIdle})
	plain, err := m.Create(CreateRequest{Provider: "plain", ProjectID: project})
	if err != nil {
		t.Fatal(err)
	}
	plainConv := uncancellable.Last()
	plainConv.EmitTurn(agentapi.TurnWorking, "")

	for _, tc := range []struct {
		name    string
		err     error
		status  int
		message string
	}{
		{"blank prompt", errOf2(m.Submit(sum.ID, PromptRequest{Text: " \n\t", RequestID: mustUUID(t)})), http.StatusBadRequest, "prompt text, a file or an attachment is required"},
		{"oversized command arguments", errOf2(m.Command(sum.ID, CommandRequest{RequestID: mustUUID(t), Name: "review", Arguments: strings.Repeat("x", maxPromptBytes+1)})), http.StatusRequestEntityTooLarge, "arguments are too large"},
		{"prompt dropping the model", errOf2(m.Submit(sum.ID, PromptRequest{Text: "hi", RequestID: mustUUID(t), Settings: &PromptSettings{}})), http.StatusBadRequest, "model must be an offered model ID"},
		{"unknown mode", errOf(m.SetMode(sum.ID, "turbo")), http.StatusBadRequest, "mode must be safe, yolo or assisted"},
		{"cancel without the capability", errOf(m.Cancel(plain.ID)), http.StatusConflict, "this provider does not support cancelling a turn"},
		{"stop an idle subagent", func() error { _, err := m.CancelSubagent(sum.ID, "helper"); return err }(), http.StatusConflict, "the subagent has no active conversation"},
	} {
		if statusOf(tc.err) != tc.status || tc.err.Error() != tc.message {
			t.Fatalf("%s = %v, want %d %q", tc.name, tc.err, tc.status, tc.message)
		}
	}
	if len(conv.Sends()) != 0 || len(conv.CommandRuns()) != 0 || len(conv.SubagentCancels()) != 0 || plainConv.Cancels() != 0 {
		t.Fatalf("refused requests reached the provider: sends %v, runs %v, stops %v, cancels %d", conv.Sends(), conv.CommandRuns(), conv.SubagentCancels(), plainConv.Cancels())
	}
}

// Clearing a queue that is already empty changes nothing.
func TestClearingAnEmptyQueueChangesNothing(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	before := mustSummary(t, m, sum.ID)
	if err := m.ClearQueue(sum.ID); err != nil {
		t.Fatalf("clear an empty queue = %v", err)
	}
	if after := mustSummary(t, m, sum.ID); after != before {
		t.Fatalf("summary changed: %+v -> %+v", before, after)
	}
}

// A steer carrying an attachment whose stored file is gone is refused, and
// nothing is steered.
func TestSteerWithAnAttachmentNoLongerStoredIsRefused(t *testing.T) {
	m, _, sum, conv, dirs := uploadTask(t, "docs")
	note := mustUpload(t, m, sum.ID, "notes.md", []byte("# notes\n"))
	mustSubmit(t, m, sum.ID, "first", mustUUID(t), ModeSend, SubmissionAccepted)
	conv.EmitTurn(agentapi.TurnWorking, "")
	if err := os.Remove(filepath.Join(dirs.task(sum.ID), note.ID)); err != nil {
		t.Fatal(err)
	}
	_, err := m.Submit(sum.ID, PromptRequest{Text: "also", RequestID: mustUUID(t), Mode: ModeSteer, Attachments: []string{note.ID}})
	wantConflict(t, "steer with a vanished attachment", err, "attachment notes.md is no longer stored")
	if steers := conv.Steers(); len(steers) != 0 {
		t.Fatalf("steers = %v", steers)
	}
}
