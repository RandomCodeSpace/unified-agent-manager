package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Only explicit public metadata crosses the API boundary. CLI diagnostics and
// authentication output can contain credentials and are never returned.
type glabStatus struct {
	Installed     bool               `json:"installed"`
	CheckedAt     time.Time          `json:"checked_at"`
	Hostname      string             `json:"hostname,omitempty"`
	Authenticated bool               `json:"authenticated"`
	Username      string             `json:"username,omitempty"`
	Name          string             `json:"name,omitempty"`
	Message       string             `json:"message,omitempty"`
	Token         *glabTokenStatus   `json:"token,omitempty"`
	Project       *glabProjectStatus `json:"project,omitempty"`
}

type glabTokenStatus struct {
	Scopes    []string `json:"scopes,omitempty"`
	ExpiresAt *string  `json:"expires_at"`
}

type glabProjectStatus struct {
	Path        string `json:"path"`
	Readable    bool   `json:"readable"`
	AccessLevel int    `json:"access_level,omitempty"`
}

func (s *Server) handleGlabStatus(w http.ResponseWriter, r *http.Request) {
	var dir string
	if id := r.URL.Query().Get("project_id"); id != "" {
		s.m.mu.Lock()
		if p := s.m.projects[id]; p != nil {
			dir = p.Dir
		}
		s.m.mu.Unlock()
		if dir == "" {
			writeFailure(w, newError(http.StatusNotFound, "project not found"))
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, checkGlabStatus(r.Context(), dir))
}

func checkGlabStatus(ctx context.Context, projectDir string) glabStatus {
	ctx, cancel := context.WithTimeout(ctx, glabTimeout)
	defer cancel()
	out := glabStatus{CheckedAt: time.Now().UTC()}
	bin, err := exec.LookPath("glab")
	if err != nil {
		out.Message = "Install glab on the UAM server and make it available on the service's PATH."
		return out
	}
	out.Installed = true
	gate := glabGate{executable: bin}
	if projectDir != "" {
		var ok bool
		gate, ok = glabOrigin(ctx, projectDir)
		if !ok {
			out.Message = "This project's origin is not a supported GitLab remote. Choose a GitLab project or the default instance."
			return out
		}
		out.Project = &glabProjectStatus{Path: gate.Path}
	} else {
		// Resolve the default outside a checkout, using glab's own environment
		// and config precedence instead of reading its credential file.
		gate.Dir, err = os.UserHomeDir()
		if err != nil {
			out.Message = "The UAM service's home directory is unavailable."
			return out
		}
		host, runErr := glabExec(ctx, glabTimeout, gate.Dir, bin, false, "config", "get", "host")
		host = strings.TrimSpace(host)
		u, parseErr := url.Parse("https://" + host)
		if runErr != nil || parseErr != nil || u.Hostname() == "" || u.Host != host || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			out.Message = "Could not resolve glab's default instance. Check glab configuration on the UAM server."
			return out
		}
		gate.Host = host
	}
	out.Hostname = gate.Host
	var user struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
		Name     string `json:"name"`
	}
	if !glabStatusRead(ctx, gate, "user", &user) || user.ID <= 0 || user.Username == "" {
		out.Message = "Could not verify authentication. Check glab sign-in and connectivity for this instance on the UAM server."
		return out
	}
	out.Authenticated, out.Username, out.Name = true, user.Username, user.Name
	var token struct {
		ID int64 `json:"id"`
		glabTokenStatus
	}
	// Personal tokens and OAuth expose different metadata endpoints. Missing
	// metadata stays unknown; the account check remains independently useful.
	if glabStatusRead(ctx, gate, "personal_access_tokens/self", &token) && token.ID > 0 {
		out.Token = &token.glabTokenStatus
	} else {
		var oauth struct {
			OwnerID   int64    `json:"resource_owner_id"`
			Scopes    []string `json:"scope"`
			ExpiresIn *int64   `json:"expires_in"`
		}
		// This URL keeps the exact authenticated host, outside /api/v4. glab
		// supplies its own Authorization header; UAM never reads the token.
		if glabStatusRead(ctx, gate, "https://"+gate.Host+"/oauth/token/info", &oauth) && oauth.OwnerID == user.ID && oauth.Scopes != nil {
			out.Token = &glabTokenStatus{Scopes: oauth.Scopes}
			if oauth.ExpiresIn != nil {
				expires := time.Now().UTC().Add(time.Duration(*oauth.ExpiresIn) * time.Second).Format(time.RFC3339)
				out.Token.ExpiresAt = &expires
			}
		}
	}
	if out.Project != nil {
		var project struct {
			ID          int64 `json:"id"`
			Permissions struct {
				ProjectAccess *struct {
					AccessLevel int `json:"access_level"`
				} `json:"project_access"`
				GroupAccess *struct {
					AccessLevel int `json:"access_level"`
				} `json:"group_access"`
			} `json:"permissions"`
		}
		if glabStatusRead(ctx, gate, "projects/"+url.PathEscape(gate.Path), &project) && project.ID > 0 {
			out.Project.Readable = true
			if p := project.Permissions.ProjectAccess; p != nil {
				out.Project.AccessLevel = p.AccessLevel
			}
			if p := project.Permissions.GroupAccess; p != nil {
				out.Project.AccessLevel = max(out.Project.AccessLevel, p.AccessLevel)
			}
		}
	}
	return out
}

// Endpoints are fixed by the caller; the only variable suffix is an escaped
// project path from its existing origin. No mutation or pagination is requested.
func glabStatusRead(ctx context.Context, gate glabGate, endpoint string, dst any) bool {
	body, err := glabExec(ctx, glabTimeout, gate.Dir, gate.executable, false,
		"api", "--hostname", gate.Host, "--method", "GET", endpoint)
	return err == nil && json.Unmarshal([]byte(body), dst) == nil
}
