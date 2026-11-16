package main

import (
	"bytes"
	tea "charm.land/bubbletea/v2"
	"flag"
	"github.com/brew0x/percolate/internal/repo"
	"github.com/brew0x/percolate/internal/scene"
	"strings"
	"testing"
)

var _ = flag.Bool("update", false, "accepted for repository golden updates")

func TestCLI(t *testing.T) {
	for _, tt := range []struct {
		name     string
		args     []string
		code     int
		contains string
	}{{"hint", nil, 2, "--fixture"}, {"version", []string{"version"}, 0, "0.1.0"}, {"render", []string{"render", "--fixture", "../../testdata/fixtures/small.json", "--frame", "7", "--size", "100x30", "--color=false"}, 0, "●"}, {"bad size", []string{"render", "--fixture", "x", "--size", "-1x2"}, 2, "size"}, {"missing", []string{"--fixture", "missing.json"}, 1, "missing.json"}} {
		t.Run(tt.name, func(t *testing.T) {
			var out, err bytes.Buffer
			code := run(tt.args, &out, &err)
			if code != tt.code {
				t.Fatalf("exit %d: %s", code, err.String())
			}
			all := out.String() + err.String()
			if !strings.Contains(all, tt.contains) {
				t.Fatalf("missing %q: %s", tt.contains, all)
			}
			if tt.name == "render" {
				if strings.Contains(out.String(), "\x1b") {
					t.Fatal("plain rendering contains ANSI")
				}
				if len(strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")) != 30 {
					t.Fatal("wrong rendered height")
				}
				var again bytes.Buffer
				run(tt.args, &again, &err)
				if again.String() != out.String() {
					t.Fatal("CLI not repeatable")
				}
			}
		})
	}
}
func TestNavigationDetailsResizeAndQuit(t *testing.T) {
	m := model{s: scene.Build(repo.Snapshot{Branches: []repo.Branch{{Name: "main"}}, Commits: []repo.Commit{{SHA: "abc", Branch: "main", Author: "robin"}, {SHA: "def", Branch: "main"}}}), width: 60, height: 20}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = next.(model)
	if m.selected != 1 {
		t.Fatal("tab did not select next mark")
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if !m.details || !strings.Contains(m.View().Content, "def") {
		t.Fatal("detail panel missing selected object")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m = next.(model)
	if m.width != 40 || m.height != 12 || !m.View().AltScreen {
		t.Fatal("resize or full screen failed")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'q'})
	if cmd == nil {
		t.Fatal("quit not handled")
	}
}
