package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// uam_chart is the host tool through which a Task shows the owner a line or
// bar chart (docs/web.md "Charts"). The rows come inline or from a shell
// command uam runs in the Task's directory, so they need not pass through
// the model; the model gets a short summary. The chart, rows included, is
// kept with the Task and the browser draws it. Pinning it to the Project
// (chart_pins.go) approves its command for Refresh.

const (
	chartToolName  = "uam_chart"
	maxChartRows   = 500
	maxChartSeries = 4
	// Text limits, in runes; maxChartCommand is in bytes.
	maxChartTitle   = 120
	maxChartLabel   = 60
	maxChartField   = 64
	maxChartValue   = 80
	maxChartCommand = 4000
	// chartTimeout bounds one command run, maxChartOutput what it may print
	// and chartErrTail how much of its error output a failure quotes.
	chartTimeout   = 30 * time.Second
	maxChartOutput = 1 << 20
	chartErrTail   = 1 << 10
	// chartsDir holds a Task's charts in its upload directory, and the
	// pinned charts' rows beside sessions.json.
	chartsDir = "charts"
)

var chartTool = agentapi.HostTool{
	Name: chartToolName,
	Description: "Show the owner a line or bar chart in this conversation; their browser draws it. " +
		"Prefer `command`, even when the rows need computing: uam runs it in the task's directory and reads its CSV or JSON output itself, " +
		"so the rows never pass through you, and you get back only a short summary (row count, ranges). Pass the rows as `data` only when you have a few at hand. " +
		fmt.Sprintf("At most %d rows and %d series; x values must be unique, y values numbers. ", maxChartRows, maxChartSeries) +
		"If uam refuses the command because the task is in Safe mode, run it with your shell tool and pass its rows as `data`. " +
		"The owner can pin the chart to the project and refresh it later, which re-runs the command without you.",
	Parameters: toolSchema([]string{"title", "kind", "x", "y"}, map[string]any{
		"title":   stringProp(fmt.Sprintf("What the chart shows, up to %d characters, e.g. \"Commits per day, September\".", maxChartTitle)),
		"kind":    enumProp("line for a trend over an ordered x, such as days; bar to compare categories.", "line", "bar"),
		"x_label": stringProp(fmt.Sprintf("The x axis title, up to %d characters.", maxChartLabel)),
		"y_label": stringProp(fmt.Sprintf("The y axis title, up to %d characters.", maxChartLabel)),
		"x":       stringProp("The field holding each row's x value: a column of the CSV header, or a key of each JSON object or data row. Rows keep their order."),
		"y": listProp(fmt.Sprintf("The fields holding the numbers, one series each, 1 to %d.", maxChartSeries),
			map[string]any{"type": "string"}),
		"command": stringProp(fmt.Sprintf("A shell command, such as a pipeline or a short script, run in the task's directory with a %s limit and %d KiB of output. "+
			"It prints CSV with a header row, or JSON: an array of objects or one object per line. It must not depend on files you made, since a refresh runs it again later.", chartTimeout, maxChartOutput>>10)),
		"format": enumProp("The command's output: csv or json. Required with command.", "csv", "json"),
		"data":   listProp("Rows instead of a command: objects holding the x and y fields.", map[string]any{"type": "object"}),
	}),
}

// ChartSpec is what a chart shows and where its rows come from: Command,
// when set, printed them in Format; X and Y name the fields read.
type ChartSpec struct {
	Title   string   `json:"title"`
	Kind    string   `json:"kind"`
	XLabel  string   `json:"x_label,omitempty"`
	YLabel  string   `json:"y_label,omitempty"`
	Command string   `json:"command,omitempty"`
	Format  string   `json:"format,omitempty"`
	X       string   `json:"x"`
	Y       []string `json:"y"`
}

// ChartSeries is one y field's values, one per label.
type ChartSeries struct {
	Name   string    `json:"name"`
	Values []float64 `json:"values"`
}

// ChartData is a chart's rows, column-wise, and when they were read.
type ChartData struct {
	Labels []string      `json:"labels"`
	Series []ChartSeries `json:"series"`
	At     time.Time     `json:"at"`
}

// Chart is a chart a Task drew. PinnedID is the Project's pinned chart made
// from it, if any.
type Chart struct {
	ChartSpec
	ChartData
	PinnedID string `json:"pinned_id,omitempty"`
}

