package main

import (
	"bytes"
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
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

// fallbackVersion is what a plain local build reports. Releases override it
// with the tag through -ldflags; go-installed builds have no ldflags, so
// installedVersion reads the module version Go records in the binary instead.
const fallbackVersion = "0.1.0"

// var, not const, so GoReleaser can set the tagged version with -ldflags.
var version = fallbackVersion

// releaseVersion resolves what `percolate version` prints. An injected release
// tag wins; otherwise a real module version (go install owner/repo/cmd@latest)
// beats the source fallback, so the documented install route reports the
// version that was actually installed rather than a stale source constant.
func releaseVersion(injected, module string) string {
	if injected != "" && injected != fallbackVersion {
		return injected
	}
	if module != "" && module != "(devel)" {
		return strings.TrimPrefix(module, "v")
	}
	return fallbackVersion
}

func installedVersion() string {
	module := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		module = info.Main.Version
	}
	return releaseVersion(version, module)
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// A freshly installed binary is usually probed with one of these spellings.
// They all answer the same honest version and the same usage, on stdout with
// exit 0, rather than looking like an invalid repository or a flag error.
func firstRun(args []string, out io.Writer) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintln(out, "percolate "+installedVersion())
	case "help", "--help", "-h":
		printHelp(out)
	default:
		return false
	}
	return true
}

const helpIntro = `Percolate draws a repository's life as a terminal landscape.

Usage:
  percolate owner/repo [--record snapshot.json]
  percolate --fixture snapshot.json
  percolate render --fixture snapshot.json --frame 7 --size 100x30 [--color=false]
  percolate version, percolate --version, percolate -v
  percolate help, percolate --help, percolate -h

Flags:
`

const helpKeys = `
In the live view: tab or arrows step through marks, enter opens details,
o opens the GitHub page, and q quits.
`

// flagSet and defineFlags are the single definition of Percolate's flags,
// shared by normal parsing and by help so the printed list cannot drift from
// what the CLI reads.
func flagSet(output io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("percolate", flag.ContinueOnError)
	fs.SetOutput(output)
	return fs
}

type cliOptions struct {
	fixture, record, size *string
	frame                 *int
	color                 *bool
}

func defineFlags(fs *flag.FlagSet) cliOptions {
	return cliOptions{
		fixture: fs.String("fixture", "", "recorded snapshot JSON to replay"),
		record:  fs.String("record", "", "save a fetched snapshot to this JSON file for exact replay"),
		frame:   fs.Int("frame", 0, "animation frame"),
		size:    fs.String("size", "100x30", "terminal columns x rows"),
		color:   fs.Bool("color", true, "ANSI colors"),
	}
}

func printHelp(out io.Writer) {
	fmt.Fprint(out, helpIntro)
	fs := flagSet(out)
	defineFlags(fs)
	fs.PrintDefaults()
	fmt.Fprint(out, helpKeys)
}

func run(args []string, out, errout io.Writer) int {
	if firstRun(args, out) {
		return 0
	}
	render := len(args) > 0 && args[0] == "render"
	if render {
		args = args[1:]
	}
	// Keep flag diagnostics on stderr; a -h anywhere (including after
	// `render`) is a successful help request on stdout, not a usage error.
	// The buffer swallows the flag package's own terse usage in that case.
	var flagOut bytes.Buffer
	fs := flagSet(&flagOut)
	o := defineFlags(fs)
	if err := fs.Parse(reorderArgs(args, fs)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printHelp(out)
			return 0
		}
		fmt.Fprint(errout, flagOut.String())
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(errout, "unexpected argument")
		return 2
	}
	if *o.fixture != "" && fs.NArg() > 0 {
		fmt.Fprintln(errout, "--fixture replays a recording; remove the repository argument")
		return 2
	}
	if *o.record != "" && *o.fixture != "" {
		fmt.Fprintln(errout, "use either --fixture to replay a recording, or owner/repo with --record")
		return 2
	}
	if *o.fixture == "" && *o.record == "" && fs.NArg() == 0 {
		fmt.Fprintln(errout, "Use percolate owner/repo to watch GitHub, or percolate --fixture snapshot.json to replay a recording.")
		return 2
	}
	w, h, err := parseSize(*o.size)
	if err != nil {
		fmt.Fprintln(errout, err)
		return 2
	}
	var snapshot repo.Snapshot
	var liveFetch live.Fetcher
	if *o.fixture != "" {
		data, err := os.ReadFile(*o.fixture)
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
		snapshot, fetch, err = watch(context.Background(), *o.record, fs.Args())
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
		fmt.Fprintln(out, paint(scene.Frame(s, *o.frame, w, h), *o.color))
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

// boolFlag is the unexported interface the flag package uses to tell boolean
// flags apart from flags that consume a following token. Reporting which flags
// take a value from the very set the parser reads keeps reorderArgs from
// drifting when a flag is added or changed.
type boolFlag interface{ IsBoolFlag() bool }

func takesValue(f *flag.Flag) bool {
	if f == nil {
		return false
	}
	bf, ok := f.Value.(boolFlag)
	return !ok || !bf.IsBoolFlag()
}

// reorderArgs moves non-flag tokens behind the flags, because flag.Parse
// stops at the first one; `percolate owner/repo --record x` must work as
// written, not only as `percolate --record x owner/repo`. Multiple positionals
// keep their relative order at the end, where run() counts them. The flags
// that consume a token are read from fs, the same definition used by Parse and
// by help, so a new flag cannot make a positional look like its value.
func reorderArgs(args []string, fs *flag.FlagSet) []string {
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
			if eq := strings.Index(name, "="); eq < 0 && takesValue(fs.Lookup(name)) {
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
//
// The first read happens before the window exists, so a rate-limited launch is
// not an exit: while a refusal's recorded reset is still ahead, watch waits for
// that instant and reads again, and only a snapshot actually read is recorded
// or returned. A refusal without a usable reset, and every other first-read
// error, return to main unchanged for today's one-line exit.
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
	for err != nil {
		// A window meant to be left running must start on its own, so a first
		// read refused with a recorded reset still ahead waits out GitHub's
		// read budget instead of exiting before the TUI exists. The wait is
		// the instant the transport recorded, never a guess, and it is
		// computed from the same now as the decision so it cannot fall
		// short of the reset. There is no ordinary pre-window retry, so
		// every other first-read error, including a refusal whose reset is
		// missing or already past, falls through to today's one-line exit.
		now := time.Now()
		if !live.ResetTimeOf(err).After(now) {
			return repo.Snapshot{}, nil, err
		}
		timer := time.NewTimer(live.RetryDelay(err, now, 0))
		select {
		case <-ctx.Done():
			timer.Stop()
			return repo.Snapshot{}, nil, ctx.Err()
		case <-timer.C:
		}
		snapshot, err = fetch(ctx)
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
