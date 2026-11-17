package main

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/brew0x/percolate/internal/github"
	"github.com/brew0x/percolate/internal/repo"
	"github.com/brew0x/percolate/internal/scene"
	"image/color"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

const version = "0.1.0"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, out, errout io.Writer) int {
	if len(args) > 0 && args[0] == "version" {
		fmt.Fprintln(out, "percolate "+version)
		return 0
	}
	render := len(args) > 0 && args[0] == "render"
	if render {
		args = args[1:]
	}
	fs := flag.NewFlagSet("percolate", flag.ContinueOnError)
	fs.SetOutput(errout)
	fixture := fs.String("fixture", "", "recorded snapshot JSON to replay")
	record := fs.String("record", "", "save a fetched snapshot to this JSON file for exact replay")
	frame := fs.Int("frame", 0, "animation frame")
	size := fs.String("size", "100x30", "terminal columns x rows")
	colored := fs.Bool("color", true, "ANSI colors")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(errout, "unexpected argument")
		return 2
	}
	if *fixture != "" && fs.NArg() > 0 {
		fmt.Fprintln(errout, "--fixture replays a recording; remove the repository argument")
		return 2
	}
	if *record != "" && *fixture != "" {
		fmt.Fprintln(errout, "use either --fixture to replay a recording, or owner/repo with --record")
		return 2
	}
	if *fixture == "" && *record == "" && fs.NArg() == 0 {
		fmt.Fprintln(errout, "Use percolate owner/repo to watch GitHub, or percolate --fixture snapshot.json to replay a recording.")
		return 2
	}
	w, h, err := parseSize(*size)
	if err != nil {
		fmt.Fprintln(errout, err)
		return 2
	}
	var snapshot repo.Snapshot
	if *fixture != "" {
		data, err := os.ReadFile(*fixture)
		if err != nil {
			fmt.Fprintln(errout, err)
			return 1
		}
		if err = json.Unmarshal(data, &snapshot); err != nil {
			fmt.Fprintln(errout, err)
			return 1
		}
	} else {
		snapshot, err = watch(context.Background(), *record, fs.Args())
		if err != nil {
			fmt.Fprintln(errout, err)
			if errors.As(err, new(usageError)) {
				return 2
			}
			return 1
		}
	}
	s := scene.Build(snapshot)
	if render {
		fmt.Fprintln(out, paint(scene.Frame(s, *frame, w, h), *colored))
		return 0
	}
	_, err = tea.NewProgram(model{s: s, width: w, height: h, selected: 0}).Run()
	if err != nil {
		fmt.Fprintln(errout, err)
		return 1
	}
	return 0
}
func parseSize(s string) (int, int, error) {
	a, b, ok := strings.Cut(s, "x")
	w, e1 := strconv.Atoi(a)
	h, e2 := strconv.Atoi(b)
	if !ok || e1 != nil || e2 != nil || w < 1 || h < 1 || w > 1000 || h > 1000 {
		return 0, 0, errors.New("size must be WxH, each between 1 and 1000")
	}
	return w, h, nil
}

// usageError marks mistakes in command-line shape, exiting 2 like flag.Parse.
type usageError struct{ error }

// valueFlags consume a following token, which lets reorderArgs tell flag
// values apart from the owner/repo positional. Keep this in sync with the
// flag set above.
var valueFlags = map[string]bool{"fixture": true, "record": true, "size": true, "frame": true}