type chartArgs struct {
	Title   string           `json:"title"`
	Kind    string           `json:"kind"`
	XLabel  string           `json:"x_label"`
	YLabel  string           `json:"y_label"`
	X       string           `json:"x"`
	Y       fieldList        `json:"y"`
	Command string           `json:"command"`
	Format  string           `json:"format"`
	Data    []map[string]any `json:"data"`
}

// fieldList is a list of field names; a model that passes one name as a
// string means a list of it.
type fieldList []string

func (f *fieldList) UnmarshalJSON(data []byte) error {
	var one string
	if json.Unmarshal(data, &one) == nil {
		*f = fieldList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return errors.New("y must be a list of field names")
	}
	*f = many
	return nil
}

// check refuses a spec a chart cannot be drawn or re-run from.
func (c ChartSpec) check() error {
	text := func(name, v string, limit int, required bool) error {
		switch {
		case required && strings.TrimSpace(v) == "":
			return fmt.Errorf("%s is required", name)
		case utf8.RuneCountInString(v) > limit:
			return fmt.Errorf("%s is longer than %d characters", name, limit)
		case !utf8.ValidString(v) || strings.ContainsFunc(v, unicode.IsControl):
			return fmt.Errorf("%s has a control character", name)
		}
		return nil
	}
	errs := []error{text("title", c.Title, maxChartTitle, true), text("x_label", c.XLabel, maxChartLabel, false),
		text("y_label", c.YLabel, maxChartLabel, false), text("x", c.X, maxChartField, true)}
	if c.Kind != "line" && c.Kind != "bar" {
		errs = append(errs, errors.New("kind must be line or bar"))
	}
	switch {
	case len(c.Y) == 0:
		errs = append(errs, errors.New("y needs at least one field"))
	case len(c.Y) > maxChartSeries:
		errs = append(errs, fmt.Errorf("y has %d fields; a chart shows at most %d series", len(c.Y), maxChartSeries))
	}
	for i, f := range c.Y {
		errs = append(errs, text("y", f, maxChartField, true))
		if f == c.X || slices.Contains(c.Y[:i], f) {
			errs = append(errs, fmt.Errorf("y field %q is repeated or is the x field", f))
		}
	}
	if c.Command != "" {
		switch {
		case len(c.Command) > maxChartCommand:
			errs = append(errs, fmt.Errorf("command is longer than %d bytes", maxChartCommand))
		case !utf8.ValidString(c.Command) || strings.ContainsFunc(c.Command, func(r rune) bool { return unicode.IsControl(r) && r != '\t' && r != '\n' }):
			errs = append(errs, errors.New("command has a control character other than a newline or a tab"))
		}
		if c.Format != "csv" && c.Format != "json" {
			errs = append(errs, errors.New("format must be csv or json with a command"))
		}
	} else if c.Format != "" {
		errs = append(errs, errors.New("format applies only to a command"))
	}
	return errors.Join(errs...)
}

// chartFromArgs checks a uam_chart call's arguments and returns its spec
// and inline rows.
func chartFromArgs(raw json.RawMessage) (ChartSpec, []map[string]any, error) {
	var in chartArgs
	if err := decodeToolArgs(raw, &in); err != nil {
		return ChartSpec{}, nil, err
	}
	spec := ChartSpec{Title: strings.TrimSpace(in.Title), Kind: in.Kind, XLabel: strings.TrimSpace(in.XLabel), YLabel: strings.TrimSpace(in.YLabel),
		Command: strings.TrimSpace(in.Command), Format: in.Format, X: in.X, Y: in.Y}
	switch {
	case spec.Command != "" && in.Data != nil:
		return ChartSpec{}, nil, errors.New("pass either command or data, not both")
	case spec.Command == "" && len(in.Data) == 0:
		return ChartSpec{}, nil, errors.New("pass a command that prints the rows, or the rows as data")
	}
	if err := spec.check(); err != nil {
		return ChartSpec{}, nil, err
	}
	return spec, in.Data, nil
}

