package web

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

func closedHistoryContractTask(t *testing.T, record agentapi.History, expose func(*agenttest.Pager) agentapi.Provider) (*Manager, *agenttest.Pager, SessionSummary, *agenttest.Conversation) {
	t.Helper()
	p := agenttest.NewPager("fake", allCaps)
	var provider agentapi.Provider = p
	if expose != nil {
		provider = expose(p)
	}
	m := startManager(t, openTestStore(t), provider)
	sum, conv := createSession(t, m, p.Provider)
	for _, it := range record.Items {
		conv.EmitItem(it)
	}
	for _, sa := range record.Subagents {
		conv.EmitSubagent(sa)
	}
	p.SetHistory(sum.ConversationID, record)
	return m, p, sum, conv
}

func closedHistoryEvidenceRecord() agentapi.History {
	start, exit := time.Now().Add(-time.Minute), 0
	return agentapi.History{Items: []agentapi.Item{
		{ID: "u", Kind: agentapi.ItemUser, Text: "run the tests", Time: start},
		{ID: "check", Kind: agentapi.ItemTool, Time: start.Add(time.Second), Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted, Input: `{"command":"go test ./internal/web"}`, Output: "ok  example.com/web 0.01s", ExitCode: &exit}},
		{ID: "answer", Kind: agentapi.ItemAssistant, Text: "Tests passed.", Time: start.Add(2 * time.Second)},
	}}
}

func TestClosedHistoryKeepsProvidersWithoutRecordPaging(t *testing.T) {
	for _, tc := range []struct {
		name   string
		expose func(*agenttest.Pager) agentapi.Provider
	}{
		{"without pager", func(p *agenttest.Pager) agentapi.Provider { return p.Provider }},
		{"without reader", func(p *agenttest.Pager) agentapi.Provider {
			return struct {
				agentapi.Provider
				agentapi.HistoryPager
				agentapi.SubagentPager
			}{p.Provider, p, p}
		}},
		{"without subagent pager", func(p *agenttest.Pager) agentapi.Provider {
			return struct {
				agentapi.Provider
				agentapi.HistoryReader
				agentapi.HistoryPager
			}{p.Provider, p, p}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := closedHistoryEvidenceRecord()
			if tc.name == "without subagent pager" {
				record.Subagents = []agentapi.Subagent{{ID: "child", Name: "explore", Status: agentapi.SubagentCompleted}}
				record.Items = append(record.Items, agentapi.Item{ID: "child-answer", Kind: agentapi.ItemAssistant, AgentID: "child", Text: "child result"})
			}
			m, p, sum, _ := closedHistoryContractTask(t, record, tc.expose)
			if _, err := m.Close(sum.ID); err != nil {
				t.Fatal(err)
			}
			held, _ := m.heldItems(sum.ID)
			if len(held) != len(record.Items) {
				t.Fatalf("retained %d items without a complete read-only recovery path", len(held))
			}
			if len(p.Reads()) != 0 || len(p.WindowReads()) != 0 {
				t.Fatal("closing an unrecoverable transcript tried reading its record")
			}
		})
	}
}

