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
	if _, _, err := c.Areas([]string{"nope"}); err == nil {
		t.Error("unknown area should fail")
	}
	areas, chosen, _ := c.Areas([]string{"auth", "api", "auth"})
	if !chosen || len(areas) != 2 {
		t.Errorf("got %v %v", areas, chosen)
	}
	if all, chosen, _ := c.Areas(nil); chosen || len(all) != 5 {
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
