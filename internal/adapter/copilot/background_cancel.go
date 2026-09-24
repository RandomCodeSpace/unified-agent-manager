package copilot

import (
	"context"
	"fmt"
	"slices"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/github/copilot-sdk/go/rpc"
)

func (c *conversation) CancelBackgroundTask(ctx context.Context, id string) (agentapi.BackgroundTasks, error) {
	c.taskRPC.Lock()
	defer c.taskRPC.Unlock()
	if c.isClosed() {
		return agentapi.BackgroundTasks{}, agentapi.ErrClosed
	}
	// Validate the exact typed record before sending the shared tasks.cancel RPC.
	tasks, err := c.sess.ListTasks(ctx)
	if err != nil {
		return c.shellCancelSnapshot(nil, err), fmt.Errorf("read shell tasks: %s", errText(err))
	}
	var target *rpc.TaskShellInfo
	for _, task := range tasks {
		if shell, ok := task.(*rpc.TaskShellInfo); ok && shell.ID == id {
			target = shell
			break
		}
	}
	if target == nil {
		return c.shellCancelSnapshot(tasks, nil), agentapi.ErrBackgroundTaskNotFound
	}
	if target.Status != rpc.TaskStatusRunning {
		return c.shellCancelSnapshot(tasks, nil), agentapi.ErrBackgroundTaskInactive
	}
	accepted, cancelErr := c.sess.CancelSubagent(ctx, id) // SDK tasks.cancel accepts either typed task.
	fresh, readErr := c.sess.ListTasks(ctx)
	snapshot := c.shellCancelSnapshot(fresh, readErr)
	if cancelErr != nil {
		return snapshot, fmt.Errorf("cancel shell task: %s", errText(cancelErr))
	}
	if readErr != nil {
		return snapshot, fmt.Errorf("shell stop requested but refresh failed: %s", errText(readErr))
	}
	if !accepted {
		return snapshot, agentapi.ErrBackgroundTaskInactive
	}
	return snapshot, nil
}

func (c *conversation) shellCancelSnapshot(tasks []rpc.TaskInfo, err error) agentapi.BackgroundTasks {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed && err == nil {
		c.reportEmptyTasks = true
		c.applyShellTasksLocked(tasks)
	}
	out := agentapi.BackgroundTasks{Tasks: []agentapi.BackgroundTask{}}
	if c.backgroundTasks != nil {
		out = *c.backgroundTasks
		out.Tasks = slices.Clone(out.Tasks)
	}
	if err != nil || c.closed {
		out.Known = false
		if !c.closed {
			c.backgroundTasks = &out
			c.emitLocked(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &out})
		}
	}
	return out
}
