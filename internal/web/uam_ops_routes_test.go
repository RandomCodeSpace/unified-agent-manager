package web

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// uamRouteOps maps each owner write route an agent can also take to the uam
// op ("uam <op>") or host tool that does it, scoped to what the agent may
// touch (its own Thread, the Threads it started, its Project's routines).
var uamRouteOps = map[string][]string{
	"POST /api/sessions":                          {createTaskToolName},
	"PATCH /api/sessions/{id}":                    {"uam rename", "uam set_budget"}, // model, effort, mode and agent stay the owner's
	"POST /api/sessions/{id}/settle":              {"uam settle"},
	"POST /api/sessions/{id}/archive":             {"uam archive"},
	"POST /api/sessions/{id}/chart/pin":           {"uam pin_chart"},
	"DELETE /api/projects/{id}/charts/{chart_id}": {"uam unpin_chart"},
	"POST /api/sessions/{id}/prompt":              {sendToolName},
}

// uamRouteExclusions are the owner write routes no agent op takes, each with
// why.
var uamRouteExclusions = map[string]string{
	"/":                           "static UI files, read only",
	"/api/":                       "unknown API paths answer 404",
	connectedProxyRoute:           "reaches a connected instance with the owner's grant",
	"POST /api/routines/{id}/run": "uses the routine's daily run limit, which the scheduled runs need",
	"POST /api/login":             "sign-in",
	"POST /api/logout":            "sign-out",
	"POST /api/viewing":           "the owner's viewing presence",
	"POST /api/fs/dirs":           "makes folders anywhere for the folder picker; agents use their shell",
	"POST /api/push/subscribe":    "browser push subscription",
	"POST /api/push/unsubscribe":  "browser push subscription",
	"POST /api/service/restart":   "restarts uam and every Thread",

	"POST /api/projects":                                        "adding a project is the owner's confirmation (uam_create_task asks for it)",
	"PATCH /api/projects/{id}":                                  "project settings",
	"DELETE /api/projects/{id}":                                 "destroys a project and its Threads",
	"POST /api/projects/{id}/branch-link/detect":                "project settings",
	"POST /api/projects/{id}/previous/{conversation_id}/import": "imports the owner's provider history",
	"POST /api/projects/{id}/routines":                          "routines run unattended on a schedule; the owner sets them",
	"PATCH /api/routines/{id}":                                  "routines run unattended on a schedule; the owner sets them",
	"DELETE /api/routines/{id}":                                 "routines run unattended on a schedule; the owner sets them",
	"POST /api/projects/{id}/charts/{chart_id}/refresh":         "reruns a pinned chart's command without a permission request",
	"POST /api/worktrees/remove":                                "destroys worktrees, which can hold unsaved work",

	"PATCH /api/settings":                                       "settings",
	"POST /api/settings/custom-models/discover":                 "settings, with provider keys",
	"POST /api/configuration/{kind}/draft":                      "agent, skill and instruction files that change what agents can do",
	"POST /api/configuration/skills/list":                       "agent, skill and instruction files that change what agents can do",
	"POST /api/configuration/skills/install":                    "agent, skill and instruction files that change what agents can do",
	"POST /api/configuration/skills/global-disabled":            "agent, skill and instruction files that change what agents can do",
	"PUT /api/configuration/{kind}/{name}":                      "agent, skill and instruction files that change what agents can do",
	"DELETE /api/configuration/{kind}/{name}":                   "agent, skill and instruction files that change what agents can do",
	"POST /api/mcp/servers":                                     "MCP servers, with their secrets",
	"PUT /api/mcp/servers/{name}":                               "MCP servers, with their secrets",
	"PATCH /api/mcp/servers/{name}":                             "MCP servers, with their secrets",
	"DELETE /api/mcp/servers/{name}":                            "MCP servers, with their secrets",
	"POST /api/sessions/{id}/mcp/reconnect":                     "a Thread's MCP servers, which change its tools",
	"POST /api/sessions/{id}/mcp/servers/{name}/enable":         "a Thread's MCP servers, which change its tools",
	"POST /api/sessions/{id}/mcp/servers/{name}/disable":        "a Thread's MCP servers, which change its tools",
	"POST /api/sessions/{id}/mcp/servers/{name}/restart":        "a Thread's MCP servers, which change its tools",
	"POST /api/sessions/{id}/mcp/servers/{name}/sign-in":        "sign-in",
	"POST /api/sessions/{id}/mcp/servers/{name}/sign-in/finish": "sign-in",

	"POST /api/providers/{provider}/account/sign-in":  "sign-in",
	"POST /api/providers/{provider}/account/device":   "sign-in",
	"DELETE /api/providers/{provider}/account/device": "sign-in",
	"DELETE /api/providers/{provider}/account/link":   "sign-in",
	"POST /api/providers/{provider}/account/sign-out": "sign-out",
	"POST /api/providers/{provider}/cli/update":       "installs the provider CLI every Thread runs",
	"POST /api/providers/{provider}/cli/restart":      "restarts the provider CLI every Thread runs",

	"POST /api/connections":                     "connected instances and their grants",
	"PATCH /api/connections/{connection}":       "connected instances and their grants",
	"DELETE /api/connections/{connection}":      "connected instances and their grants",
	"POST /api/federation/pair":                 "connected instances and their grants",
	"DELETE /api/federation/grants/{grant}":     "connected instances and their grants",
	"DELETE /api/federation/grants/self":        "connected instances and their grants",
	"POST /api/connected-notifications/viewing": "the owner's viewing presence",
	"POST /api/message-pairs/{id}/{action}":     "the owner's control of messages between Threads",

	"DELETE /api/sessions/{id}":                                    "destroys a Thread's record",
	"POST /api/sessions/{id}/reopen":                               "brings back a Thread the owner put away",
	"POST /api/sessions/{id}/unarchive":                            "brings back a Thread the owner put away",
	"POST /api/sessions/{id}/close":                                "the owner's stop of a Thread's conversation",
	"POST /api/sessions/{id}/cancel":                               "the owner's stop of a turn",
	"POST /api/sessions/{id}/subagents/{agent_id}/cancel":          "the owner's stop of a subagent",
	"POST /api/sessions/{id}/background-tasks/{task_id}/cancel":    "the owner's stop of a background task",
	"POST /api/sessions/{id}/command":                              "slash commands change the Thread's mode, model and session",
	"POST /api/sessions/{id}/interactions/{iid}":                   "answers a permission request or question to the owner",
	"POST /api/sessions/{id}/surface/action":                       "the owner's answer to a uam_surface card",
	"POST /api/sessions/{id}/queue/resume":                         "the owner's prompt queue",
	"POST /api/sessions/{id}/queue/clear":                          "the owner's prompt queue",
	"DELETE /api/sessions/{id}/queue/{request_id}":                 "the owner's prompt queue",
	"POST /api/sessions/{id}/attachments":                          "uploads",
	"POST /api/sessions/{id}/file-grants":                          "shares files outside sign-in",
	"DELETE /api/sessions/{id}/file-grants/{grant_id}":             "shares files outside sign-in",
	"POST /api/sessions/{id}/files/resolve":                        "a read in a POST body; agents read files directly",
	"POST /api/sessions/{id}/changes/revert":                       "discards work; agents use their own git and edit tools",
	"POST /api/sessions/{id}/git/init":                             "agents use git in their shell",
	"POST /api/sessions/{id}/git/branch":                           "agents use git in their shell",
	"POST /api/sessions/{id}/subagents/{agent_id}/prompt":          "the owner's message to a Thread's subagent",
	"POST /api/sessions/{id}/aside":                                "the owner's side question to a Utility model",
	"POST /api/sessions/{id}/suggestions":                          "the owner's composer suggestions",
	"POST /api/sessions/{id}/rerun":                                "rewrites the owner's conversation",
	"POST /api/sessions/{id}/fork":                                 "rewrites the owner's conversation",
	"POST /api/sessions/{id}/fork/dismiss":                         "rewrites the owner's conversation",
	"POST /api/sessions/{id}/rewind":                               "rewrites the owner's conversation",
	"POST /api/sessions/{id}/rewind/reconcile":                     "rewrites the owner's conversation",
	"POST /api/sessions/{id}/rewind/release":                       "rewrites the owner's conversation",
	"POST /api/sessions/{id}/resend":                               "rewrites the owner's conversation",
	"POST /api/sessions/{id}/gitlab/refresh":                       "asks GitLab with the owner's glab sign-in; the pipeline op reads the cache",
	"POST /api/sessions/{id}/gitlab/investigate":                   "starts a Utility model run the owner pays for",
	"POST /api/sessions/{id}/gitlab/merge-requests/{iid}/comments": "writes to GitLab as the owner; agents use glab in their shell",
	"POST /api/sessions/{id}/gitlab/merge-requests/{iid}/resolve":  "writes to GitLab as the owner; agents use glab in their shell",
	"POST /api/sessions/{id}/gitlab/merge-requests/{iid}/actions":  "writes to GitLab as the owner; agents use glab in their shell",
}

