package copilot

import (
	"context"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// Opt-in: two short, tool-free turns in a disposable session. The existing
// adapter projection must agree with the sum of the live per-call charges.
func TestWaybillLiveNanoAIUSumsToSession(t *testing.T) {
	if os.Getenv("UAM_WAYBILL_LIVE") != "1" {
		t.Skip("set UAM_WAYBILL_LIVE=1 for the paid gpt-6-luna probe")
	}
	version, err := exec.Command("copilot", "--version").Output()
	if err != nil || !strings.Contains(string(version), "1.0.94") {
		t.Fatal("probe requires Copilot CLI 1.0.94")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := newSDKClient()
	if err != nil {
		t.Fatal(err)
	}
	defer client.ForceStop()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	c := client.(sdkClientAdapter).c
	session, err := c.CreateSession(ctx, &copilot.SessionConfig{
		Model: "gpt-6-luna", WorkingDirectory: t.TempDir(),
		EnableConfigDiscovery: copilot.Bool(false), EnableSkills: copilot.Bool(false),
		EnableSessionStore: copilot.Bool(false), EnableFileHooks: copilot.Bool(false),
		AvailableTools: []string{},
		OnPermissionRequest: func(copilot.PermissionRequest, copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
			return &rpc.PermissionDecisionReject{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = session.Disconnect()
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := c.DeleteSession(cleanup, session.SessionID); err != nil {
			t.Errorf("delete probe session: %v", err)
		}
	}()
	h := openWeb(t)
	var mu sync.Mutex
	var nanos [2]int64
	var costs [2]float64
	var calls [2]int
	turn := 0
	unsubscribe := session.On(func(event copilot.SessionEvent) {
		mu.Lock()
		defer mu.Unlock()
		switch data := event.Data.(type) {
		case *rpc.AssistantUsageData:
			if data.Model != "gpt-6-luna" {
				t.Errorf("unexpected model %q", data.Model)
			}
			calls[turn]++
			if data.CopilotUsage != nil {
				nanos[turn] += int64(data.CopilotUsage.TotalNanoAiu)
			}
			if data.Cost != nil {
				costs[turn] += *data.Cost
			}
			h.fs.onEvent(event)
		case *rpc.SessionUsageCheckpointData:
			h.fs.onEvent(event)
		}
	})
	defer unsubscribe()
	for i := range 2 {
		mu.Lock()
		turn = i
		mu.Unlock()
		if _, err := session.SendAndWait(ctx, copilot.MessageOptions{Prompt: "Reply with exactly OK. Do not use tools."}); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	usage := usages(h.sink.all())
	if nanos[0] <= 0 || nanos[1] <= 0 || len(usage) == 0 {
		t.Fatalf("missing charged turns: nano_aiu=%v calls=%v", nanos, calls)
	}
	want := float64(nanos[0]+nanos[1]) / 1e9
	if math.Abs(usage[len(usage)-1]-want) > 1e-9 {
		t.Fatalf("turn sum %.9f != session ai_units %.9f", want, usage[len(usage)-1])
	}
	t.Logf("CLI 1.0.94 model=gpt-6-luna calls=%v nano_aiu=%v premium_cost=%v sum_ai_units=%.9f session_ai_units=%.9f", calls, nanos, costs, want, usage[len(usage)-1])
}
