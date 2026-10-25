package web

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// draw.io: the mxGraph XML model of a .drawio file, and the check of a
// draw.io origin before it may be enabled in Settings (docs/web.md
// "Drawings").

// mxItem is one element under <root>: an <mxCell>, or an <object> or
// <UserObject> wrapping one, which then carries the id and label.
type mxItem struct {
	XMLName  xml.Name
	ID       string      `xml:"id,attr"`
	Label    string      `xml:"label,attr"`
	Value    string      `xml:"value,attr"`
	Style    string      `xml:"style,attr"`
	Vertex   string      `xml:"vertex,attr"`
	Edge     string      `xml:"edge,attr"`
	Parent   string      `xml:"parent,attr"`
	Source   string      `xml:"source,attr"`
	Target   string      `xml:"target,attr"`
	Geometry *mxGeometry `xml:"mxGeometry"`
	Cell     *mxItem     `xml:"mxCell"`
}

type mxGeometry struct {
	X, Y, Width, Height float64
}

func (g *mxGeometry) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	for _, a := range start.Attr {
		v, _ := strconv.ParseFloat(a.Value, 64)
		switch a.Name.Local {
		case "x":
			g.X = v
		case "y":
			g.Y = v
		case "width":
			g.Width = v
		case "height":
			g.Height = v
		}
	}
	return d.Skip()
}

type mxGraphModel struct {
	Root mxRoot `xml:"root"`
}

type mxRoot struct {
	Items []mxItem `xml:",any"`
}

// drawioDiagram models a .drawio file: an <mxfile> whose first <diagram>
// holds the model, plain or in draw.io's compressed form (base64 of a raw
// deflate of the URL-encoded XML), or a bare <mxGraphModel>.
func drawioDiagram(content []byte) (*diagram, error) {
	var doc struct {
		XMLName  xml.Name
		Diagrams []struct {
			Text  string        `xml:",chardata"`
			Model *mxGraphModel `xml:"mxGraphModel"`
		} `xml:"diagram"`
		Root mxRoot `xml:"root"`
	}
	bad := errors.New("not a draw.io file: it needs an mxfile or mxGraphModel element")
	if err := xml.Unmarshal(content, &doc); err != nil {
		return nil, bad
	}
	var items []mxItem
	switch doc.XMLName.Local {
	case "mxGraphModel":
		items = doc.Root.Items
	case "mxfile":
		if len(doc.Diagrams) == 0 {
			return nil, errors.New("not a draw.io file: the mxfile has no diagram")
		}
		page := doc.Diagrams[0]
		if page.Model == nil {
			var model mxGraphModel
			raw, err := inflateDrawio(page.Text)
			if err == nil {
				err = xml.Unmarshal(raw, &model)
			}
			if err != nil {
				return nil, errors.New("not a draw.io file: the diagram is neither plain nor compressed XML")
			}
			page.Model = &model
		}
		items = page.Model.Root.Items
	default:
		return nil, bad
	}
	d := &diagram{}
	// An edge's label may be its own value or a child cell with edgeLabel
	// style, which parents the edge.
	labels := map[string]string{}
	var cells []mxItem
	for _, it := range items {
		switch it.XMLName.Local {
		case "mxCell":
		case "object", "UserObject":
			if it.Cell == nil {
				continue
			}
			c := *it.Cell
			c.ID, c.Value = it.ID, it.Label
			it = c
		default:
			continue
		}
		if strings.Contains(it.Style, "edgeLabel") {
			labels[it.Parent] = drawioText(it.Value)
			continue
		}
		cells = append(cells, it)
	}
	for _, c := range cells {
		switch {
		case c.Vertex == "1":
			n := diagramNode{ID: c.ID, Type: drawioShape(c.Style), Label: drawioText(c.Value), Stroke: styleValue(c.Style, "strokeColor"), Fill: styleValue(c.Style, "fillColor")}
			if c.Geometry != nil {
				n.X, n.Y, n.W, n.H = c.Geometry.X, c.Geometry.Y, c.Geometry.Width, c.Geometry.Height
			}
			d.nodes = append(d.nodes, n)
		case c.Edge == "1":
			label := drawioText(c.Value)
			if label == "" {
				label = labels[c.ID]
			}
			d.edges = append(d.edges, diagramEdge{ID: c.ID, From: c.Source, To: c.Target, Label: label})
		}
	}
	return d, nil
}

