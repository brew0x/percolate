# Visual language

| Mark | Repository object | Rule |
| --- | --- | --- |
| Branch name | Branch | Lane, in snapshot branch order. Commits and PRs referencing missing branches introduce a lane. |
| `│ ─ ╭ ╮ ╰ ╯ ├ ┤ ┬ ┴ ┼` in the commit region | Commit parent link | Paths connect recorded commits to known parents. Cross-lane parents form rounded fork or merge elbows at the parent row: each corner points back at the child commit and across at the parent. A cell carrying several recorded segments shows their union as a junction (`├` branch right, `┤` branch left, `┬` `┴` `┼` three-way), so no recorded path hides another. Missing parents draw no path. |
| `●` | Commit | Newest at top, descending timestamp; SHA breaks timestamp ties. Position is global commit rank, not elapsed time; parent endpoints share the same row across lanes. |
| `◇`, `─`, `#N` | Pull request | Diamond at head, path toward base, review mark at the base end. PR number labels the path. All recorded states retain their path; PR state, reviews and the resolved review state are in details. |
| `▽` `▼` `⊗` at the base end of a pull request's flow | The pull request's recorded reviews, resolved | The convergence mark reports the review state of the flow where it meets the base lane: `▽` waiting (no recorded review, or no reviewer's latest recorded review approves or requests changes), `▼` approved, `⊗` changes requested. The snapshot's review order is the recorded history: the last recorded review per reviewer decides that reviewer's say, no wall clock is consulted, and reviewers who decided both ways leave the flow at changes requested. A review with no attributable author (a deleted reviewer) has no reviewer to hold a say, so it never decides; two of them never share a slot where a later approval could hide an earlier request. The mark sits exactly where the flow meets the base; the waiting and approved arrows keep pointing into the base and differ only in fill, while changes requested crosses the convergence out. The mark uses the pass palette when approved and the fail one when changes are requested, and the flow's own style while waiting. A pull request forming in or fading out during a change draws the mark dim with the rest of its flow; the pass and fail palettes colour settled flows only. A base beyond the viewport keeps the plain `‹`/`›` indicator, and a selected pull request keeps the selected style on its whole flow. When compressed rows make several pull requests converge on one base cell with differing review states, no single arrow speaks for them: that cell keeps the plain path run and each state stays in its pull request's details, so one flow is never credited with another's mark; flows that agree there keep the shared arrow. A convergence mark also never replaces another pull request's head diamond: when a compressed row puts a flow's base on a different PR's head lane, the diamond stays and the converging PR's state lives in its details, whichever PR is selected. A departing diamond draws with the fading flow, beneath every surviving path, so it never suppresses a surviving flow's convergence mark: that flow's own path has already covered the cell and the mark belongs there. Selection does not claim a disputed cell either — drawing last would make one flow's arrow read as another's when color is disabled — but the selected diamond, path and number keep the selected style. A flow fading out still counts at that cell while its diamond and path are visible, so a settled flow never wears a departing flow's state. Every flow's path, including the selected one, is laid down before any mark, so a later path crossing a shared row cannot erase an earlier flow's arrow, diamond or number label. During a change the whole fading flow — paths and marks — is laid down before the surviving flows' paths, so a departing review mark never crosses out a surviving flow's path mid-route; it shows only where nothing surviving covers it. |
| `‹`, `›` | Pull request | Base lies beyond this viewport; path continues toward it. |
| `∿ #N` (fresh) | Open issue with a recorded closing reference | Small spring on the shelf above the pull request's head lane; `N` is the issue number. A dotted `⋮` feed runs down that lane into the pull request's flow, stopping above its diamond. The layer draws last but only fills cells that are empty or its own: a commit dripping through the shelf during a change keeps the cell, and the spring reappears whole once the cell frees. |
| `⋮` | Closing reference feed | Recorded link from an issue to the pull request whose reference names it. Drawn only in empty cells, so commits and parent paths occlude it rather than merge with it. A pull request that is leaving the scene answers only its own fading issue's reference; a departed pull request never carries a surviving issue's feed. |
| `∿` (dim, far-left margin) | Open issue with no recorded closing reference | Sits quietly in the margin with no feed. A closed issue draws nothing. |
| `◌ ◔ ◑ ◕` | Running workflow run or job | Four phases, eight frames each, repeating every 32 frames. First half of cycle dims. Applies to any status other than completed. |
| `✓` | Completed successful workflow or job | Conclusion success. |
| `✕` | Completed failed workflow or job | Conclusion failure, timed_out, or startup_failure. |
| `−` | Other completed workflow or job | Cancelled, skipped, neutral, or another conclusion; never implies success and never uses the fail palette. |
| Job cells two columns right of a commit | A workflow run's recorded jobs | A run with a recorded job list breaks into one cell per job in snapshot order; each uses its own recorded status and conclusion, so a running job pulses while a finished job beside it holds its outcome. A run without a recorded job list keeps one aggregate mark. If more jobs are recorded than fit before the next lane, the last cell carries a failure mark when a displaced job failed, otherwise the run's own outcome, so a hidden failure is never invisible. |
| `◆ tag` | Release | At the bottom of the lane containing its head commit, gathering the deployments that record the same head SHA. Releases with no recorded SHA use an `unlinked` lane. |
| `▤ name` | Published release artifact | One row above its release per recorded artifact, nearest the release first in snapshot order, using the release's lane and style. A row at or above the commit region is not claimed, and an artifact whose whole run is occupied by a path or flow is left out of the strip; the full list is in the release's details. A release with no recorded artifacts says so there rather than drawing an empty name. |
| `─▣ environment` | Deployment flowing out of a release | On the release's bottom row, one branch per deployment in snapshot order, joined to `◆` by a `─` connector. The link comes only from an equal recorded head SHA; the first release in snapshot order owns a shared commit. A deployment with no matching release instead keeps a standalone `▣ environment` at the lane bottom, drawn only where that row is free. If the named strip does not fit, environment names are dropped first so every deployment keeps its own status-coloured `▣`; omitted deployments stay in details and navigation. |
| `!` below a release | More recorded deployments than the strip can hold | Drawn only when even status-only branches do not fit before the next lane. It takes the fail palette when any omitted deployment failed, so a hidden failure is never invisible; otherwise it is dim. Visible branches keep their own recorded colours. |
| Dim commit and parent path | Commit age or merged lane | Older than 48 hours relative to snapshot now, or a lane whose newest commit is a secondary parent of a commit on another lane. Paths dim if either endpoint dims. Where paths share a cell, the cell dims only when every path through it dims. No wall clock is consulted. |
| White, bold, underlined mark | Selected object | The cursor moves with arrows, tab, mouse wheel, or by clicking/hovering a drawn mark; it never names an object that is not there. Selected mark draws last. Enter opens a panel listing the object's recorded fields and its GitHub URL when a repository is known; a replay says it has no URL rather than inventing one. `o` opens that URL in the browser. A refresh keeps the same object selected when it still exists, so the highlight and any open panel never jump to a different object on their own. |
| `PERCOLATE a–b/n` | Lane viewport | Visible lane range and total lane count. |
| `«`, `»` at the edge of the lane-name row | Lanes beyond the viewport | One dim mark stands for the whole run of offscreen lanes on that side: `«` to the left when scrolled right, `»` to the right while lanes remain. Drawn only when such lanes exist, in the reserved edge columns so no lane name is covered; the header above gives their exact count. It is a viewport cue, not a repository object: no lane is invented and no object is aggregated. |
| New bead forming at the top of its lane | Commit added by a refresh | For 32 frames after a live snapshot changes, a commit absent from the previous snapshot drips toward its recorded row; its parent paths and any CI anchored to it follow. It draws dim for the first half, then takes its final style. A commit landing below the top starts at the top row; a newest commit (recorded rank 0) starts one row above the commit region, clear of the lane names, so it visibly falls in instead of sitting still. The offset applies even to a repository's first commit, where rank alone is ignored. |
| Finishing run settling | Workflow run completed by a refresh | A run that was running in the previous snapshot and is completed in the new one keeps the frame-indexed pulse for the first half of the 32-frame change, then stops and draws its recorded outcome glyph. Its recorded jobs settle with it: a job unfinished in the previous snapshot pulses alongside the run, while a job already completed keeps its recorded outcome throughout. A run still running in the new snapshot keeps pulsing for the whole change; no run freezes on a still glyph mid-transition. |
| Object fading in place | PR, issue or commit gone after a refresh | An object present in the previous snapshot but absent from the new one draws dim in place through the 32-frame change, then leaves. Its lane is the same branch it belonged to; a lane removed in the new snapshot is kept under its own name for the fade, so nothing vanishes off-screen or drifts onto another branch. Only real lanes are kept: a mark that has no lane of its own (an unlinked issue's margin spring, or any mark's unused base) never brings a removed branch back. Fading marks draw beneath the surviving ones, so a cell shared with something that stays belongs to what stays — including a surviving flow's path under a departing review mark — except a convergence cell joining differing review states, which keeps its plain path until the fading flow is gone. No closed state is invented. |

Only published releases enter the snapshot: a draft was never published and
draws no `◆`. A fork pull request's head lane is GitHub's fork-qualified label
(for example `alice:main`), so its commit never joins the upstream lane of the
same branch name.

CI sits two columns right of its commit; a run with recorded jobs extends that
strip one cell per job in snapshot order. Releases or runs whose SHA is absent
use an explicit `unlinked` lane. Commit and PR paths use separate vertical
regions; releases collect at the bottom with their artifacts stacked above and
their deployments flowing out along the release row. Branch names and object
labels truncate to their lane width. Plain rendering preserves glyphs while
omitting colors.

A deployment's recorded state colours its whole branch: `success` uses the pass
palette, `failure` and `error` the fail palette, and `queued`, `pending` and
`in_progress` the in-flight palette. Any other or unrecorded state keeps the
neutral deployment palette, so missing status never reads as success. The glyph
does not pulse: only recorded running CI has a moving glyph. A deployment that
is forming or leaving a refresh draws dim with its branch, whatever its state.

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

At scale the frame shows as many lanes as fit with a readable column each. A
landscape of at most twelve lanes splits its columns evenly. A larger one
ranks its lanes by recorded activity and shares the spare columns by that
rank: a lane carrying more marks gets a wider column for its beads, flows and
labels, while a quiet lane keeps a ten-column minimum and sits with its quiet
neighbours as a soft bundle. Ties keep branch order. Lanes beyond the viewport
are one dim edge mark (`«` or `») standing for the whole offscreen run, with the
exact count in the header; they are a viewport cue, not invented lanes. The
activity count is the marks Build resolved for the lane, so no object is
aggregated or invented and the same snapshot always lays out the same way.
Static renders show the first lanes; navigating marks in the TUI reveals their
head lane. Every PR remains in the scene, including offscreen ones. Dense marks
may share cells: snapshot order wins among marks, then the selected mark draws
last; every flow path, including the selected one, is laid down before any mark,
so a crossing path cannot erase a mark. A convergence arrow likewise leaves
another PR's head diamond intact when a compressed row puts them on one cell;
that PR's state stays in its details. A base cell joining PR flows with
differing review states is the one exception — it keeps the plain path rather
than a single PR's arrow, even when one of the flows is selected; that flow
still keeps its diamond, path and number in the selected style. This is occlusion, not
aggregation or fabricated activity. Navigation reaches every
mark even when its original cell is occupied. During a change, fading marks
draw beneath the surviving ones so the settled state keeps shared cells, except
a convergence cell joining differing review states, which stays plain until the
fading flow leaves; the
issue layer only fills cells that are empty or its own. Vertical positions
compress to available rows; very small terminals show only the title. Resize
recomputes the same scene at the new dimensions.

Palette IDs: 0 plain, 1 lane, 2 fresh, 3 faded, 4 pass, 5 fail, 6 in-flight
(pulse) deployment or running CI, 7 release and neutral deployment, 8 selected.
A frame is the runes **and** these IDs.
No mark moves merely to decorate the landscape. Only recorded running CI pulses,
and only a recorded change between two snapshots animates: a live refresh draws
the 32-frame transition, then holds exactly the new snapshot. Fixtures and
`render` never animate a change — they draw one snapshot at the requested frame.

A failed live read keeps the last scene and adds `refresh failed: <reason>;
retrying` to the footer until a successful read clears it. The reason is the
failed read's own message, flattened to one printable line; no outcome is
guessed and no new mark is drawn. When that full wording does not fit, the
prefix and retry note are dropped before the reason is, so an actionable detail
such as a rate-limit reset time stays readable. Status and key hints share one
row when they fit; a long status takes its own row above the controls rather
than pushing them off screen. The hints stay together and lose detail only in a
narrow terminal, and the browser and quit keys survive whenever there is room
for them.

Lanes occupy only recorded commit paths, rather than full-height columns. A
merged lane ends at its merge path; no merge is inferred from PR state. A newer
commit after a merge keeps that lane active. Compressed rows may overlap paths;
rounded elbows remain evidence of recorded parent links, never branch-name guesses.
Details report only recorded fields: a run without a recorded job list says
`jobs not recorded` rather than a numeric zero, a release without recorded
artifacts says `artifacts not recorded` instead of an empty name, and a
deployment without a recorded state reports `state: not recorded` rather than
leaving the field blank. A release lists each
recorded artifact name even when the bottom strip could not fit it. A run that records failing jobs names them in
its details (`failing: test, lint`), matching the fail cells in its job strip. A pull request lists its recorded reviews and
adds the resolved review state under `resolved` (`resolved: waiting`,
`resolved: approved`, `resolved: changes requested`) — the same fact its
convergence mark draws. The `reviews` field above it holds the recorded
`reviewer: state` list, not the resolved word. A pull request shows at most the ten most
recent recorded reviews; older reviews are not present in the snapshot, so the
list is never a claim about the full review history.
