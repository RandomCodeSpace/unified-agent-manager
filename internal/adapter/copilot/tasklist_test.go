package copilot

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/github/copilot-sdk/go/rpc"
)

func readTaskList(t *testing.T, name string) []rpc.TaskInfo {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var strict rpc.TaskList
	if json.Unmarshal(raw, &strict) == nil {
		t.Fatalf("%s: SDK decoded it; the fixture no longer shows the defect", name)
	}
	tasks, err := decodeTaskList(raw)
	if err != nil {
		t.Fatalf("decodeTaskList(%s): %v", name, err)
	}
	return tasks
}

func TestDecodeTaskListReadsDetachedShellFloatPID(t *testing.T) {
	tasks := readTaskList(t, "tasklist-detached.json")
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
	shell, ok := tasks[0].(*rpc.TaskShellInfo)
	if !ok {
		t.Fatalf("task = %T, want *rpc.TaskShellInfo", tasks[0])
	}
	if shell.ID != "1" || shell.Command != "sleep 120" || shell.Status != rpc.TaskStatusRunning ||
		shell.AttachmentMode != rpc.TaskShellInfoAttachmentModeDetached || shell.Pid == nil || *shell.Pid != 3892667 {
		t.Fatalf("shell = %+v", shell)
	}
}

func TestDecodeTaskListKeepsValidEntriesBesideMalformedOnes(t *testing.T) {
	tasks := readTaskList(t, "tasklist-mixed.json")
	var ids []string
	for _, task := range tasks {
		switch v := task.(type) {
		case *rpc.TaskAgentInfo:
			ids = append(ids, "agent:"+v.ID)
		case *rpc.TaskShellInfo:
			ids = append(ids, "shell:"+v.ID)
		default:
			t.Fatalf("unexpected task %T", task)
		}
	}
	if want := []string{"agent:agent-1", "shell:1", "shell:0"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
}

func TestDecodeTaskListFailsWhenNoEntryIsReadable(t *testing.T) {
	raw := []byte(`{"tasks":[{"type":"shell","id":"2","status":"running","startedAt":"yesterday"}]}`)
	if tasks, err := decodeTaskList(raw); err == nil {
		t.Fatalf("decodeTaskList = %v, nil; want an error, not an empty list", tasks)
	}
	if tasks, err := decodeTaskList([]byte(`{"tasks":[]}`)); err != nil || len(tasks) != 0 {
		t.Fatalf("empty list = %v, %v", tasks, err)
	}
}

// TestListTasksRawMatchesSDKLayout fails when an SDK upgrade moves the field
// listTasksRaw reads; ListTasks would silently fall back to the strict decode.
func TestListTasksRawMatchesSDKLayout(t *testing.T) {
	field, ok := reflect.TypeFor[rpc.TasksAPI]().FieldByName("client")
	if !ok || field.Type.Kind() != reflect.Pointer {
		t.Fatalf("rpc.TasksAPI.client = %v, %v", field.Type, ok)
	}
	method := reflect.Zero(field.Type).MethodByName("Request")
	if !method.IsValid() || method.Type() != reflect.TypeFor[rawRequestFunc]() {
		t.Fatalf("Request = %v", method)
	}
}

type fakeRawClient struct {
	method string
	params any
}

func (c *fakeRawClient) Request(_ context.Context, method string, params any) (json.RawMessage, error) {
	c.method, c.params = method, params
	return json.RawMessage(`{"tasks":[]}`), nil
}

func TestListTasksRawCallsTheInnerClient(t *testing.T) {
	client := &fakeRawClient{}
	api := &struct{ client *fakeRawClient }{client: client}
	raw, ok, err := listTasksRaw(context.Background(), api, "s1")
	if !ok || err != nil || string(raw) != `{"tasks":[]}` {
		t.Fatalf("listTasksRaw = %s, %v, %v", raw, ok, err)
	}
	if client.method != "session.tasks.list" || !reflect.DeepEqual(client.params, map[string]any{"sessionId": "s1"}) {
		t.Fatalf("request = %q %v", client.method, client.params)
	}
	if _, ok, _ := listTasksRaw(context.Background(), rpc.NewSessionRPC(nil, "s1").Tasks, "s1"); ok {
		t.Fatal("nil client must fall back to the typed call")
	}
}