// parseChartOutput reads a command's output as rows: CSV with a header row,
// or JSON, an array of objects or a stream of them. fields lists the
// fields the rows have, for a refusal naming a missing one.
func parseChartOutput(format string, out []byte) (rows []map[string]any, fields []string, err error) {
	out = bytes.TrimPrefix(out, []byte("\xef\xbb\xbf"))
	if format == "csv" {
		r := csv.NewReader(bytes.NewReader(out))
		r.TrimLeadingSpace = true
		r.LazyQuotes = true
		records, err := r.ReadAll()
		if err != nil {
			return nil, nil, fmt.Errorf("the output is not CSV: %w", err)
		}
		if len(records) == 0 {
			return nil, nil, errors.New("the output is empty; it needs a header row and at least one row")
		}
		fields = make([]string, len(records[0]))
		for i, f := range records[0] {
			fields[i] = strings.TrimSpace(f)
		}
		for _, rec := range records[1:] {
			row := make(map[string]any, len(rec))
			for i, v := range rec {
				row[fields[i]] = v
			}
			rows = append(rows, row)
		}
		return rows, fields, nil
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	trimmed := bytes.TrimSpace(out)
	if bytes.HasPrefix(trimmed, []byte("[")) {
		if err := dec.Decode(&rows); err != nil {
			return nil, nil, fmt.Errorf("the output is not a JSON array of objects: %w", err)
		}
	} else {
		for {
			var row map[string]any
			err := dec.Decode(&row)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, nil, fmt.Errorf("the output is not JSON objects, one per line: %w", err)
			}
			rows = append(rows, row)
		}
	}
	if len(rows) > 0 {
		for f := range rows[0] {
			fields = append(fields, f)
		}
		slices.Sort(fields)
	}
	return rows, fields, nil
}

// chartRows turns rows into a chart's labels and series, in row order. It
// refuses too many rows, a missing field, a repeated x value and a y value
// that is not a finite number.
func chartRows(spec ChartSpec, rows []map[string]any, fields []string) ([]string, []ChartSeries, error) {
	switch {
	case len(rows) == 0:
		return nil, nil, errors.New("there are no rows to chart")
	case len(rows) > maxChartRows:
		return nil, nil, fmt.Errorf("there are %d rows; a chart takes at most %d. Aggregate them, or keep the last ones (for example `| tail -n %d`)", len(rows), maxChartRows, maxChartRows)
	}
	if fields != nil {
		for _, f := range append([]string{spec.X}, spec.Y...) {
			if !slices.Contains(fields, f) {
				return nil, nil, fmt.Errorf("the rows have no field %q; their fields are: %s", f, strings.Join(fields, ", "))
			}
		}
	}
	labels := make([]string, 0, len(rows))
	series := make([]ChartSeries, len(spec.Y))
	for i, f := range spec.Y {
		series[i] = ChartSeries{Name: f, Values: make([]float64, 0, len(rows))}
	}
	seen := make(map[string]int, len(rows))
	for n, row := range rows {
		x, err := chartLabel(row[spec.X])
		if err != nil {
			return nil, nil, fmt.Errorf("row %d: %s %w", n+1, spec.X, err)
		}
		if first, ok := seen[x]; ok {
			return nil, nil, fmt.Errorf("rows %d and %d have the same x value %q; x values must be unique, so aggregate the rows first", first, n+1, x)
		}
		seen[x] = n + 1
		labels = append(labels, x)
		for i, f := range spec.Y {
			v, err := chartValue(row[f])
			if err != nil {
				return nil, nil, fmt.Errorf("row %d: %s %w", n+1, f, err)
			}
			series[i].Values = append(series[i].Values, v)
		}
	}
	return labels, series, nil
}

func chartLabel(v any) (string, error) {
	var s string
	switch v := v.(type) {
	case string:
		s = strings.TrimSpace(v)
	case float64:
		s = strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		s = strconv.FormatBool(v)
	case nil:
		return "", errors.New("is missing")
	default:
		return "", errors.New("must be a string or a number")
	}
	switch {
	case s == "":
		return "", errors.New("is empty")
	case utf8.RuneCountInString(s) > maxChartValue:
		return "", fmt.Errorf("is longer than %d characters", maxChartValue)
	case !utf8.ValidString(s) || strings.ContainsFunc(s, unicode.IsControl):
		return "", errors.New("has a control character")
	}
	return s, nil
}

func chartValue(v any) (float64, error) {
	var f float64
	switch v := v.(type) {
	case float64:
		f = v
	case string:
		var err error
		if f, err = strconv.ParseFloat(strings.TrimSpace(v), 64); err != nil {
			return 0, fmt.Errorf("is %q, not a number", clipRunes(v, 20))
		}
	case nil:
		return 0, errors.New("is missing")
	default:
		return 0, errors.New("must be a number")
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, errors.New("must be a finite number")
	}
	return f, nil
}