func TestClosedHistoryRecoversAfterItsReloadedCacheExpires(t *testing.T) {
	record := closedHistoryEvidenceRecord()
	archived := archiveRecord(30)
	record.Items = append(record.Items, archived.Items...)
	record.Subagents = archived.Subagents
	m, p, sum, _ := closedHistoryContractTask(t, record, nil)
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if held, _ := m.heldItems(sum.ID); len(held) != 0 {
		t.Fatal("initial close did not release the live transcript")
	}
	waitHistory(t, m, sum.ID, HistoryLoaded)
	if len(p.Reads()) != 1 {
		t.Fatalf("first reload made %d reads", len(p.Reads()))
	}
	later := time.Now().Add(historyIdle + time.Second)
	m.mu.Lock()
	m.now = func() time.Time { return later }
	m.mu.Unlock()
	m.evictHistories()
	if held, _ := m.heldItems(sum.ID); len(held) != 0 {
		t.Fatal("idle sweep did not release the reread transcript")
	}
	// These reads precede a main Detail request, so they must reach the record
	// using the state left by this second release.
	body, err := m.ItemBody(sum.ID, "", "answer")
	if err != nil || body.Item.Text != "Tests passed." {
		t.Fatalf("body after cache expiry = %+v, %v", body, err)
	}
	child, err := m.CompactSubagent(sum.ID, "sa1")
	if err != nil || len(child.Items) == 0 || child.Items[len(child.Items)-1].Text != "sub 119" {
		t.Fatalf("subagent after cache expiry = %+v, %v", child, err)
	}
	evidence, err := m.TurnEvidence(context.Background(), sum.ID, time.Time{}, time.Time{})
	if err != nil || len(evidence.Checks) != 1 || evidence.Checks[0].ItemID != "check" {
		t.Fatalf("evidence after cache expiry = %+v, %v", evidence, err)
	}
	if len(p.Reads()) != 2 || len(p.Opens()) != 1 {
		t.Fatalf("provider calls after second release: %d reads, %d opens", len(p.Reads()), len(p.Opens()))
	}
}

func TestClosedHistoryServesARetainedTruncatedUnavailableFallback(t *testing.T) {
	st := openTestStore(t)
	id, convID := mustUUID(t), "truncated-unavailable-live"
	seedWebRecord(t, st, id, convID, StateCompleted)
	p := agenttest.NewPager("fake", allCaps)
	p.SetHistory(convID, agentapi.History{})
	scripted := newScripted("fake")
	scripted.Provider = p.Provider
	scripted.script(func(p *scriptedProvider) { p.historyErr = errors.New("record unavailable") })
	provider := struct {
		agentapi.Provider
		agentapi.HistoryReader
		agentapi.HistoryPager
		agentapi.SubagentPager
	}{scripted, p, p, p}
	m := startManager(t, st, provider)
	if submission, err := m.Submit(id, PromptRequest{Text: "continue", RequestID: mustUUID(t)}); err != nil || submission.Status != SubmissionAccepted {
		t.Fatalf("submit = %+v, %v", submission, err)
	}
	conv := p.Last()
	for i := range maxItems + 1 {
		conv.EmitItem(agentapi.Item{ID: fmt.Sprintf("old-%04d", i), Kind: agentapi.ItemAssistant, Text: "old turn"})
	}
	for _, it := range closedHistoryEvidenceRecord().Items {
		conv.EmitItem(it)
	}
	conv.EmitSubagent(agentapi.Subagent{ID: "child", Name: "explore", Status: agentapi.SubagentCompleted})
	conv.EmitItem(agentapi.Item{ID: "child-answer", Kind: agentapi.ItemAssistant, AgentID: "child", Text: "retained child result"})
	if d := detail(t, m, id); d.History != HistoryUnavailable || !d.HistoryTruncated || len(d.Items) == 0 {
		t.Fatalf("fixture history = %q, truncated %v, items %d", d.History, d.HistoryTruncated, len(d.Items))
	}
	if _, err := m.Close(id); err != nil {
		t.Fatal(err)
	}
	p.SetReadHook(func(context.Context, agentapi.ReadRequest) (agentapi.History, error) {
		return agentapi.History{}, errors.New("reload still unavailable")
	})
	evidence, err := m.TurnEvidence(context.Background(), id, time.Time{}, time.Time{})
	if err != nil || len(evidence.Checks) != 1 || evidence.Checks[0].ItemID != "check" {
		t.Fatalf("retained evidence = %+v, %v", evidence, err)
	}
	child, err := m.Subagent(id, "child")
	if err != nil || len(child.Items) != 1 || child.Items[0].Text != "retained child result" {
		t.Fatalf("retained subagent = %+v, %v", child, err)
	}
	if len(p.Opens()) != 1 {
		t.Fatal("reading the retained fallback resumed the conversation")
	}
}

