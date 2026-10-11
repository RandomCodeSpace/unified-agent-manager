package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

const glabStatusFixture = `
case "$*" in
  "config get host") printf 'gitlab.example.test';;
  "api --hostname gitlab.example.test --method GET user") printf '{"id":7,"username":"alex","name":"Alex Morgan","email":"private@example.test","token":"glpat-private"}';;
  "api --hostname gitlab.example.test --method GET personal_access_tokens/self") printf '{"id":4,"scopes":["api","read_repository"],"expires_at":"2027-01-31","token":"glpat-private"}';;
  "api --hostname gitlab.example.test --method GET projects/group%%2Fsub%%2Frepo") printf '{"id":12,"permissions":{"project_access":{"access_level":20},"group_access":{"access_level":40}}}';;
  *) exit 2;;
esac
`

func TestGlabStatusDefaultAndProject(t *testing.T) {
	// %% belongs to printf's input only, not to the endpoint in the case pattern.
	glabTestFake(t, strings.ReplaceAll(glabStatusFixture, "%%", "%"))
	for _, dir := range []string{"", glabTestRepo(t, "git@gitlab.example.test:group/sub/repo.git")} {
		got := checkGlabStatus(t.Context(), dir)
		if !got.Installed || !got.Authenticated || got.Hostname != "gitlab.example.test" || got.Username != "alex" || got.CheckedAt.IsZero() {
			t.Fatalf("status = %+v", got)
		}
		if got.Token == nil || len(got.Token.Scopes) != 2 || got.Token.ExpiresAt == nil || *got.Token.ExpiresAt != "2027-01-31" {
			t.Fatalf("token metadata = %+v", got.Token)
		}
		if dir != "" && (got.Project == nil || !got.Project.Readable || got.Project.Path != "group/sub/repo" || got.Project.AccessLevel != 40) {
			t.Fatalf("project = %+v", got.Project)
		}
		b, err := json.Marshal(got)
		if err != nil || strings.Contains(string(b), "private") {
			t.Fatal("status returned private fields or could not be encoded")
		}
	}
}

func TestGlabStatusUnknownMetadataAndProjectAccess(t *testing.T) {
	glabTestFake(t, `case "$*" in
  "api --hostname gitlab.example.test --method GET user") printf '{"id":7,"username":"alex"}';;
  *) printf 'glpat-private diagnostic' >&2; exit 1;;
esac`)
	dir := glabTestRepo(t, "https://gitlab.example.test/group/repo.git")
	got := checkGlabStatus(t.Context(), dir)
	if !got.Authenticated || got.Token != nil || got.Project == nil || got.Project.Readable || got.Project.AccessLevel != 0 {
		t.Fatalf("unknown access must stay unknown: %+v", got)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "private") {
		t.Fatal("CLI diagnostic escaped into status")
	}
}

func TestGlabStatusOAuthMetadata(t *testing.T) {
	for _, owner := range []string{"7", "8"} {
		t.Run(owner, func(t *testing.T) {
			glabTestFake(t, `case "$*" in
  "config get host") printf gitlab.example.test;;
  "api --hostname gitlab.example.test --method GET user") printf '{"id":7,"username":"alex"}';;
  "api --hostname gitlab.example.test --method GET https://gitlab.example.test/oauth/token/info") printf '{"resource_owner_id":`+owner+`,"scope":["api","write_repository"],"expires_in":7200}';;
  *) exit 1;;
esac`)
			got := checkGlabStatus(t.Context(), "")
			if !got.Authenticated {
				t.Fatal("OAuth account not recognized")
			}
			if owner != "7" {
				if got.Token != nil {
					t.Fatal("returned another account's token metadata")
				}
				return
			}
			if got.Token == nil || len(got.Token.Scopes) != 2 || got.Token.ExpiresAt == nil {
				t.Fatalf("OAuth metadata = %+v", got.Token)
			}
			expires, err := time.Parse(time.RFC3339, *got.Token.ExpiresAt)
			if err != nil || time.Until(expires) < 119*time.Minute || time.Until(expires) > 121*time.Minute {
				t.Fatalf("OAuth expiry = %v, %v", expires, err)
			}
		})
	}
}

func TestGlabStatusUnavailable(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		got := checkGlabStatus(t.Context(), "")
		if got.Installed || got.Authenticated || !strings.Contains(got.Message, "Install glab") {
			t.Fatalf("status = %+v", got)
		}
	})
	t.Run("authentication", func(t *testing.T) {
		glabTestFake(t, `if [ "$1" = config ]; then printf gitlab.example.test; else printf 'glpat-private' >&2; exit 1; fi`)
		got := checkGlabStatus(t.Context(), "")
		if !got.Installed || got.Authenticated || got.Username != "" || !strings.Contains(got.Message, "Could not verify authentication") || strings.Contains(got.Message, "private") {
			t.Fatalf("status = %+v", got)
		}
	})
	t.Run("invalid-default", func(t *testing.T) {
		glabTestFake(t, `printf 'https://token@gitlab.example.test/?secret=value'`)
		got := checkGlabStatus(t.Context(), "")
		if got.Hostname != "" || got.Authenticated || !strings.Contains(got.Message, "default instance") {
			t.Fatalf("status = %+v", got)
		}
	})
	t.Run("github", func(t *testing.T) {
		bin := glabTestFake(t, `printf called > "$0.calls"`)
		got := checkGlabStatus(t.Context(), glabTestRepo(t, "https://github.com/group/repo.git"))
		if got.Hostname != "" || got.Authenticated || !strings.Contains(got.Message, "origin") {
			t.Fatalf("status = %+v", got)
		}
		if _, err := os.Stat(bin + ".calls"); !os.IsNotExist(err) {
			t.Fatal("GitHub origin ran glab")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		glabTestFake(t, "sleep 10")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if got := checkGlabStatus(ctx, ""); got.Authenticated {
			t.Fatal("cancelled check claimed authentication")
		}
	})
}

func TestGlabStatusRoute(t *testing.T) {
	glabTestFake(t, strings.ReplaceAll(glabStatusFixture, "%%", "%"))
	ts := newTestServer(t, ServerConfig{Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})
	if w := ts.do(http.MethodGet, "/api/glab", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated route = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, "/api/glab?project_id=missing", "", withCookie(ts)); w.Code != http.StatusNotFound {
		t.Fatalf("missing project = %d", w.Code)
	}
	p, err := ts.m.AddProject(glabTestRepo(t, "https://gitlab.example.test/group/sub/repo.git"), "GitLab fixture")
	if err != nil {
		t.Fatal(err)
	}
	w := ts.do(http.MethodGet, "/api/glab?project_id="+p.ID, "", withCookie(ts))
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"access_level":40`) {
		t.Fatalf("project route = %d %s", w.Code, w.Body)
	}
}
