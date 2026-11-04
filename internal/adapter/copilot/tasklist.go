package copilot

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strconv"
	"unsafe"

	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// rawRequestFunc is the signature of the SDK's internal jsonrpc2.Client.Request.
type rawRequestFunc = func(context.Context, string, any) (json.RawMessage, error)

// listTasksRaw calls session.tasks.list through the JSON-RPC client inside
// api, an *rpc.TasksAPI. The SDK exposes no raw request and decodes the whole
// list strictly, so one entry it cannot read hides every task. ok is false
// when the SDK's layout no longer matches; the caller then uses the typed call.
// shortcut: reads an unexported SDK field; drop once the SDK decodes
// TaskShellInfo.pid as a number (still int64 in v1.0.29-preview.1).
func listTasksRaw(ctx context.Context, api any, sessionID string) (raw json.RawMessage, ok bool, err error) {
	v := reflect.ValueOf(api)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return nil, false, nil
	}
	f := v.Elem().FieldByName("client")
	if !f.IsValid() || f.Kind() != reflect.Pointer || f.IsNil() {
		return nil, false, nil
	}
	m := reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().MethodByName("Request") // #nosec G103 -- read-only access to the SDK's own client field, same type and address.
	if !m.IsValid() {
		return nil, false, nil
	}
	request, ok := m.Interface().(rawRequestFunc)
	if !ok {
		return nil, false, nil
	}
	raw, err = request(ctx, "session.tasks.list", map[string]any{"sessionId": sessionID})
	return raw, true, err
}

// decodeTaskList decodes a session.tasks.list result entry by entry, so one
// unreadable entry leaves the others listed. It fails only when the list
// itself is unreadable or every entry is.
func decodeTaskList(raw json.RawMessage) ([]rpc.TaskInfo, error) {
	var list struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	tasks := make([]rpc.TaskInfo, 0, len(list.Tasks))
	var firstErr error
	for _, entry := range list.Tasks {
		task, err := decodeTask(entry)
		if err != nil {
			log.Warn("copilot task list entry unreadable; skipped", "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		tasks = append(tasks, task)
	}
	if len(tasks) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return tasks, nil
}

// decodeTask decodes one entry with the SDK's own union decoder. CLI 1.0.105
// sends a detached shell's pid as 3892667.0, which the SDK's int64 rejects;
// a whole-number pid is rewritten as an integer and the entry decoded again.
func decodeTask(entry json.RawMessage) (rpc.TaskInfo, error) {
	task, err := decodeTaskEntry(entry)
	if err == nil {
		return task, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(entry, &fields) != nil {
		return nil, err
	}
	pid, perr := strconv.ParseFloat(string(fields["pid"]), 64)
	if perr != nil || pid != math.Trunc(pid) || math.Abs(pid) > 1<<53 {
		return nil, err
	}
	fields["pid"] = strconv.AppendInt(nil, int64(pid), 10)
	fixed, merr := json.Marshal(fields)
	if merr != nil {
		return nil, err
	}
	if task, retryErr := decodeTaskEntry(fixed); retryErr == nil {
		return task, nil
	}
	return nil, err
}

func decodeTaskEntry(entry json.RawMessage) (rpc.TaskInfo, error) {
	var one rpc.TaskList
	if err := json.Unmarshal(slices.Concat([]byte(`{"tasks":[`), entry, []byte(`]}`)), &one); err != nil {
		return nil, err
	}
	return one.Tasks[0], nil
}