func TestClosedHistoryKeepsUnconfirmedSteerReceipt(t *testing.T) {
	record := closedHistoryEvidenceRecord()
	m, p, sum, conv := closedHistoryContractTask(t, record, nil)
	receipt := agentapi.Item{ID: "local-steer", Kind: agentapi.ItemUser, Text: "also check cancellation", Delivery: agentapi.DeliverySteer, SteerStatus: agentapi.SteerNotDelivered, Time: time.Now()}
	conv.EmitItem(receipt)
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	held, _ := m.heldItems(sum.ID)
	if len(held) != len(record.Items)+1 || !slices.ContainsFunc(held, func(it agentapi.Item) bool {
		return it.ID == receipt.ID && it.Text == receipt.Text && it.SteerStatus == receipt.SteerStatus
	}) {
		t.Fatal("closing discarded a local receipt absent from the provider record")
	}
	if len(p.Reads()) != 0 {
		t.Fatal("closing a protected local receipt read the provider record")
	}
}

func TestClosedHistoryKeepsAnUnavailableLiveRecord(t *testing.T) {
	st := openTestStore(t)
	id, convID := mustUUID(t), "unreadable-live"
	seedWebRecord(t, st, id, convID, StateCompleted)
	p := agenttest.NewPager("fake", allCaps)
	p.SetHistory(convID, closedHistoryEvidenceRecord())
	scripted := newScripted("fake")
	scripted.Provider = p.Provider
	scripted.script(func(p *scriptedProvider) { p.historyErr = errors.New("record unreadable") })
	provider := struct {
		agentapi.Provider
		agentapi.HistoryReader
		agentapi.HistoryPager
		agentapi.SubagentPager
	}{scripted, p, p, p}
	m := startManager(t, st, provider)
	if submission, err := m.Submit(id, PromptRequest{Text: "continue", RequestID: mustUUID(t)}); err != nil || submission.Status != SubmissionAccepted {
		t.Fatalf("submit = %+v, %v", submission, err)
	}
	p.Last().EmitItem(agentapi.Item{ID: "live-answer", Kind: agentapi.ItemAssistant, Text: "only in live memory"})
	if d := detail(t, m, id); d.History != HistoryUnavailable || !strings.Contains(d.HistoryReason, "record unreadable") {
		t.Fatalf("history = %q, reason = %q", d.History, d.HistoryReason)
	}
	if _, err := m.Close(id); err != nil {
		t.Fatal(err)
	}
	held, _ := m.heldItems(id)
	if !slices.ContainsFunc(held, func(it agentapi.Item) bool { return it.ID == "live-answer" }) {
		t.Fatal("closing discarded the transcript despite its unavailable record")
	}
}

func TestClosedHistoryKeepsLoadedFallbackAfterLatestRecordReadFails(t *testing.T) {
	var scripted *scriptedProvider
	record := closedHistoryEvidenceRecord()
	m, _, sum, _ := closedHistoryContractTask(t, record, func(p *agenttest.Pager) agentapi.Provider {
		scripted = newScripted("fake")
		scripted.Provider = p.Provider
		return struct {
			agentapi.Provider
			agentapi.HistoryReader
			agentapi.HistoryPager
			agentapi.SubagentPager
		}{scripted, p, p, p}
	})
	viewer, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	scripted.script(func(p *scriptedProvider) { p.historyErr = errors.New("latest record read failed") })
	if submission, err := m.Submit(sum.ID, PromptRequest{Text: "continue", RequestID: mustUUID(t)}); err != nil || submission.Status != SubmissionAccepted {
		t.Fatalf("submit = %+v, %v", submission, err)
	}
	if d := detail(t, m, sum.ID); d.History != HistoryLoaded {
		t.Fatalf("failed latest read lost the usable loaded fallback: %q", d.History)
	}
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	m.Unsubscribe(viewer)
	if held, _ := m.heldItems(sum.ID); len(held) != len(record.Items) {
		t.Fatalf("latest failed record read permitted releasing %d fallback items", len(record.Items)-len(held))
	}
}