// registeredRoutes reads every route pattern the package's non-test files
// register with a ServeMux's Handle or HandleFunc: string literals, string
// constants, and concatenations of the two.
func registeredRoutes(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	var files []*ast.File
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	consts := map[string]ast.Expr{}
	for _, f := range files {
		for _, decl := range f.Decls {
			if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.CONST {
				for _, spec := range gen.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, name := range vs.Names {
						if i < len(vs.Values) {
							consts[name.Name] = vs.Values[i]
						}
					}
				}
			}
		}
	}
	var str func(ast.Expr) (string, bool)
	str = func(e ast.Expr) (string, bool) {
		switch e := e.(type) {
		case *ast.BasicLit:
			if e.Kind == token.STRING {
				s, err := strconv.Unquote(e.Value)
				return s, err == nil
			}
		case *ast.Ident:
			if v, ok := consts[e.Name]; ok {
				return str(v)
			}
		case *ast.BinaryExpr:
			if e.Op == token.ADD {
				a, ok := str(e.X)
				b, ok2 := str(e.Y)
				return a + b, ok && ok2
			}
		case *ast.ParenExpr:
			return str(e.X)
		}
		return "", false
	}
	var routes []string
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle" {
				return true
			}
			pattern, ok := str(call.Args[0])
			if !ok {
				t.Errorf("%s: route pattern is not a string constant", fset.Position(call.Pos()))
				return true
			}
			routes = append(routes, pattern)
			return true
		})
	}
	return routes
}