// chartEnv is the environment chart commands run with: the service's, less
// uam's own variables, with git and pagers kept from prompting.
func chartEnv() []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "UAM_") })
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "PAGER=cat", "NO_COLOR=1", "TERM=dumb", "GH_PROMPT_DISABLED=1")
}

// capWriter keeps up to limit bytes and cancels the run past them.
type capWriter struct {
	buf    bytes.Buffer
	limit  int
	over   bool
	cancel context.CancelFunc
}

func (w *capWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.limit {
		w.over = true
		w.cancel()
		return 0, errors.New("output limit reached")
	}
	return w.buf.Write(p)
}

// runChartCommand runs command in a shell in dir, in its own process group
// with no input, and returns what it printed. A non-zero exit, the time
// limit and too much output are errors that say how to fix them.
func runChartCommand(ctx context.Context, dir, command string) ([]byte, error) {
	shell, err := terminalShell()
	if err != nil {
		return nil, fmt.Errorf("no shell found: %w", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, chartTimeout)
	defer cancel()
	c := exec.CommandContext(runCtx, shell, "-c", command) // #nosec G204 G702 -- an agent's command in yolo mode, or one the owner approved by pinning it, in the service user's shell.
	c.Dir = dir
	c.Env = chartEnv()
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = time.Second
	stdout := &capWriter{limit: maxChartOutput, cancel: cancel}
	stderr := &tailBuffer{limit: chartErrTail}
	c.Stdout, c.Stderr = stdout, stderr
	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("the shell did not start: %w", err)
	}
	waitErr := c.Wait()
	_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	switch {
	case ctx.Err() != nil:
		return nil, context.Cause(ctx)
	case stdout.over:
		return nil, fmt.Errorf("the command printed more than %d KiB; aggregate or limit its rows", maxChartOutput>>10)
	case runCtx.Err() != nil:
		return nil, fmt.Errorf("the command did not finish within %s", chartTimeout)
	case waitErr != nil:
		msg := fmt.Sprintf("the command failed with exit status %d", exitCode(c.ProcessState))
		if tail := strings.TrimSpace(stderr.String()); tail != "" {
			msg += ": " + tail
		}
		return nil, errors.New(msg)
	}
	return stdout.buf.Bytes(), nil
}

// readChart reads the rows of spec: from its command, run in dir, or else
// from inline. It returns them with how long the command took.
func readChart(ctx context.Context, spec ChartSpec, dir string, inline []map[string]any) (ChartData, time.Duration, error) {
	rows, fields := inline, []string(nil)
	var took time.Duration
	if spec.Command != "" {
		start := time.Now()
		out, err := runChartCommand(ctx, dir, spec.Command)
		took = time.Since(start)
		if err != nil {
			return ChartData{}, took, err
		}
		if rows, fields, err = parseChartOutput(spec.Format, out); err != nil {
			return ChartData{}, took, err
		}
	}
	labels, series, err := chartRows(spec, rows, fields)
	if err != nil {
		return ChartData{}, took, err
	}
	return ChartData{Labels: labels, Series: series, At: time.Now().UTC()}, took, nil
}

// chartSummary is what the model gets back: the rows' extent, never the
// rows.
func chartSummary(spec ChartSpec, data ChartData, took time.Duration) string {
	var b strings.Builder
	noun := "rows"
	if len(data.Labels) == 1 {
		noun = "row"
	}
	fmt.Fprintf(&b, "Charted %q (%s) for the owner from %d %s", spec.Title, spec.Kind, len(data.Labels), noun)
	if spec.Command != "" {
		fmt.Fprintf(&b, ", read from the command in %.1fs", took.Seconds())
	}
	if n := len(data.Labels); n > 1 {
		fmt.Fprintf(&b, ". %s: %s … %s.", spec.X, data.Labels[0], data.Labels[n-1])
	} else {
		fmt.Fprintf(&b, ". %s: %s.", spec.X, data.Labels[0])
	}
	for _, s := range data.Series {
		lo, hi, sum := s.Values[0], s.Values[0], 0.0
		for _, v := range s.Values {
			lo, hi, sum = min(lo, v), max(hi, v), sum+v
		}
		fmt.Fprintf(&b, " %s: min %s, max %s, total %s, last %s.", s.Name, chartNumber(lo), chartNumber(hi), chartNumber(sum), chartNumber(s.Values[len(s.Values)-1]))
	}
	b.WriteString(" The chart shows in the conversation with a table of the rows; do not repeat them.")
	return b.String()
}