func TestClosedHistoryCloseWaitsForTheLastViewer(t *testing.T) {
	record := closedHistoryEvidenceRecord()
	m, p, sum, _ := closedHistoryContractTask(t, record, nil)
	first, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	m.Unsubscribe(first)
	if held, _ := m.heldItems(sum.ID); len(held) != len(record.Items) {
		t.Fatal("closing released a transcript while another viewer remained")
	}
	m.Unsubscribe(second)
	if held, _ := m.heldItems(sum.ID); len(held) != 0 {
		t.Fatalf("last viewer left %d closed transcript items retained", len(held))
	}
	if len(p.Reads()) != 0 {
		t.Fatal("viewer departure read the transcript instead of releasing it")
	}
}

func TestClosedHistoryArchivesARereadSettledTask(t *testing.T) {
	record := archiveRecord(130)
	m, p, _, sum := archivedTask(t, record, true)
	d, err := m.CompactDetail(sum.ID)
	if err != nil || len(d.Items) == 0 || d.HistoryBefore == nil || *d.HistoryBefore == "" {
		t.Fatalf("detail before archive = %+v, %v", d, err)
	}
	before, boundary := *d.HistoryBefore, d.Items[0].ID
	if _, err := m.Archive(sum.ID); err != nil {
		t.Fatal(err)
	}
	if held, _ := m.heldItems(sum.ID); len(held) != 0 {
		t.Fatalf("archiving a reread settled Task retained %d items", len(held))
	}
	page, err := m.CompactOlderHistory(sum.ID, "", before)
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("saved history cursor after archive = %+v, %v", page, err)
	}
	ids := recordIDs(record, "")
	at := slices.Index(ids, boundary)
	want := ids[max(0, at-historyPageItems):at]
	got := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		got = append(got, it.ID)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("saved cursor page = %v, want %v", got, want)
	}
	if page.After == "" {
		t.Fatal("archive page lost its forward cursor")
	}
	forward, err := m.CompactHistoryPage(sum.ID, "", "", page.After)
	if err != nil || len(forward.Items) == 0 || forward.Items[0].ID != boundary {
		t.Fatalf("forward page after archive = %+v, %v", forward, err)
	}
	if len(p.Opens()) != 0 {
		t.Fatal("archiving or following an old cursor resumed the settled conversation")
	}
}

func TestClosedHistoryEvidenceReportsReloadFailure(t *testing.T) {
	m, p, sum, _ := closedHistoryContractTask(t, closedHistoryEvidenceRecord(), nil)
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	p.SetReadHook(func(context.Context, agentapi.ReadRequest) (agentapi.History, error) {
		return agentapi.History{}, errors.New("journal read failed")
	})
	if evidence, err := m.TurnEvidence(context.Background(), sum.ID, time.Time{}, time.Time{}); err == nil {
		t.Fatalf("unreadable evidence was silently returned as %+v", evidence)
	}
	if len(p.Reads()) != 1 || len(p.Opens()) != 1 {
		t.Fatalf("provider calls: %d reads, %d opens; want one read without resume", len(p.Reads()), len(p.Opens()))
	}
}

func TestClosedHistoryLegacySubagentReadsItsArchivedTranscript(t *testing.T) {
	m, p, sum, _ := closedHistoryContractTask(t, archiveRecord(130), nil)
	if _, err := m.Archive(sum.ID); err != nil {
		t.Fatal(err)
	}
	if held, _ := m.heldItems(sum.ID); len(held) != 0 {
		t.Fatal("archived transcript was not released")
	}
	child, err := m.Subagent(sum.ID, "sa1")
	if err != nil || len(child.Items) != 120 || child.Items[0].Text != "sub 0" || child.Items[119].Text != "sub 119" {
		t.Fatalf("legacy subagent after archive = %+v, %v", child, err)
	}
	if len(p.Opens()) != 1 {
		t.Fatal("reading the archived subagent resumed its parent conversation")
	}
}

