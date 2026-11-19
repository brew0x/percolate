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
	"github.com/brew0x/percolate/internal/live"
	"github.com/brew0x/percolate/internal/repo"
	"github.com/brew0x/percolate/internal/scene"
	"image/color"
	"io"
	"os"
	"strconv"
	"strings"
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
	var liveFetch live.Fetcher
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
		var fetch live.Fetcher
		snapshot, fetch, err = watch(context.Background(), *record, fs.Args())
		if err != nil {
			fmt.Fprintln(errout, err)
			if errors.As(err, new(usageError)) {
				return 2
			}
			return 1
		}
		if !render {
			// Only the interactive window polls. A render is a single exact
			// frame and a fixture is a fixed replay, so neither refreshes.
			liveFetch = fetch
		}
	}
	s := scene.Build(snapshot)
	if render {
		fmt.Fprintln(out, paint(scene.Frame(s, *frame, w, h), *colored))
		return 0
	}
	m := model{
		s: s, width: w, height: h, selected: 0,
		prev: snapshot, cur: snapshot, fetch: liveFetch,
		web: browserBase(), slug: slugFromArgs(fs.Args()),
	}
	if len(s.Marks) > 0 {
		m.selectedID = s.Marks[0].ID
	}
	_, err = tea.NewProgram(m).Run()
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

// watch fetches a live snapshot for owner/repo and returns a Fetcher that can
// repeat the same bounded read on the poll interval. When a path is given, the
// first snapshot is recorded as exact JSON so any later view can replay it.
func watch(ctx context.Context, recordPath string, args []string) (repo.Snapshot, live.Fetcher, error) {
	if len(args) != 1 {
		return repo.Snapshot{}, nil, usageError{errors.New("repository must be owner/repo, for example percolate brew0x/percolate")}
	}
	owner, name, ok := strings.Cut(args[0], "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return repo.Snapshot{}, nil, usageError{errors.New("repository must be owner/repo, for example percolate brew0x/percolate")}
	}
	token, err := github.ResolveToken(ctx)
	if err != nil {
		return repo.Snapshot{}, nil, err
	}
	client, err := github.NewClient(token)
	if err != nil {
		return repo.Snapshot{}, nil, err
	}
	opts := github.DefaultSnapshotOptions(owner, name)
	fetch := func(ctx context.Context) (repo.Snapshot, error) {
		return client.BuildSnapshot(ctx, opts)
	}
	snapshot, err := fetch(ctx)
	if err != nil {
		return repo.Snapshot{}, nil, err
	}
	if recordPath != "" {
		data, err := json.MarshalIndent(snapshot, "", "  ")
		if err != nil {
			return repo.Snapshot{}, nil, err
		}
		if err := os.WriteFile(recordPath, append(data, '\n'), 0o600); err != nil {
			return repo.Snapshot{}, nil, err
		}
	}
	return snapshot, fetch, nil
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
