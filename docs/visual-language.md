# Visual language

| Mark | Repository object | Rule |
| --- | --- | --- |
| Branch name | Branch | Lane, in snapshot branch order. Commits and PRs referencing missing branches introduce a lane. |
| `│ ─ ╭ ╮ ╰ ╯ ├ ┤ ┬ ┴ ┼` in the commit region | Commit parent link | Paths connect recorded commits to known parents. Cross-lane parents form rounded fork or merge elbows at the parent row: each corner points back at the child commit and across at the parent. A cell carrying several recorded segments shows their union as a junction (`├` branch right, `┤` branch left, `┬` `┴` `┼` three-way), so no recorded path hides another. Missing parents draw no path. |
| `●` | Commit | Newest at top, descending timestamp; SHA breaks timestamp ties. Position is global commit rank, not elapsed time; parent endpoints share the same row across lanes. |
| `◇`, `─`, `#N` | Pull request | Diamond at head, path toward base, review mark at the base end. PR number labels the path. All recorded states retain their path; PR state, reviews and the resolved review state are in details. |
| `▽` `▼` `⊗` at the base end of a pull request's flow | The pull request's recorded reviews, resolved | The convergence mark reports the review state of the flow where it meets the base lane: `▽` waiting (no recorded review, or no reviewer's latest recorded review approves or requests changes), `▼` approved, `⊗` changes requested. The snapshot's review order is the recorded history: the last recorded review per reviewer decides that reviewer's say, no wall clock is consulted, and reviewers who decided both ways leave the flow at changes requested. A review with no attributable author (a deleted reviewer) has no reviewer to hold a say, so it never decides; two of them never share a slot where a later approval could hide an earlier request. The mark sits exactly where the flow meets the base; the waiting and approved arrows keep pointing into the base and differ only in fill, while changes requested crosses the convergence out. The mark uses the pass palette when approved and the fail one when changes are requested, and the flow's own style while waiting. A pull request forming in or fading out during a change draws the mark dim with the rest of its flow; the pass and fail palettes colour settled flows only. A base beyond the viewport keeps the plain `‹`/`›` indicator, and a selected pull request keeps the selected style on its whole flow. When compressed rows make several pull requests converge on one base cell with differing review states, no single arrow speaks for them: that cell keeps the plain path run and each state stays in its pull request's details, so one flow is never credited with another's mark; flows that agree there keep the shared arrow. A flow fading out still counts at that cell while its diamond and path are visible, so a settled flow never wears a departing flow's state. Every flow's path is laid down before any mark, so a later path crossing a shared row cannot erase an earlier flow's arrow, diamond or number label. |
| `‹`, `›` | Pull request | Base lies beyond this viewport; path continues toward it. |
| `∿ #N` (fresh) | Open issue with a recorded closing reference | Small spring on the shelf above the pull request's head lane; `N` is the issue number. A dotted `⋮` feed runs down that lane into the pull request's flow, stopping above its diamond. The layer draws last but only fills cells that are empty or its own: a commit dripping through the shelf during a change keeps the cell, and the spring reappears whole once the cell frees. |
| `⋮` | Closing reference feed | Recorded link from an issue to the pull request whose reference names it. Drawn only in empty cells, so commits and parent paths occlude it rather than merge with it. A pull request that is leaving the scene answers only its own fading issue's reference; a departed pull request never carries a surviving issue's feed. |
| `∿` (dim, far-left margin) | Open issue with no recorded closing reference | Sits quietly in the margin with no feed. A closed issue draws nothing. |
| `◌ ◔ ◑ ◕` | Running workflow run | Four phases, eight frames each, repeating every 32 frames. First half of cycle dims. Applies to any status other than completed. |
| `✓` | Completed successful workflow | Conclusion success. |
| `✕` | Completed failed workflow | Conclusion failure, timed_out, or startup_failure. |
| `−` | Other completed workflow | Cancelled, skipped, neutral, or another conclusion; never implies success. |
| `◆ tag` | Release | At the bottom of the lane containing its head commit. |
| `▣ environment` | Deployment | At the bottom of the lane containing its head commit; state in details. |
| Dim commit and parent path | Commit age or merged lane | Older than 48 hours relative to snapshot now, or a lane whose newest commit is a secondary parent of a commit on another lane. Paths dim if either endpoint dims. Where paths share a cell, the cell dims only when every path through it dims. No wall clock is consulted. |
| White, bold, underlined mark | Selected object | Selected mark draws last. Enter opens recorded details. A refresh keeps the same object selected when it still exists, so the highlight and any open details never jump to a different object on their own. |
| `PERCOLATE a–b/n` | Lane viewport | Visible lane range and total lane count. |
| New bead forming at the top of its lane | Commit added by a refresh | For 32 frames after a live snapshot changes, a commit absent from the previous snapshot drips toward its recorded row; its parent paths and any CI anchored to it follow. It draws dim for the first half, then takes its final style. A commit landing below the top starts at the top row; a newest commit (recorded rank 0) starts one row above the commit region, clear of the lane names, so it visibly falls in instead of sitting still. The offset applies even to a repository's first commit, where rank alone is ignored. |
| Finishing run settling | Workflow run completed by a refresh | A run that was running in the previous snapshot and is completed in the new one keeps the frame-indexed pulse for the first half of the 32-frame change, then stops and draws its recorded outcome glyph. A run still running in the new snapshot keeps pulsing for the whole change; no run freezes on a still glyph mid-transition. |
| Object fading in place | PR, issue or commit gone after a refresh | An object present in the previous snapshot but absent from the new one draws dim in place through the 32-frame change, then leaves. Its lane is the same branch it belonged to; a lane removed in the new snapshot is kept under its own name for the fade, so nothing vanishes off-screen or drifts onto another branch. Only real lanes are kept: a mark that has no lane of its own (an unlinked issue's margin spring, or any mark's unused base) never brings a removed branch back. Fading marks draw beneath the surviving ones, so a cell shared with something that stays belongs to what stays, except a convergence cell joining differing review states, which keeps its plain path until the fading flow is gone. No closed state is invented. |

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
may share cells: snapshot order wins among marks, then the selected mark draws
last; every flow path is laid down before any mark, so a crossing path cannot
erase a mark. A base cell joining PR flows with differing review states is the
one exception — it keeps the plain path rather than a single PR's arrow. This is occlusion, not
aggregation or fabricated activity. Navigation reaches every
mark even when its original cell is occupied. During a change, fading marks
draw beneath the surviving ones so the settled state keeps shared cells, except
a convergence cell joining differing review states, which stays plain until the
fading flow leaves; the
issue layer only fills cells that are empty or its own. Vertical positions
compress to available rows; very small terminals show only the title. Resize
recomputes the same scene at the new dimensions.

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
recorded state leaves it blank. A pull request lists its recorded reviews and
adds the resolved review state (`reviews: waiting`, `reviews: approved`,
`reviews: changes requested`) — the same fact its convergence mark draws. A pull request shows at most the ten most
recent recorded reviews; older reviews are not present in the snapshot, so the
list is never a claim about the full review history.
