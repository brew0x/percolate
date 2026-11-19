# Deterministic demo of Percolate replaying a recorded fixture.
# Regenerate the committed GIF with: make demo
#
# The committed demo/percolate.gif is generated from the same fixture by
# demo/generate.sh so CI can verify it stays in step with the renderer.

Output demo/percolate.gif

Set Shell "bash"
Set FontSize 16
Set Width 900
Set Height 520
Set Padding 20
Set Theme "Dracula"

Type "go run ./cmd/percolate render --fixture testdata/fixtures/small.json --frame 0 --size 80x24 --color=false"
Enter
Sleep 600ms

Type "for i in $(seq 1 31); do printf '\\033[H\\033[2J'; go run ./cmd/percolate render --fixture testdata/fixtures/small.json --frame $i --size 80x24 --color=false; sleep 0.12; done"
Enter
Sleep 4500ms
