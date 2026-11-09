package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadExample(t *testing.T) {
	c, err := Load("../../saw.example.json")
	if err != nil {
		t.Fatal(err)
	}
	app, err := c.App("main")
	if err != nil || app.Name != "main" || app.IOS != "com.example.app" {
		t.Errorf("got %+v %v", app, err)
	}
	if _, err := c.App(""); err == nil {
		t.Error("two apps: --app should be required")
	}
	if got := c.Subsystem(app); got != "com.example.app" {
		t.Error(got)
	}
	if _, _, err := c.Scopes([]string{"nope"}); err == nil {
		t.Error("unknown scope should fail")
	}
	scopes, chosen, _ := c.Scopes([]string{"auth", "api", "auth"})
	if !chosen || len(scopes) != 2 {
		t.Errorf("got %v %v", scopes, chosen)
	}
	if all, chosen, _ := c.Scopes(nil); chosen || len(all) != 5 {
		t.Errorf("got %v %v", all, chosen)
	}
}

func TestNoConfig(t *testing.T) {
	c := &Config{}
	app, err := c.App("com.x.app")
	if err != nil || app.Android != "com.x.app" || app.IOS != "com.x.app" {
		t.Errorf("got %+v %v", app, err)
	}
	if _, err := c.App(""); err == nil {
		t.Error("want an error without --app")
	}
	if c.TagFor() != nil {
		t.Error("no tag convention")
	}
}

func TestFindWalksUp(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, FileName), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Find(deep); got != filepath.Join(root, FileName) {
		t.Error(got)
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(p, []byte(`{"apps": {"a": {"andriod": "com.x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "andriod") {
		t.Errorf("want an error naming the misspelled key, got %v", err)
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("missing file should fail")
	}
	if _, err := Load(writeConfig(t, `{"apps": `)); err == nil {
		t.Error("broken JSON should fail")
	}
	if _, err := Load(writeConfig(t, `{"apps": {"a": {}}}`)); err == nil || !strings.Contains(err.Error(), "needs an android package or ios bundle id") {
		t.Errorf("app with no ids: %v", err)
	}
}

func TestLoadFindsNearest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"apps": {"only": {"android": "com.x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	sub := filepath.Join(dir, "a")
	os.Mkdir(sub, 0o755)
	os.Chdir(sub)
	c, err := Load("")
	if err != nil || c.Path == "" {
		t.Fatalf("got %+v %v", c, err)
	}
	if app, err := c.App(""); err != nil || app.Name != "only" {
		t.Errorf("the only app should be the default: %+v %v", app, err)
	}
	if _, err := c.App("other"); err == nil || !strings.Contains(err.Error(), "one of: only") {
		t.Errorf("got %v", err)
	}
	os.Chdir(t.TempDir())
	if c, err := Load(""); err != nil || c.Path != "" {
		t.Errorf("no saw.json is fine: %+v %v", c, err)
	}
}

func TestTemplates(t *testing.T) {
	c := &Config{}
	if _, err := c.App("nodots"); err == nil {
		t.Error("a bare name with no saw.json should fail")
	}
	c.Android.Tag = "{scope}-{SCOPE}"
	if got := c.TagFor()("api"); got != "api-API" {
		t.Error(got)
	}
	c.IOS.Subsystem = "{bundle}.log"
	if got := c.Subsystem(App{IOS: "com.x"}); got != "com.x.log" {
		t.Error(got)
	}
	if got := (&Config{}).Subsystem(App{IOS: "com.x"}); got != "" {
		t.Errorf("no convention, no subsystem: %q", got)
	}
}