// inflateDrawio decodes a compressed <diagram> text.
func inflateDrawio(text string) ([]byte, error) {
	packed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(flate.NewReader(bytes.NewReader(packed)), maxDrawingBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxDrawingBytes {
		return nil, errors.New("the diagram is too large")
	}
	decoded, err := url.PathUnescape(string(raw))
	if err != nil {
		return nil, err
	}
	return []byte(decoded), nil
}

// drawioShape maps a cell style to the model's node types.
func drawioShape(style string) string {
	switch {
	case strings.HasPrefix(style, "ellipse") || strings.Contains(style, "shape=ellipse"):
		return "ellipse"
	case strings.HasPrefix(style, "rhombus") || strings.Contains(style, "shape=rhombus"):
		return "diamond"
	case strings.HasPrefix(style, "text;") || style == "text":
		return "text"
	}
	return "rectangle"
}

// styleValue reads key=value from a draw.io style string.
func styleValue(style, key string) string {
	for part := range strings.SplitSeq(style, ";") {
		if v, ok := strings.CutPrefix(part, key+"="); ok {
			return v
		}
	}
	return ""
}

var htmlTags = regexp.MustCompile(`<[^>]*>`)

// drawioText is a cell value as plain text: its HTML, when it is HTML, with
// tags dropped.
func drawioText(value string) string {
	return strings.TrimSpace(html.UnescapeString(htmlTags.ReplaceAllString(strings.ReplaceAll(value, "<br>", " "), "")))
}

// DrawIOSettings is the opt-in draw.io editor: its origin, and whether it
// is enabled, which needs a passing check first.
type DrawIOSettings struct {
	Origin  string `json:"origin,omitempty"`
	Enabled bool   `json:"enabled,omitempty"`
}

// DrawIOCheck is the POST /api/settings/drawio/check response.
type DrawIOCheck struct {
	OK       bool         `json:"ok"`
	Errors   []string     `json:"errors"`
	Warnings []string     `json:"warnings"`
	Detail   DrawIODetail `json:"detail"`
}

// DrawIODetail is what the check found: Public is set for the public
// diagrams.net service, CORS when the origin sends
// Access-Control-Allow-Origin, FramingOK when nothing refuses the frame.
type DrawIODetail struct {
	Public    bool `json:"public"`
	CORS      bool `json:"cors"`
	FramingOK bool `json:"framing_ok"`
}

const (
	drawioCheckTimeout = 5 * time.Second
	drawioCheckBody    = 256 << 10
	drawioPublicHost   = "embed.diagrams.net"
)

var drawioMarkers = []string{"draw.io", "diagrams.net", "geApp", "mxgraph"}

// drawioOrigin checks and normalises an origin for Settings: an absolute
// http(s) origin with nothing after the host.
func drawioOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("the draw.io origin must be an absolute http(s) origin such as https://drawio.example.com")
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), nil
}