// Every owner write route either has an agent op or is excluded with a
// reason, so a new route cannot quietly leave agents behind.
func TestEveryOwnerWriteRouteHasAnAgentOpOrAReason(t *testing.T) {
	routes := registeredRoutes(t)
	if !slices.Contains(routes, "PATCH /api/sessions/{id}") || !slices.Contains(routes, "POST /api/sessions/{id}/chart/pin") || !slices.Contains(routes, connectedProxyRoute) {
		t.Fatalf("route scan missed known routes: %q", routes)
	}
	tools := map[string]bool{}
	for _, tool := range []agentapi.HostTool{createTaskTool, sendTool, chartTool, formTool, surfaceTool, uamTool} {
		tools[tool.Name] = true
	}
	seen := map[string]bool{}
	for _, route := range routes {
		seen[route] = true
		method, _, found := strings.Cut(route, " ")
		if found && (method == "GET" || method == "HEAD") {
			continue
		}
		ops, mapped := uamRouteOps[route]
		reason, excluded := uamRouteExclusions[route]
		switch {
		case mapped && excluded:
			t.Errorf("%s has an agent op and an exclusion", route)
		case !mapped && !excluded:
			t.Errorf("%s is an owner write route with neither a uam op (uamRouteOps) nor a reason in uamRouteExclusions", route)
		case excluded && strings.TrimSpace(reason) == "":
			t.Errorf("%s is excluded without a reason", route)
		}
		for _, op := range ops {
			name, isOp := strings.CutPrefix(op, "uam ")
			if isOp && uamShapes[name] == "" || !isOp && !tools[op] {
				t.Errorf("%s maps to %q, which is no uam op or host tool", route, op)
			}
		}
	}
	for route := range uamRouteOps {
		if !seen[route] {
			t.Errorf("uamRouteOps lists %s, which is not registered", route)
		}
	}
	for route := range uamRouteExclusions {
		if !seen[route] {
			t.Errorf("uamRouteExclusions lists %s, which is not registered", route)
		}
	}
}