// reorderArgs moves non-flag tokens behind the flags, because flag.Parse
// stops at the first one; `percolate owner/repo --record x` must work as
// written, not only as `percolate --record x owner/repo`. Multiple positionals
// keep their relative order at the end, where run() counts them.
func reorderArgs(args []string) []string {
	var positional, rest []string
	takeValue := false
	for _, a := range args {
		if takeValue {
			rest, takeValue = append(rest, a), false
			continue
		}
		if strings.HasPrefix(a, "-") {
			rest = append(rest, a)
			name := strings.TrimLeft(a, "-")
			if eq := strings.Index(name, "="); eq < 0 && valueFlags[name] {
				takeValue = true
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(rest, positional...)
}

// watch fetches a live snapshot for owner/repo and, when a path is given,
// records the exact JSON so any later view can replay it identically.
func watch(ctx context.Context, recordPath string, args []string) (repo.Snapshot, error) {
	if len(args) != 1 {
		return repo.Snapshot{}, usageError{errors.New("repository must be owner/repo, for example percolate brew0x/percolate")}
	}
	owner, name, ok := strings.Cut(args[0], "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return repo.Snapshot{}, usageError{errors.New("repository must be owner/repo, for example percolate brew0x/percolate")}
	}
	token, err := github.ResolveToken(ctx)
	if err != nil {
		return repo.Snapshot{}, err
	}
	client, err := github.NewClient(token)
	if err != nil {
		return repo.Snapshot{}, err
	}
	snapshot, err := client.BuildSnapshot(ctx, github.DefaultSnapshotOptions(owner, name))
	if err != nil {
		return repo.Snapshot{}, err
	}
	if recordPath != "" {
		data, err := json.MarshalIndent(snapshot, "", "  ")
		if err != nil {
			return repo.Snapshot{}, err
		}
		if err := os.WriteFile(recordPath, append(data, '\n'), 0o600); err != nil {
			return repo.Snapshot{}, err
		}
	}
	return snapshot, nil
}

var palette = []color.Color{lipgloss.Color("#d5ddd9"), lipgloss.Color("#374b4b"), lipgloss.Color("#80d9c0"), lipgloss.Color("#586b70"), lipgloss.Color("#a0dc80"), lipgloss.Color("#f08382"), lipgloss.Color("#f4cf83"), lipgloss.Color("#e5b8ef"), lipgloss.Color("#ffffff")}

func paint(g scene.Grid, colored bool) string {
	if !colored {
		return g.String()
	}
	var b strings.Builder
	for y, row := range g {
		if y > 0 {
			b.WriteByte('\n')
		}
		for x := 0; x < len(row); {
			end := x + 1
			for end < len(row) && row[end].Style == row[x].Style {
				end++
			}
			var text strings.Builder
			for _, c := range row[x:end] {
				text.WriteRune(c.Rune)
			}
			st := lipgloss.NewStyle().Foreground(palette[int(row[x].Style)])
			if row[x].Style == scene.Selected {
				st = st.Bold(true).Underline(true)
			}
			b.WriteString(st.Render(text.String()))
			x = end
		}
	}
	return b.String()
}

type tickMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(125*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

type model struct {
	s                              scene.Scene
	frame, width, height, selected int
	details                        bool
}

func (m model) Init() tea.Cmd { return tick() }
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tickMsg:
		m.frame++
		return m, tick()
	case tea.WindowSizeMsg:
		m.width = v.Width
		m.height = v.Height
	case tea.KeyPressMsg:
		switch v.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab", "right", "down":
			if len(m.s.Marks) > 0 {
				m.selected = (m.selected + 1) % len(m.s.Marks)
			}
		case "shift+tab", "left", "up":
			if len(m.s.Marks) > 0 {
				m.selected = (m.selected + len(m.s.Marks) - 1) % len(m.s.Marks)
			}
		case "enter":
			m.details = !m.details
		case "esc":
			m.details = false
		}
	}
	return m, nil
}
func (m model) View() tea.View {
	s := m.s
	if len(s.Marks) > 0 {
		mark := s.Marks[m.selected]
		s.SelectedID = mark.ID
		count := scene.VisibleLanes(m.width)
		if mark.Lane < s.FirstLane || mark.Lane >= s.FirstLane+count {
			s.FirstLane = mark.Lane
		}
	}
	reserve := 1
	if m.details {
		reserve = 3
	}
	height := max(0, m.height-reserve)
	text := paint(scene.Frame(s, m.frame, m.width, height), true)
	footer := "tab / arrows  ·  enter  ·  q"
	if m.details && len(s.Marks) > 0 {
		footer = s.Marks[m.selected].Detail + "\n\n" + footer
	}
	footer = lipgloss.NewStyle().Width(max(0, m.width)).MaxWidth(max(0, m.width)).MaxHeight(reserve).Render(footer)
	if height > 0 {
		text += "\n"
	}
	view := tea.NewView(text + footer)
	view.AltScreen = true
	return view
}
