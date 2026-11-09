package handlers

import (
	"io/fs"
	"os"
	"path"
	"strings"
	"testing"
)

// TestDashboardEmbedContract pins the agreement between the dashboard
// bundler and the server: index.html keeps the Go template placeholders,
// its asset URLs are rewritten from /static to the ./.static route, and
// the embedded tree actually holds those assets.
// A bundler or config change that moves the output breaks this here
// rather than at runtime on an empty page.
func TestDashboardEmbedContract(t *testing.T) {
	page, assets, err := prepareDashboard(os.DirFS("../.."))
	if err != nil {
		t.Fatalf("prepare dashboard: %v", err)
	}

	// The parsed template is what the server executes. Placeholders have
	// to survive the rewrite, and asset URLs have to point at the route
	// serveStatic actually handles.
	source := page.Tree.Root.String()
	placeholders := []string{
		"AdminView", "StatsMode", "DevMode",
		"ShowImpressum", "ShowPrivacy", "ShowContact", "LogoutURL",
	}
	for _, p := range placeholders {
		if want := "{{." + p + "}}"; !strings.Contains(source, want) {
			t.Errorf("dashboard index.html is missing the %s placeholder", p)
		}
	}
	if !strings.Contains(source, "./.static/") {
		t.Errorf("dashboard index.html references no ./.static asset:\n%s", source)
	}
	if strings.Contains(source, `"/static/`) {
		t.Errorf("dashboard index.html still holds an unrewritten /static reference")
	}

	exts := map[string]bool{}
	err = fs.WalkDir(assets, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			exts[path.Ext(p)] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("cannot walk the embedded static tree: %v", err)
	}
	for _, ext := range []string{".js", ".css"} {
		if !exts[ext] {
			t.Errorf("embedded static tree holds no %s asset, got %v", ext, exts)
		}
	}
}
