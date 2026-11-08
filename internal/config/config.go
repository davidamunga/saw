// Package config reads saw.json, the one file that ties saw to a project.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const FileName = "saw.json"

type Config struct {
	Path    string         `json:"-"`
	Apps    map[string]App `json:"apps"`
	AreaSet []string       `json:"areas"`
	Android struct {
		// Tag is the logcat tag for an area, e.g. "Example{Area}". Empty: the app has no tag convention.
		Tag string `json:"tag"`
	} `json:"android"`
	IOS struct {
		// Subsystem is the app's Logger subsystem; areas are its categories. Default "{bundle}".
		Subsystem string `json:"subsystem"`
	} `json:"ios"`
}

type App struct {
	Name    string `json:"-"`
	Android string `json:"android"` // package name
	IOS     string `json:"ios"`     // bundle id
	Process string `json:"process"` // iOS executable; looked up on the device when empty
}

// Find walks up from dir to the nearest saw.json. It returns "" when there is none.
func Find(dir string) string {
	for {
		p := filepath.Join(dir, FileName)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// Load reads path, or finds saw.json from the working directory when path is empty.
// No file is fine: `saw` then works from --app ids alone.
func Load(path string) (*Config, error) {
	if path == "" {
		wd, _ := os.Getwd()
		path = Find(wd)
		if path == "" {
			return &Config{}, nil
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields() // a misspelled key is an error, not silently ignored
	if err := dec.Decode(c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.Path = path
	for name, app := range c.Apps {
		if app.Android == "" && app.IOS == "" {
			return nil, fmt.Errorf("%s: app %q needs an android package or ios bundle id", path, name)
		}
		app.Name = name
		c.Apps[name] = app
	}
	return c, nil
}

func (c *Config) appNames() []string {
	names := make([]string, 0, len(c.Apps))
	for n := range c.Apps {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// App resolves --app: a name from saw.json, or a package/bundle id used as-is on both platforms.
// With no --app, the only configured app.
func (c *Config) App(name string) (App, error) {
	if name == "" {
		switch len(c.Apps) {
		case 1:
			return c.Apps[c.appNames()[0]], nil
		case 0:
			return App{}, errors.New("pass --app <package or bundle id> (no saw.json found)")
		default:
			return App{}, fmt.Errorf("pass --app (one of: %s)", strings.Join(c.appNames(), ", "))
		}
	}
	if app, ok := c.Apps[name]; ok {
		return app, nil
	}
	if strings.Contains(name, ".") {
		return App{Name: name, Android: name, IOS: name}, nil
	}
	if len(c.Apps) == 0 {
		return App{}, fmt.Errorf("unknown app %q: pass a package or bundle id, or add it to saw.json", name)
	}
	return App{}, fmt.Errorf("unknown app %q (one of: %s)", name, strings.Join(c.appNames(), ", "))
}

// Areas resolves --area. With none chosen, every configured area and chosen=false.
func (c *Config) Areas(selected []string) (areas []string, chosen bool, err error) {
	if len(selected) == 0 {
		return c.AreaSet, false, nil
	}
	for _, a := range selected {
		if len(c.AreaSet) > 0 && !slices.Contains(c.AreaSet, a) {
			return nil, false, fmt.Errorf("unknown area %q (one of: %s)", a, strings.Join(c.AreaSet, ", "))
		}
		if !slices.Contains(areas, a) {
			areas = append(areas, a)
		}
	}
	return areas, true, nil
}

// TagFor maps an area to its logcat tag, or is nil when the project has no tag convention.
func (c *Config) TagFor() func(string) string {
	tmpl := c.Android.Tag
	if tmpl == "" {
		return nil
	}
	return func(area string) string {
		title := area
		if area != "" {
			title = strings.ToUpper(area[:1]) + area[1:]
		}
		return strings.NewReplacer("{Area}", title, "{area}", area, "{AREA}", strings.ToUpper(area)).Replace(tmpl)
	}
}

// Subsystem is the app's iOS Logger subsystem, or "" when the project doesn't log to one.
// A saw.json with areas implies the convention: subsystem = bundle id, area = category.
func (c *Config) Subsystem(app App) string {
	tmpl := c.IOS.Subsystem
	if tmpl == "" {
		if len(c.AreaSet) == 0 {
			return ""
		}
		tmpl = "{bundle}"
	}
	return strings.ReplaceAll(tmpl, "{bundle}", app.IOS)
}
