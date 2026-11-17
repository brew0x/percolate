# Visual language

| Mark | Repository object | Rule |
| --- | --- | --- |
| Branch name | Branch | Lane, in snapshot branch order. Commits and PRs referencing missing branches introduce a lane. |
| `│ ─ ╭ ╮ ╰ ╯ ├ ┤ ┬ ┴ ┼` in the commit region | Commit parent link | Paths connect recorded commits to known parents. Cross-lane parents form rounded fork or merge elbows at the parent row: each corner points back at the child commit and across at the parent. A cell carrying several recorded segments shows their union as a junction (`├` branch right, `┤` branch left, `┬` `┴` `┼` three-way), so no recorded path hides another. Missing parents draw no path. |
| `●` | Commit | Newest at top, descending timestamp; SHA breaks timestamp ties. Position is global commit rank, not elapsed time; parent endpoints share the same row across lanes. |
| `◇`, `─`, `▼`, `#N` | Pull request | Diamond at head, path toward base, arrow at base. PR number labels the path. All recorded states retain their path; state and reviews are in details. |
| `‹`, `›` | Pull request | Base lies beyond this viewport; path continues toward it. |
| `◌ ◔ ◑ ◕` | Running workflow run | Four phases, eight frames each, repeating every 32 frames. First half of cycle dims. Applies to any status other than completed. |
| `✓` | Completed successful workflow | Conclusion success. |
| `✕` | Completed failed workflow | Conclusion failure, timed_out, or startup_failure. |
| `−` | Other completed workflow | Cancelled, skipped, neutral, or another conclusion; never implies success. |
| `◆ tag` | Release | At the bottom of the lane containing its head commit. |
| `▣ environment` | Deployment | At the bottom of the lane containing its head commit; state in details. |
| Dim commit and parent path | Commit age or merged lane | Older than 48 hours relative to snapshot now, or a lane whose newest commit is a secondary parent of a commit on another lane. Paths dim if either endpoint dims. Where paths share a cell, the cell dims only when every path through it dims. No wall clock is consulted. |
| White, bold, underlined mark | Selected object | Selected mark draws last. Enter opens recorded details. |
| `PERCOLATE a–b/n` | Lane viewport | Visible lane range and total lane count. |

Only published releases enter the snapshot: a draft was never published and
draws no `◆`. A fork pull request's head lane is GitHub's fork-qualified label
(for example `alice:main`), so its commit never joins the upstream lane of the
same branch name.

CI sits two columns right of its commit. Releases or runs whose SHA is absent
use an explicit `unlinked` lane. Commit and PR paths use separate vertical
regions; releases collect at the bottom. Branch names and object labels truncate
to their lane width. Plain rendering preserves glyphs while omitting colors.

At scale the frame shows as many lanes as fit with at least 14 columns per lane.
Static renders show the first lanes; navigating marks in the TUI reveals their
head lane. Every PR remains in the scene, including offscreen ones. Dense marks
may share cells: snapshot order wins, then the selected mark draws last. This
is occlusion, not aggregation or fabricated activity. Navigation reaches every
mark even when its original cell is occupied. Vertical positions compress to
available rows; very small terminals show only the title. Resize recomputes the
same scene at the new dimensions.

Palette IDs: 0 plain, 1 lane, 2 fresh, 3 faded, 4 pass, 5 fail, 6 pulse,
7 release/deployment, 8 selected. A frame is the runes **and** these IDs.
No mark moves merely to decorate the landscape. Only recorded running CI pulses.

Lanes occupy only recorded commit paths, rather than full-height columns. A
merged lane ends at its merge path; no merge is inferred from PR state. A newer
commit after a merge keeps that lane active. Compressed rows may overlap paths;
rounded elbows remain evidence of recorded parent links, never branch-name guesses.
Details report only recorded fields: a run without a recorded job list says
`jobs not recorded` rather than a numeric zero, and a deployment without a
recorded state leaves it blank. A pull request shows at most the ten most
recent recorded reviews; older reviews are not present in the snapshot, so the
list is never a claim about the full review history.