func chartNumber(v float64) string { return strconv.FormatFloat(v, 'g', 6, 64) }

// chartFile is where a Task keeps the chart of one uam_chart call: in its
// upload directory, so it goes with the Task, named by the call ID's digest.
func (m *Manager) chartFile(taskID, callID string) string {
	sum := sha256.Sum256([]byte(callID))
	return filepath.Join(m.taskUploadDir(taskID), chartsDir, hex.EncodeToString(sum[:16])+".json")
}

// writePrivateJSON replaces path with v's JSON, owner-only, creating its
// directories under root.
func writePrivateJSON(root, path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil {
		return err
	}
	dir := root
	for _, part := range append([]string{"."}, strings.Split(rel, string(filepath.Separator))...) {
		dir = filepath.Join(dir, part)
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
			return fmt.Errorf("%s is not a directory", dir)
		}
	}
	return writeFileAtomic(path, ".chart-*", data)
}

// chartCall runs a uam_chart call of the Task taskID.
func (m *Manager) chartCall(ctx context.Context, taskID string, call agentapi.HostToolCall) agentapi.HostToolResult {
	text, err := m.drawChart(ctx, taskID, call)
	if err != nil {
		return agentapi.HostToolResult{Text: err.Error(), Failed: true}
	}
	return agentapi.HostToolResult{Text: text}
}

func (m *Manager) drawChart(ctx context.Context, taskID string, call agentapi.HostToolCall) (string, error) {
	if call.TaskID != taskID {
		return "", errors.New("the call does not belong to this task")
	}
	if call.CallID == "" || len(call.CallID) > maxToolCallID {
		return "", errors.New("the call has no usable ID")
	}
	spec, inline, err := chartFromArgs(call.Arguments)
	if err != nil {
		return "", err
	}
	ctx, end, err := m.startCall(ctx, taskID)
	if err != nil {
		return "", err
	}
	defer end()
	m.mu.Lock()
	s := m.sessions[taskID]
	var mode store.Mode
	var dir string
	if s != nil {
		mode, dir = s.mode, s.workdir
	}
	m.mu.Unlock()
	if s == nil {
		return "", newError(http.StatusNotFound, msgSessionNotFound)
	}
	// A command runs without a permission request, so only where the owner
	// allows every request without asking.
	if spec.Command != "" && mode != store.ModeYolo {
		return "", errors.New("this task is in Safe mode, so uam does not run commands for it. Run the command with your shell tool, which asks the owner, and pass its rows as data")
	}
	data, took, err := readChart(ctx, spec, dir, inline)
	if err != nil {
		return "", err
	}
	if err := writePrivateJSON(m.uploadRoot(), m.chartFile(taskID, call.CallID), Chart{ChartSpec: spec, ChartData: data}); err != nil {
		log.Warn("store a chart failed", "session", taskID, "error", err)
		return "", errors.New("uam could not store the chart; try again")
	}
	m.mu.Lock()
	removed := s.removed
	m.mu.Unlock()
	if removed {
		removeUploads(m.taskUploadDir(taskID))
		return "", newError(http.StatusNotFound, msgSessionNotFound)
	}
	return chartSummary(spec, data, took), nil
}

// TaskChart returns the chart the Task id drew with the call callID, with
// the Project's pin of it, if any.
func (m *Manager) TaskChart(id, callID string) (Chart, error) {
	s, err := m.lookup(id)
	if err != nil {
		return Chart{}, err
	}
	if callID == "" || len(callID) > maxToolCallID {
		return Chart{}, newError(http.StatusNotFound, "chart not found")
	}
	data, err := os.ReadFile(m.chartFile(id, callID))
	var c Chart
	if err != nil || json.Unmarshal(data, &c) != nil {
		return Chart{}, newError(http.StatusNotFound, "chart not found")
	}
	m.mu.Lock()
	project := s.projectID
	m.mu.Unlock()
	if pins, err := m.storedPins(project); err == nil {
		for _, p := range pins {
			if p.TaskID == id && p.CallID == callID {
				c.PinnedID = p.ID
			}
		}
	}
	return c, nil
}

// chartRuns bounds pinned chart refreshes: one run per chart at a time,
// and when each last ran.
type chartRuns struct {
	mu      sync.Mutex
	running map[string]bool
	last    map[string]time.Time
}