func isLoopbackHost(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

// checkDrawIO probes origin as the editor frame would load it: GET
// /?embed=1&proto=json with a bounded body, no cross-origin redirect, and
// looks for draw.io's markers and for framing or CORS headers. publicHTTPS
// says whether uam itself is served over https, when an http origin would
// be mixed content; publicOrigins are uam's own, for frame-ancestors. The
// body is never kept.
func checkDrawIO(ctx context.Context, client *http.Client, raw string, publicHTTPS bool, publicOrigins []string) DrawIOCheck {
	out := DrawIOCheck{Errors: []string{}, Warnings: []string{}}
	origin, err := drawioOrigin(raw)
	if err != nil {
		out.Errors = append(out.Errors, err.Error())
		return out
	}
	u, _ := url.Parse(origin)
	if u.Scheme == "http" && publicHTTPS && !isLoopbackHost(u.Host) {
		out.Errors = append(out.Errors, "uam is served over https, so an http draw.io origin would be blocked as mixed content; use https")
		return out
	}
	out.Detail.Public = strings.EqualFold(u.Host, drawioPublicHost)
	ctx, cancel := context.WithTimeout(ctx, drawioCheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/?embed=1&proto=json", nil)
	if err != nil {
		out.Errors = append(out.Errors, err.Error())
		return out
	}
	if client == nil {
		client = &http.Client{}
	}
	c := *client
	c.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
		if strings.ToLower(req.URL.Scheme+"://"+req.URL.Host) != origin {
			return errors.New("the origin redirects elsewhere; give the final origin")
		}
		return nil
	}
	res, err := c.Do(req)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			out.Errors = append(out.Errors, fmt.Sprintf("the origin did not answer within %s", drawioCheckTimeout))
		case strings.Contains(err.Error(), "redirects elsewhere"):
			out.Errors = append(out.Errors, "the origin redirects elsewhere; give the final origin")
		default:
			out.Errors = append(out.Errors, "the origin could not be reached")
		}
		return out
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(res.Body, drawioCheckBody))
	if res.StatusCode != http.StatusOK {
		out.Errors = append(out.Errors, fmt.Sprintf("the origin answered HTTP %d, not a draw.io page", res.StatusCode))
		return out
	}
	page := strings.ToLower(string(body))
	marked := false
	for _, m := range drawioMarkers {
		marked = marked || strings.Contains(page, strings.ToLower(m))
	}
	if !marked {
		out.Errors = append(out.Errors, "the origin does not serve a draw.io page (no draw.io, diagrams.net, geApp or mxgraph marker)")
	}
	out.Detail.FramingOK = true
	if xfo := strings.ToUpper(strings.TrimSpace(res.Header.Get("X-Frame-Options"))); xfo == "DENY" || xfo == "SAMEORIGIN" && !containsOrigin(publicOrigins, origin) {
		out.Detail.FramingOK = false
		out.Errors = append(out.Errors, "the origin sends X-Frame-Options "+xfo+", which refuses the uam frame")
	}
	if ancestors, ok := frameAncestors(res.Header.Values("Content-Security-Policy")); ok && !allowsAncestor(ancestors, origin, publicOrigins) {
		out.Detail.FramingOK = false
		out.Errors = append(out.Errors, "the origin's Content-Security-Policy frame-ancestors does not allow uam")
	}
	out.Detail.CORS = res.Header.Get("Access-Control-Allow-Origin") != ""
	switch {
	case out.Detail.Public:
		out.Warnings = append(out.Warnings, "embed.diagrams.net is a third-party service under its own terms; its beacon is disabled (stealth=1) and diagram content stays in the browser")
	case !out.Detail.CORS:
		out.Warnings = append(out.Warnings, "the origin sends no Access-Control-Allow-Origin; the editor runs with allow-same-origin on its own origin only")
	}
	out.OK = len(out.Errors) == 0
	return out
}

func containsOrigin(origins []string, origin string) bool {
	for _, o := range origins {
		if strings.EqualFold(o, origin) {
			return true
		}
	}
	return false
}

// frameAncestors returns the sources of the first frame-ancestors directive
// in the policies, and whether there is one.
func frameAncestors(policies []string) ([]string, bool) {
	for _, policy := range policies {
		for directive := range strings.SplitSeq(policy, ";") {
			fields := strings.Fields(directive)
			if len(fields) > 0 && strings.EqualFold(fields[0], "frame-ancestors") {
				return fields[1:], true
			}
		}
	}
	return nil, false
}

// allowsAncestor says whether a frame-ancestors list lets one of uam's
// origins (or, with none configured, any https page) frame origin.
func allowsAncestor(sources []string, origin string, publicOrigins []string) bool {
	for _, src := range sources {
		s := strings.ToLower(strings.Trim(src, "'"))
		switch {
		case s == "*", s == "https:" && (len(publicOrigins) == 0 || hasScheme(publicOrigins, "https")), s == "http:" && hasScheme(publicOrigins, "http"):
			return true
		case s == "self" && containsOrigin(publicOrigins, origin):
			return true
		case containsOrigin(publicOrigins, s):
			return true
		case strings.HasPrefix(s, "*."):
			for _, o := range publicOrigins {
				if u, err := url.Parse(o); err == nil && strings.HasSuffix(u.Hostname(), s[1:]) {
					return true
				}
			}
		default:
			for _, o := range publicOrigins {
				if u, err := url.Parse(o); err == nil && (u.Host == s || u.Hostname() == s) {
					return true
				}
			}
		}
	}
	return false
}

func hasScheme(origins []string, scheme string) bool {
	for _, o := range origins {
		if strings.HasPrefix(o, scheme+"://") {
			return true
		}
	}
	return false
}

// drawioPolicy is the page policy for settings: the service policy with
// the draw.io origin as a frame source while the editor is enabled.
func drawioPolicy(st Settings) string {
	if !st.DrawIO.Enabled {
		return contentSecurity
	}
	origin, err := drawioOrigin(st.DrawIO.Origin)
	if err != nil {
		return contentSecurity
	}
	return contentSecurity + "; frame-src 'self' " + origin
}
