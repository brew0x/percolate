// Command cmd writes fictional fixtures relative to the repository root.
package main

import (
	"encoding/json"
	"github.com/brew0x/percolate/internal/repo/fixturegen"
	"os"
	"path/filepath"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			panic("go.mod not found")
		}
		root = parent
	}
	small := fixturegen.Generate(8, 3, 12, 1, 2)
	small.Runs[1].Conclusion = "failure"
	small.Runs[1].Jobs[0].Conclusion = "failure"
	for name, s := range map[string]any{"small": small, "busy": fixturegen.Generate(42, 40, 300, 120, 200)} {
		data, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			panic(err)
		}
		if err = os.WriteFile(filepath.Join(root, "testdata/fixtures", name+".json"), append(data, '\n'), 0644); err != nil {
			panic(err)
		}
	}
}
