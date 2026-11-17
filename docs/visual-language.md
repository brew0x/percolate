# Visual language

| Mark | Repository object | Rule |
| --- | --- | --- |
| Branch name | Branch | Lane, in snapshot branch order. Commits and PRs referencing missing branches introduce a lane. |
| `│ ─ ╭ ╮ ╰ ╯ ├ ┤ ┬ ┴ ┼` in the commit region | Commit parent link | Paths connect recorded commits to known parents. Cross-lane parents form rounded fork or merge elbows at the parent row: each corner points back at the child commit and across at the parent. A cell carrying several recorded segments shows their union as a junction (`├` branch right, `┤` branch left, `┬` `┴` `┼` three-way), so no recorded path hides another. Missing parents draw no path. |
| `●` | Commit | Newest at top, descending timestamp; SHA breaks timestamp ties. Position is global commit rank, not elapsed time; parent endpoints share the same row across lanes. |
| `◇`, `─`, `▼`, `#N` | Pull request | Diamond at head, path toward base, arrow at base. PR number labels the path. All recorded states retain their path; state and reviews are in details. |
| `‹`, `›` | Pull request | Base lies beyond this viewport; path continues toward it. |
| `∿ #N` (fresh) | Open issue with a recorded closing reference | Small spring on the shelf above the pull request's head lane; `N` is the issue number. A dotted `⋮` feed runs down that lane into the pull request's flow, stopping above its diamond. |
| `⋮` | Closing reference feed | Recorded link from an issue to the pull request whose reference names it. Drawn only in empty cells, so commits and parent paths occlude it rather than merge with it. |
| `∿` (dim, far-left margin) | Open issue with no recorded closing reference | Sits quietly in the margin with no feed. A closed issue draws nothing. |
| `◌ ◔ ◑ ◕` | Running workflow run | Four phases, eight frames each, repeating every 32 frames. First half of cycle dims. Applies to any status other than completed. |
| `✓` | Completed successful workflow | Conclusion success. |
| `✕` | Completed failed workflow | Conclusion failure, timed_out, or startup_failure. |
| `−` | Other completed workflow | Cancelled, skipped, neutral, or another conclusion; never implies success. |
| `◆ tag` | Release | At the bottom of the lane containing its head commit. |
| `▣ environment` | Deployment | At the bottom of the lane containing its head commit; state in details. |
| Dim commit and parent path | Commit age or merged lane | Older than 48 hours relative to snapshot now, or a lane whose newest commit is a secondary parent of a commit on another lane. Paths dim if either endpoint dims. Where paths share a cell, the cell dims only when every path through it dims. No wall clock is consulted. |
| White, bold, underlined mark | Selected object | Selected mark draws last. Enter opens recorded details. |
| `PERCOLATE a–b/n` | Lane viewport | Visible lane range and total lane count. |
| New bead forming at the top of its lane | Commit added by a refresh | For 32 frames after a live snapshot changes, a commit absent from the previous snapshot drips down to its recorded rank; its parent paths follow. It draws dim for the first half, then takes its final style. A commit landing below the top starts at the top row; a newest commit (recorded rank 0) starts above the lane so it visibly falls in instead of sitting still. |
| Finishing run settling | Workflow run completed by a refresh | A run that was running in the previous snapshot and is completed in the new one keeps the frame-indexed pulse for the first half of the 32-frame change, then stops and draws its recorded outcome glyph. A run still running in the new snapshot keeps pulsing for the whole change; no run freezes on a still glyph mid-transition. |
| Object fading in place | PR, issue or commit gone after a refresh | An object present in the previous snapshot but absent from the new one draws dim in place through the 32-frame change, then leaves. Its lane is the same branch it belonged to; a lane removed in the new snapshot is kept under its own name for the fade, so nothing vanishes off-screen or drifts onto another branch. No closed state is invented. |

Only published releases enter the snapshot: a draft was never published and
draws no `◆`. A fork pull request's head lane is GitHub's fork-qualified label
(for example `alice:main`), so its commit never joins the upstream lane of the
same branch name.

CI sits two columns right of its commit. Releases or runs whose SHA is absent
use an explicit `unlinked` lane. Commit and PR paths use separate vertical
regions; releases collect at the bottom. Branch names and object labels truncate
to their lane width. Plain rendering preserves glyphs while omitting colors.

An issue feeds a pull request only when the snapshot records a closing
reference naming a pull request that is present; the scene never guesses a link
from issue text, numbering, or branch names. A dangling reference stays quiet
like an unlinked issue. Unlinked springs stack down the far-left margin from
the top; selecting one keeps its spring visible. No issue spring moves with the
animation frame. Several issues may close one pull request and share its head
lane: the spring shows the number drawn last (a selected issue draws after the
others), and a shorter number clears the cells left by a longer one, so no
fabricated issue number can appear. In a compact frame where the shelf would
meet the PR's diamond row or its number-label row, the spring sits above the
lane names and no feed is drawn; neither the diamond nor the label, including a
selected one, is ever overwritten.

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
No mark moves merely to decorate the landscape. Only recorded running CI pulses,
and only a recorded change between two snapshots animates: a live refresh draws
the 32-frame transition, then holds exactly the new snapshot. Fixtures and
`render` never animate a change — they draw one snapshot at the requested frame.

Lanes occupy only recorded commit paths, rather than full-height columns. A
merged lane ends at its merge path; no merge is inferred from PR state. A newer
commit after a merge keeps that lane active. Compressed rows may overlap paths;
rounded elbows remain evidence of recorded parent links, never branch-name guesses.
Details report only recorded fields: a run without a recorded job list says
`jobs not recorded` rather than a numeric zero, and a deployment without a
recorded state leaves it blank. A pull request shows at most the ten most
recent recorded reviews; older reviews are not present in the snapshot, so the
list is never a claim about the full review history.
