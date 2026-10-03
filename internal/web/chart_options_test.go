package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChartOptionsStandardFamilies(t *testing.T) {
	for _, kind := range []string{"line", "bar", "pie", "scatter", "effectScatter", "radar", "tree", "treemap", "sunburst", "boxplot", "candlestick", "heatmap", "parallel", "lines", "graph", "sankey", "funnel", "gauge", "pictorialBar", "themeRiver", "chord"} {
		t.Run(kind, func(t *testing.T) {
			raw := fmt.Sprintf(`{"animation":true,"tooltip":{"renderMode":"html"},"series":[{"type":%q,"coordinateSystem":"cartesian2d","data":[{"name":"<script>plain text</script>","value":3}],"animation":true}]}`, kind)
			got, err := chartOptions([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			var option map[string]any
			if err := json.Unmarshal(got, &option); err != nil {
				t.Fatal(err)
			}
			series := option["series"].([]any)[0].(map[string]any)
			if option["animation"] != false || series["animation"] != false || option["tooltip"].(map[string]any)["renderMode"] != "richText" {
				t.Fatalf("unsafe rendering defaults: %s", got)
			}
			if kind == "effectScatter" && series["rippleEffect"].(map[string]any)["number"] != float64(0) {
				t.Fatalf("animated ripples: %s", got)
			}
			if kind == "lines" && series["effect"].(map[string]any)["show"] != false {
				t.Fatalf("animated lines: %s", got)
			}
			if kind == "sankey" && series["layoutIterations"] != float64(32) {
				t.Fatalf("sankey default iterations: %s", got)
			}
		})
	}
}

func TestChartOptionsGraphLayoutsAndDatasetText(t *testing.T) {
	for _, tc := range []struct{ nodes, want string }{
		{`[{"id":"a"},{"id":"b"}]`, "circular"},
		{`[{"id":"a","x":0,"y":0},{"id":"b","x":10,"y":20}]`, "none"},
	} {
		got, err := chartOptions([]byte(`{"series":{"type":"graph","data":` + tc.nodes + `}}`))
		if err != nil || !bytes.Contains(got, []byte(`"layout":"`+tc.want+`"`)) {
			t.Fatalf("graph layout = %s, %v", got, err)
		}
	}
	// Source columns and ordinary labels are data. They do not execute code or open links.
	raw := `{"dataset":{"source":[{"image":"https://example.test/a.png","link":"function words are text","value":4}]},"series":{"type":"bar"}}`
	got, err := chartOptions([]byte(raw))
	if err != nil || !bytes.Contains(got, []byte("function words are text")) {
		t.Fatalf("dataset = %s, %v", got, err)
	}
}

func TestChartOptionsTooltipArraysUseRichText(t *testing.T) {
	raw := []byte(`{"tooltip":[{"renderMode":"html","formatter":"<img src=x onerror=alert(1)>"}],"series":[{"type":"pie","tooltip":[{"renderMode":"html"},{"renderMode":"html"}],"data":[{"value":1,"tooltip":{"renderMode":"html"}}]}]}`)
	got, err := chartOptions(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte(`"renderMode":"html"`)) || bytes.Count(got, []byte(`"renderMode":"richText"`)) != 4 || bytes.Count(got, []byte(`"confine":true`)) != 4 {
		t.Fatalf("tooltip arrays retain HTML rendering: %s", got)
	}
}

func TestChartOptionsAcceptsBoundedIntegerCounts(t *testing.T) {
	for _, repeat := range []string{"false", "0", "1000"} {
		raw := `{"xAxis":{"splitNumber":1000},"series":[{"type":"pictorialBar","symbolRepeat":` + repeat + `},{"type":"sankey","layoutIterations":128}]}`
		if _, err := chartOptions([]byte(raw)); err != nil {
			t.Fatalf("bounded counts with symbolRepeat=%s: %v", repeat, err)
		}
	}
}

func TestChartOptionsRefusesUnsafeAndUnboundedInput(t *testing.T) {
	valid := `"series":[{"type":"pie","data":[1]}]`
	for _, tc := range []struct{ raw, want string }{
		{`null`, "one JSON object"},
		{`[]`, "one JSON object"},
		{`{"series":function(){}}`, "one JSON object"},
		{`{` + valid + `} {}`, "one JSON object"},
		{`{}`, "1 to 32 series"},
		{`{"series":[{"type":"custom"}]}`, "unsupported chart series"},
		{`{"series":[{"type":"map"}]}`, "unsupported chart series"},
		{`{"series":[{"type":"bar3D"}]}`, "unsupported chart series"},
		{`{"series":[{"type":"lines"}]}`, "explicit cartesian2d or polar"},
		{`{"series":[{"type":"graph","layout":"force"}]}`, "force layouts"},
		{`{"series":[{"type":"graph","layout":"none","data":[{"x":1}]}]}`, "finite x and y"},
		{`{"series":[{"type":"pie","symbol":"image://https://example.test/image"}]}`, "image symbols"},
		{`{"series":[{"type":"line","markLine":{"symbol":["none","image://https://example.test/image"]}}]}`, "image symbols"},
		{`{"title":{"link":"javascript:alert(1)"},` + valid + `}`, "open links"},
		{`{"tooltip":{"textStyle":{"rich":{"a":{"backgroundColor":{"image":"https://example.test/image"}}}}},` + valid + `}`, "load images"},
		{`{"tooltip":["html"],` + valid + `}`, "object or an array of objects"},
		{`{"series":[{"type":"pie","tooltip":[null]}]}`, "object or an array of objects"},
		{`{"toolbox":{"feature":{"dataView":{}}},` + valid + `}`, "toolbox"},
		{`{"graphic":[],` + valid + `}`, "graphic"},
		{`{"media":[],` + valid + `}`, "media"},
		{`{"dataset":{"transform":{"type":"filter"}},` + valid + `}`, "transform"},
		{`{"__proto__":{},` + valid + `}`, "reserved object key"},
		{`{"xAxis":{"jitter":1},` + valid + `}`, "random axis jitter"},
		{`{"xAxis":{"breaks":[{"start":1,"end":2}]},` + valid + `}`, "breaks"},
		{`{"series":[{"type":"pictorialBar","symbolRepeat":1001}]}`, "between 0 and 1000"},
		{`{"series":[{"type":"pictorialBar","symbolRepeat":"1000000000"}]}`, "explicit bounded integer count"},
		{`{"series":[{"type":"pictorialBar","symbolRepeat":true,"symbolSize":0.000001}]}`, "automatic repetition"},
		{`{"series":[{"type":"pictorialBar","symbolRepeat":"fixed","symbolMargin":-100}]}`, "automatic repetition"},
		{`{"series":[{"type":"pictorialBar","symbolRepeat":1.5}]}`, "integer between 0 and 1000"},
		{`{"series":[{"type":"pictorialBar","symbolRepeat":1e308}]}`, "integer between 0 and 1000"},
		{`{"xAxis":{"splitNumber":"1000000000"},` + valid + `}`, "explicit bounded integer count"},
		{`{"xAxis":{"splitNumber":1.5},` + valid + `}`, "integer between 0 and 1000"},
		{`{"xAxis":{"splitNumber":1e308},` + valid + `}`, "integer between 0 and 1000"},
		{`{"series":[{"type":"sankey","layoutIterations":1e308}]}`, "integer between 0 and 128"},
		{`{"series":[{"type":"sankey","layoutIterations":129}]}`, "integer between 0 and 128"},
		{`{"series":[{"type":"sankey","layoutIterations":-1}]}`, "integer between 0 and 128"},
		{`{"series":[{"type":"sankey","layoutIterations":1.5}]}`, "integer between 0 and 128"},
		{`{"series":[{"type":"pie","data":[1e999]}]}`, "finite"},
		{`{"series":[` + strings.Repeat(`{"type":"pie"},`, maxChartOptionSeries) + `{"type":"pie"}]}`, "1 to 32 series"},
		{`{"series":[{"type":"tree","data":` + strings.Repeat(`[`, maxChartOptionDepth) + `0` + strings.Repeat(`]`, maxChartOptionDepth) + `}]}`, "nested levels"},
		{`{"series":[{"type":"bar","data":[` + strings.Repeat(`0,`, maxChartOptionValues) + `0]}]}`, "JSON values"},
		{`{"title":{"text":"` + strings.Repeat("x", maxChartOutput) + `"},` + valid + `}`, "exceeds 1024 KiB"},
	} {
		if _, err := chartOptions([]byte(tc.raw)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("options %.100s: %v; want %q", tc.raw, err, tc.want)
		}
	}
}

func TestEChartsSafeModeInlineAndRoutes(t *testing.T) {
	ts := newTestServer(t, ServerConfig{Assets: frameAssets()})
	task, conv, dir := chartTask(t, ts.m, ts.prov, "safe")
	marker := filepath.Join(dir, "command-ran")
	res := chartCall(t, conv, "refused", fmt.Sprintf(`{"title":"Pie","kind":"echarts","command":"touch %s","format":"json"}`, marker))
	if !res.Failed || !strings.Contains(res.Text, "Safe mode") {
		t.Fatalf("safe command = %+v", res)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("command ran in Safe mode")
	}
	res = chartCall(t, conv, "inline", `{"title":"Pie","kind":"echarts","options":{"series":[{"type":"pie","data":[{"name":"private chart data","value":17}]}]}}`)
	if res.Failed || strings.Contains(res.Text, "private chart data") {
		t.Fatalf("inline options = %+v", res)
	}
	path := "/api/sessions/" + task.ID + "/chart?call=inline"
	if w := ts.do(http.MethodGet, path, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated chart = %d", w.Code)
	}
	w := ts.do(http.MethodGet, path, "", withCookie(ts))
	var got Chart
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || got.Kind != "echarts" || len(got.Options) == 0 || got.Labels == nil || got.Series == nil {
		t.Fatalf("chart route = %d %s, %v", w.Code, w.Body, err)
	}
	w = ts.do(http.MethodPost, "/api/sessions/"+task.ID+"/chart/pin", `{"call_id":"inline"}`, withCookie(ts))
	var pin PinnedChart
	if err := json.Unmarshal(w.Body.Bytes(), &pin); err != nil || w.Code != http.StatusOK || !bytes.Equal(pin.Options, got.Options) {
		t.Fatalf("pin route = %d %s, %v", w.Code, w.Body, err)
	}
	if _, err := ts.m.RefreshChart(context.Background(), task.ProjectID, pin.ID, false); statusOf(err) != http.StatusConflict {
		t.Fatalf("snapshot refresh = %v", err)
	}
}

func TestEChartsCommandRefreshKeepsOptionsAndSkipsModel(t *testing.T) {
	m, prov, st := newTestManager(t)
	task, conv, dir := chartTask(t, m, prov, "yolo")
	file := filepath.Join(dir, "options.json")
	writeFile(t, file, `{"series":[{"type":"pie","data":[{"name":"initial","value":3}]}]}`)
	res := chartCall(t, conv, "command", `{"title":"Pie","kind":"echarts","command":"cat options.json","format":"json"}`)
	if res.Failed || strings.Contains(res.Text, "initial") {
		t.Fatalf("command options = %+v", res)
	}
	sends := len(conv.Sends())
	pin, err := m.PinChart(task.ID, "command")
	if err != nil || len(pin.Options) == 0 || pin.Command != "cat options.json" {
		t.Fatalf("pin = %+v, %v", pin, err)
	}
	writeFile(t, file, `{"series":[{"type":"pie","data":[{"name":"refreshed","value":7}]}]}`)
	m.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	got, err := m.RefreshChart(context.Background(), task.ProjectID, pin.ID, false)
	if err != nil || !bytes.Contains(got.Options, []byte("refreshed")) || got.Error != "" {
		t.Fatalf("refresh = %+v, %v", got, err)
	}
	writeFile(t, file, `{"series":[{"type":"graph","layout":"force"}]}`)
	m.now = func() time.Time { return time.Now().Add(4 * time.Minute) }
	failed, err := m.RefreshChart(context.Background(), task.ProjectID, pin.ID, false)
	if err != nil || !strings.Contains(failed.Error, "force layouts") || !bytes.Equal(failed.Options, got.Options) {
		t.Fatalf("failed refresh = %+v, %v", failed, err)
	}
	if len(conv.Sends()) != sends {
		t.Fatal("refresh reached the model")
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, err := startManager(t, st, prov).PinnedCharts(task.ProjectID)
	if err != nil || len(list) != 1 || !bytes.Equal(list[0].Options, got.Options) || list[0].Error == "" {
		t.Fatalf("restored pin = %+v, %v", list, err)
	}
}

func TestEChartsArgumentForms(t *testing.T) {
	for _, tc := range []struct{ args, want string }{
		{`{"title":"t","kind":"echarts"}`, "option object"},
		{`{"title":"t","kind":"echarts","options":{},"data":[]}`, "not data rows"},
		{`{"title":"t","kind":"echarts","options":{},"command":"cat options.json","format":"json"}`, "not both"},
		{`{"title":"t","kind":"echarts","options":{},"x":"x"}`, "do not pass x"},
		{`{"title":"t","kind":"echarts","command":"echo","format":"csv"}`, "format must be json"},
		{`{"title":"t","kind":"bar","options":{}}`, "requires kind echarts"},
	} {
		if _, _, err := chartFromArgs([]byte(tc.args)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("args %s: %v; want %q", tc.args, err, tc.want)
		}
	}
}
