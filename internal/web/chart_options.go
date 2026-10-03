package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
)

const (
	maxChartOptionDepth  = 32
	maxChartOptionValues = 20_000
	maxChartOptionSeries = 32
)

// These are the standard 2D series bundled by the web client's ECharts runtime.
// Maps need registered geographic assets; custom series need executable code.
var chartOptionSeries = []string{
	"line", "bar", "pie", "scatter", "effectScatter", "radar", "tree", "treemap", "sunburst",
	"boxplot", "candlestick", "heatmap", "parallel", "lines", "graph", "sankey", "funnel",
	"gauge", "pictorialBar", "themeRiver", "chord",
}

// chartOptions accepts JSON data, not a program or an HTML document. ECharts owns
// its chart schema; this checks the resource and execution boundaries we expose,
// and removes animation so refreshing the same data settles on the same image.
func chartOptions(raw []byte) (json.RawMessage, error) {
	if len(raw) > maxChartOutput {
		return nil, fmt.Errorf("options exceeds %d KiB", maxChartOutput>>10)
	}
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))))
	dec.UseNumber()
	var option map[string]any
	if err := dec.Decode(&option); err != nil || option == nil {
		return nil, errors.New("options must be one JSON object")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("options must be one JSON object")
	}
	values := 0
	if err := checkChartOptionValue(option, 0, false, &values); err != nil {
		return nil, err
	}
	series, ok := option["series"].([]any)
	if one, single := option["series"].(map[string]any); single {
		series, ok = []any{one}, true
	}
	if !ok || len(series) == 0 || len(series) > maxChartOptionSeries {
		return nil, fmt.Errorf("options.series needs 1 to %d series", maxChartOptionSeries)
	}
	for _, value := range series {
		s, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("each options.series entry must be an object")
		}
		kind, _ := s["type"].(string)
		if !slices.Contains(chartOptionSeries, kind) {
			return nil, fmt.Errorf("unsupported chart series %q; use a standard 2D series without maps or custom code", kind)
		}
		if kind == "graph" {
			if err := fixedChartGraph(s); err != nil {
				return nil, err
			}
		}
		if kind == "lines" && s["coordinateSystem"] != "cartesian2d" && s["coordinateSystem"] != "polar" {
			return nil, errors.New("lines needs an explicit cartesian2d or polar coordinateSystem; geographic maps are unavailable")
		}
		s["animation"] = false
		if kind == "effectScatter" {
			s["rippleEffect"] = map[string]any{"number": 0}
		}
		if kind == "lines" {
			s["effect"] = map[string]any{"show": false}
		}
		if kind == "sankey" {
			if _, set := s["layoutIterations"]; !set {
				s["layoutIterations"] = 32
			}
		}
	}
	option["series"] = series
	option["animation"] = false
	return json.Marshal(option)
}

func checkChartOptionValue(value any, depth int, source bool, count *int) error {
	*count++
	if depth > maxChartOptionDepth || *count > maxChartOptionValues {
		return fmt.Errorf("options takes at most %d nested levels and %d JSON values", maxChartOptionDepth, maxChartOptionValues)
	}
	switch v := value.(type) {
	case json.Number:
		if n, err := v.Float64(); err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			return errors.New("options numbers must be finite")
		}
	case []any:
		for _, item := range v {
			if err := checkChartOptionValue(item, depth+1, source, count); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, item := range v {
			if key == "__proto__" || key == "constructor" || key == "prototype" {
				return errors.New("options contains a reserved object key")
			}
			// Dataset source rows are plain values, not ECharts options. Column
			// names such as "link" or "image" must remain usable as data.
			if !source {
				if err := checkChartOptionField(key, item); err != nil {
					return err
				}
			}
			if err := checkChartOptionValue(item, depth+1, source || key == "source", count); err != nil {
				return err
			}
			if !source {
				switch key {
				case "animation":
					v[key] = false
				case "tooltip", "dataZoom":
					entries := []any{item}
					if array, ok := item.([]any); ok {
						entries = array
					}
					for _, value := range entries {
						entry, ok := value.(map[string]any)
						if !ok {
							return fmt.Errorf("options.%s must be an object or an array of objects", key)
						}
						if key == "tooltip" {
							entry["renderMode"], entry["confine"] = "richText", true
						}
					}
				case "rippleEffect":
					v[key] = map[string]any{"number": 0}
				case "effect":
					v[key] = map[string]any{"show": false}
				}
			}
		}
	}
	return nil
}

func checkChartOptionField(key string, value any) error {
	switch key {
	case "toolbox", "graphic", "geo", "map", "mapbox", "bmap", "amap", "globe",
		"media", "baseOption", "options", "transform", "breaks", "renderItem":
		return fmt.Errorf("options.%s is not supported", key)
	case "image", "link", "sublink":
		return fmt.Errorf("options.%s cannot load images or open links", key)
	case "symbol", "icon":
		if symbols, ok := value.([]any); ok {
			for _, symbol := range symbols {
				if err := checkChartOptionField(key, symbol); err != nil {
					return err
				}
			}
		}
		if text, ok := value.(string); ok && strings.HasPrefix(strings.ToLower(strings.TrimSpace(text)), "image://") {
			return errors.New("options cannot load image symbols or icons")
		}
	case "coordinateSystem":
		text, ok := value.(string)
		if !ok || !slices.Contains([]string{"cartesian2d", "polar", "radar", "singleAxis", "parallel", "calendar", "matrix", "none", "view"}, text) {
			return errors.New("options has an unsupported coordinateSystem; geographic maps are unavailable")
		}
	case "jitter":
		if n, ok := value.(json.Number); !ok || n.String() != "0" {
			return errors.New("options cannot use random axis jitter")
		}
	case "symbolRepeat", "splitNumber", "layoutIterations":
		if repeat, ok := value.(bool); key == "symbolRepeat" && ok && !repeat {
			return nil
		}
		limit := 1000.0
		if key == "layoutIterations" {
			limit = 128
		}
		n, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("options.%s needs an explicit bounded integer count; automatic repetition and numeric strings are not supported", key)
		}
		x, err := n.Float64()
		if err != nil || x < 0 || x > limit || math.Trunc(x) != x {
			return fmt.Errorf("options.%s must be an integer between 0 and %.0f", key, limit)
		}
	}
	return nil
}

func fixedChartGraph(series map[string]any) error {
	nodes, _ := series["data"].([]any)
	if len(nodes) == 0 {
		nodes, _ = series["nodes"].([]any)
	}
	fixed := len(nodes) > 0
	for _, value := range nodes {
		node, ok := value.(map[string]any)
		if !ok {
			fixed = false
			break
		}
		_, x := node["x"].(json.Number)
		_, y := node["y"].(json.Number)
		fixed = fixed && x && y
	}
	layout, _ := series["layout"].(string)
	switch layout {
	case "":
		if fixed {
			series["layout"] = "none"
		} else {
			series["layout"] = "circular"
		}
	case "none":
		if !fixed {
			return errors.New("graph layout none requires finite x and y coordinates on every node")
		}
	case "circular":
	default:
		return errors.New("graph layout must be none with fixed coordinates, or circular; force layouts are not deterministic")
	}
	return nil
}
