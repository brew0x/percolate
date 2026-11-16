# Visual language

| Mark | Repository object | Rule |
| --- | --- | --- |
| Branch name, `│`, `╵` | Branch | Lane, in snapshot branch order. Commits and PRs referencing missing branches introduce a lane. |
| `●` | Commit | Newest at top, descending timestamp; SHA breaks timestamp ties. Position is relative rank within its lane, not elapsed time. |
| `◇`, `─`, `▼`, `#N` | Pull request | Diamond at head, path toward base, arrow at base. PR number labels the path. All recorded states retain their path; state and reviews are in details. |
| `‹`, `›` | Pull request | Base lies beyond this viewport; path continues toward it. |
| `◌ ◔ ◑ ◕` | Running workflow run | Four phases, eight frames each, repeating every 32 frames. First half of cycle dims. Applies to any status other than completed. |
| `✓` | Completed successful workflow | Conclusion success. |
| `✕` | Completed failed workflow | Conclusion failure, timed_out, or startup_failure. |
| `−` | Other completed workflow | Cancelled, skipped, neutral, or another conclusion; never implies success. |
| `◆ tag` | Release | At the bottom of the lane containing its head commit. |
| `▣ environment` | Deployment | At the bottom of the lane containing its head commit; state in details. |
| Dim commit | Commit age | Older than 48 hours relative to snapshot now. No wall clock is consulted. |
| White, bold, underlined mark | Selected object | Selected mark draws last. Enter opens recorded details. |
| `PERCOLATE a–b/n` | Lane viewport | Visible lane range and total lane count. |

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