func TestClosedHistoryRerunFindsTheOwnerBeyondTheNewestWindow(t *testing.T) {
	record := closedHistoryEvidenceRecord()
	for i := range maxRerunRead + 10 {
		record.Items = append(record.Items, agentapi.Item{ID: fmt.Sprintf("later-%03d", i), Kind: agentapi.ItemAssistant, Text: "continued analysis"})
	}
	m, p, sum, _ := closedHistoryContractTask(t, record, nil)
	// Main history loading keeps only the newest tail; the owner prompt is
	// reachable only by walking the pager past that tail.
	p.SetReadHook(func(context.Context, agentapi.ReadRequest) (agentapi.History, error) {
		return agentapi.History{Items: slices.Clone(record.Items[len(record.Items)-maxRerunRead:]), Truncated: true}, nil
	})
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if held, _ := m.heldItems(sum.ID); len(held) != 0 {
		t.Fatal("closed transcript was not released")
	}
	again, err := m.Rerun(sum.ID, RerunRequest{RequestID: mustUUID(t)})
	if err != nil || again.RerunOf != sum.ID {
		t.Fatalf("rerun after close = %+v, %v", again, err)
	}
	if sends := p.Last().Sends(); !slices.Equal(sends, []string{record.Items[0].Text}) {
		t.Fatalf("rerun sends = %q, want original owner prompt %q", sends, record.Items[0].Text)
	}
}

func TestClosedHistoryExportKeepsSubagentHeading(t *testing.T) {
	record := archiveRecord(30)
	record.Subagents[0].Description = "survey the state lifecycle"
	record.Subagents[0].Runs = []agentapi.SubagentRun{
		{StartedAt: time.Now().Add(-time.Minute), Status: agentapi.SubagentCompleted, Trigger: agentapi.SubagentTriggerSpawn},
		{StartedAt: time.Now().Add(-time.Second), Status: agentapi.SubagentCompleted, Trigger: agentapi.SubagentTriggerUser},
	}
	m, p, sum, _ := closedHistoryContractTask(t, record, nil)
	before, _, err := m.ExportMarkdown(context.Background(), sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := "<summary>Subagent: survey the state lifecycle · completed · 2 runs</summary>"
	if !strings.Contains(string(before), want) {
		t.Fatalf("live export lacks fixture heading %q", want)
	}
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if held, _ := m.heldItems(sum.ID); len(held) != 0 {
		t.Fatal("closed transcript was not released")
	}
	after, _, err := m.ExportMarkdown(context.Background(), sum.ID)
	if err != nil || !strings.Contains(string(after), want) {
		t.Fatalf("closed export lost subagent heading %q: %v", want, err)
	}
	if len(p.Opens()) != 1 {
		t.Fatal("exporting the closed transcript resumed its conversation")
	}
}

func TestClosedHistoryCancelledReloadCannotRepopulateReleasedTask(t *testing.T) {
	m, p, sum, _ := closedHistoryContractTask(t, closedHistoryEvidenceRecord(), nil)
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if held, _ := m.heldItems(sum.ID); len(held) != 0 {
		t.Fatal("closed transcript was not released")
	}
	started, cancelled, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var finishOnce sync.Once
	release := func() { finishOnce.Do(func() { close(finish) }) }
	t.Cleanup(release)
	p.SetReadHook(func(ctx context.Context, _ agentapi.ReadRequest) (agentapi.History, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-finish
		return agentapi.History{Items: []agentapi.Item{{ID: "stale", Kind: agentapi.ItemAssistant, Text: "stale journal read"}}}, nil
	})
	if _, err := m.Detail(sum.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("released transcript did not start a read-only reload")
	}
	if submission, err := m.Submit(sum.ID, PromptRequest{Text: "next turn", RequestID: mustUUID(t)}); err != nil || submission.Status != SubmissionAccepted {
		t.Fatalf("submit = %+v, %v", submission, err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("resuming the conversation did not cancel its old transcript reload")
	}
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	release()
	// Shutdown joins the ignored-cancellation reader, so the assertion cannot
	// race a late attempt to install its stale result.
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if held, _ := m.heldItems(sum.ID); len(held) != 0 {
		t.Fatalf("cancelled reload repopulated the released Task with %+v", held)
	}
}
