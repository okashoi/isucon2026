# ISUReal score-max plan

Rewrite the remaining hot paths so europe and america spend most reads on local standbys and finish more post-to-friends-feed cycles. Keep Primary s4 plus sync standbys. Overlay cache and one-roundtrip writes. Do not hash-shard. Stack is PR-vis, PR-conn, PR-fof, PR-sse, PR-export, PR-img, PR-graph, PR-write, then optional PR-asia2.

## How to read this

One box is one unit of work. Every box names the evidence that checks it. A nested box is a sub-step of the box above it. Check a box only when its evidence exists, a file, a log line, a screenshot, a test run, or a SHA. The body is a how-to. The appendices explain and record.

The program runs `pstack/skills/poteto-mode/playbooks/autopilot-stack.md`. The operator lands every PR. Owners stop at STACK-READY. Agents never SSH. Contest-host commands run only on the host itself or by the operator. Makefile targets that SSH are out of agent scope.

Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

## Program checklist

### Arm the program

- [ ] State the protocol and this plan to the operator, then stop. Start execution only on the operator's explicit go.
- [ ] Read these from trunk at program start. Re-read them at every tick.
  - [ ] `git show origin/main:pstack/skills/poteto-mode/playbooks/autopilot-stack.md`
  - [ ] `git show origin/main:pstack/skills/swarm/SKILL.md`
  - [ ] `git show origin/main:.claude/skills/isubench/SKILL.md`
  - [ ] `git show origin/main:pstack/skills/poteto-mode/playbooks/opening-a-pr.md`
  - [ ] `git show origin/main:pstack/skills/poteto-mode/references/bugbot-triage.md`
  - [ ] `git show origin/main:docs/contest/manual.md`
  - [ ] `git show origin/main:docs/doc-idea.md`
- [ ] On the operator's go, arm the audit tick as `/loop 1h` with the tick prompt below. Never leave the cadence to memory.
- [ ] Use this tick prompt, verbatim. "Re-read the execution playbook from trunk. Audit the operation against it and fix drift in this tick. Probe every active lane and judge progress by side effects only. Stand down a stuck lane and dispatch its replacement now. Then post a short status message to the operator in chat only when the audit found a tracked change that no earlier status message reported, such as a PR opened, a code-ready head, a round launched or closed, a verdict, a merge, a stuck agent and the action taken, a blocker added or cleared, or a decision only the operator can make. Name every such change and nothing else. Do not repeat a table, the merged list, or an unchanged blocker. If the audit found none, end the turn with no reply text. Either way, log this tick's row in your decision trail. The row names the items reported, or none."
- [ ] On the operator's hold or stand-down, send every owner a zero-writes order at once.
- [ ] Hard ban. Never SSH. Never run `make replica/status`, `make logs/clear`, or any script that opens `ssh`. Live and perf evidence comes from the operator pasting `~/isubench/*` and pprotein GETs, or from `go test` on a laptop with a local Postgres.
- [ ] Before the first SCORE PR lands, set `go/initialize.go` `agent` / `harness` / `model` to the real tools. Today they still say `human` / `manual` / `brain`.

### Spawn owners

- [ ] Spawn one owner per PR with the full lifecycle the execution playbook names.
- [ ] Follow this dependency graph. Start dependent work only after its parent merges, or base it on the parent branch when the execution playbook stacks.
  - [ ] PR-vis and PR-conn are independent and first. Both branch from `main`.
  - [ ] PR-fof after PR-vis.
  - [ ] PR-sse after PR-conn.
  - [ ] PR-export after PR-vis.
  - [ ] PR-graph after PR-fof.
  - [ ] PR-write after PR-vis.
  - [ ] PR-img after PR-export. Flattens `image_objects` partitions.
  - [ ] PR-asia2 after PR-graph and PR-write. Skip unless Asia host CPU is free in pprotein.
- [ ] Hold the file boundaries. PR-vis touches only `sql/` and relationship Go files. PR-conn touches only `go/server.go` and Postgres conf under `infra/s4/`. PR-fof touches feed and viewer files. PR-sse touches notification files. PR-export touches export and image store read paths. PR-img touches `sql/` image schema and reset only. PR-graph touches relationship cache files and purge internals. PR-write touches remaining write handlers and `sql/reset.sql` functions. PR-asia2 touches deploy Makefile and asia2 unit files only.
- [ ] Hold the review gate. PR-vis and PR-fof change feed or friendship visibility. They wait for the operator's review in chat with screenshots and a video before merge.

### PR mechanics, for every PR

- [ ] Resolve the forge once. Default to `gh`; if `command -v origin` succeeds and Origin can resolve the repository, use `origin pr` for every PR operation. Record any fallback to `gh`. Never require `gt`.
- [ ] Open the PR ready, never draft, per **Opening a PR**. Use the run's built-in PR tool when it has one, else `origin pr create --status open --base <base-branch>` or `gh pr create --base <base-branch>` according to the resolved forge. A stack child targets its parent branch.
- [ ] Run the repo's lint and typecheck once before the PR-facing push. Push with hooks on.
- [ ] Run `/deslop` before each commit and `/no-comments` before review.
- [ ] Triage every Bugbot and security-reviewer comment per `../references/bugbot-triage.md`.
- [ ] Rebase onto current trunk before the code-ready report and babysit. Keep that merge base in fix rounds. Rebase again only at merge prep, on a `git merge-tree` conflict with trunk, or on a CI failure that comes from a change on trunk.

### Verdict and merge, for every PR

- [ ] A full swarm runs once, at the code-ready head. Another full swarm runs only for new behavior, a redesign, or a large new diff. A rebase, a merge-conflict resolution, or a minor fix gets a single verifier at the new head. Run each full swarm per `pstack/skills/swarm/SKILL.md`. One gates lane. The ten live lanes from the PR's **Verify, live** block. The perf lane from its **Verify, perf** block. Two or more audit lanes, each with its own focus, that read the diff and the receipts and distrust the PR body. The root audits the receipts in the merge-ready report before the verdict.
- [ ] Clean only when every lane is `PASS`. Findings go back to the owner, including a defect that a lane filed as a note. A new head gets a fresh verdict, except for results that stay valid under the patch-id rule in `playbooks/shipping.md`.
- [ ] Root appends each clean PR to the base-branch stack. The operator lands bottom-up. Owners never merge.

### Boot recipe, for every live lane

Each live lane runs on a local checkout or on a contest host shell the operator already opened. Drive through isubench and httptest. Never SSH from the agent.

- [ ] `git fetch origin <head-branch> && git checkout <head SHA>`.
- [ ] For unit-shaped lanes, start local Postgres if needed and `cd go && go test`. For contest lanes, the operator runs `make deploy` on the host and pastes `make isubench/check` output. Wait for `OK` or `NG` in that paste.
- [ ] Deliver input only through `go test`, httptest cases, or operator-pasted isubench. Read-only diagnostics are `~/isubench/last.json`, `~/isubench/diff.txt`, and pprotein httplog or slowlog TSV the operator pastes.
- [ ] Save every artifact to `/tmp/swarm-<pr-id>/worker-<n>/<slug>.txt` and return the paths with the report. Treat those files as the lane screenshots.

## Fix friend-request visibility (PR-vis)

**Depends on.** None.

**Files.**

- [ ] Edit `sql/reset.sql`.
- [ ] Edit `go/relationship.go`.
- [ ] Edit `go/relationship_list_api.go`.
- [ ] Create `go/friend_request_visibility_test.go`.

**Build.**

- [ ] Remove `synchronous_commit=local` from `create_friend_request` in `sql/reset.sql`, or make the accepting path and the incoming-request list read the Primary until remote_apply finishes.

**You see.**

- [ ] After a friend request from asia, europe `GET` incoming-requests returns the row before the create response returns success.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] `go/friend_request_visibility_test.go` gains a table case for create then list across two DB handles. Run `cd go && go test -run FriendRequestVisibility ./...`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `grok-4.7-high-fast` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Run create-friend-request then incoming-list at trunk and head. If trunk lacks the feature, record that and gate the cross-handle visibility plus the end state the user waits for. Save `vis-regression.txt`. Pass when head shows the request and trunk either matches or is documented as broken.
- [ ] Lane 2. Accept request then friends feed contains the peer. Save `vis-accept-feed.txt`. Pass when the peer post is visible.
- [ ] Lane 3. Reject request leaves no friendship. Save `vis-reject.txt`. Pass when friends feed omits the peer.
- [ ] Lane 4. Block after request hides the peer. Save `vis-block.txt`. Pass when status stays correct.
- [ ] Lane 5. Duplicate request returns the existing conflict shape. Save `vis-dup.txt`. Pass when status and body match trunk.
- [ ] Lane 6. Region mismatch still 403. Save `vis-region.txt`. Pass when status is 403.
- [ ] Lane 7. isubench check status and body for friend-request endpoints stay within allowed. Save `vis-isubench.txt`. Pass when compare prints OK.
- [ ] Lane 8. Logout and login still work after the SQL change. Save `vis-auth.txt`. Pass when login returns 200.
- [ ] Lane 9. Initialize clears pending requests. Save `vis-init.txt`. Pass when list is empty after initialize.
- [ ] Lane 10. Critical feed case with no unauthorized post. Save `vis-crit-feed.txt`. Pass when unauthorized posts stay absent.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Friend-request create `avg_ms` from isubench at trunk and head.
- [ ] Probe. Operator runs `make isubench/check` at trunk then head and pastes the friend-request row.
- [ ] Baseline. Record the trunk `avg_ms` first.
- [ ] Rule. Head must stay within 25 percent of trunk `avg_ms`. Fail if worse by more than 25 percent without a memo that remote_apply is the paid cost.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 1 and lane 7 screenshots into `tmp/media/PR-vis-review-vis-regression.txt` and `tmp/media/PR-vis-review-vis-isubench.txt`.
- [ ] Record a 30 to 60 second video of the change on a lane VM. Save it as `tmp/media/PR-vis-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at merge-ready. Wait for the operator's click.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends it to the base-branch stack and the operator lands it bottom-up.

## Cap primary connection budget (PR-conn)

**Depends on.** None.

**Files.**

- [ ] Edit `go/server.go`.
- [ ] Edit `infra/s4/etc/postgresql/16/main/conf.d/isucon.conf`.

**Build.**

- [ ] Lower `maxDBConns` so three app pools plus standby and admin slots fit under `max_connections`, and raise `max_connections` only if the operator measured exhaustion.

**You see.**

- [ ] App boots without `too many connections` under isubench check-full.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Add a comment-free assert in a small `go/server_conns_test.go` that `3*maxDBConns < 300` or the configured max. Run `cd go && go test -run MaxDBConns ./...`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `grok-4.7-high-fast` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Boot the app at trunk and head. Save `conn-boot.txt`. Pass when both ping Primary.
- [ ] Lane 2. Parallel httptest login storm on head. Save `conn-storm.txt`. Pass when no connection errors.
- [ ] Lane 3. isubench check OK. Save `conn-isubench.txt`. Pass when compare prints OK.
- [ ] Lane 4. Feed still 200. Save `conn-feed.txt`. Pass when status is 200.
- [ ] Lane 5. Post create still 201. Save `conn-post.txt`. Pass when status is 201.
- [ ] Lane 6. Image get still 200. Save `conn-image.txt`. Pass when status is 200.
- [ ] Lane 7. Notifications connect. Save `conn-sse.txt`. Pass when status is 200.
- [ ] Lane 8. Export still 200. Save `conn-export.txt`. Pass when status is 200.
- [ ] Lane 9. Initialize language field stays `go`. Save `conn-init.txt`. Pass when JSON has language go.
- [ ] Lane 10. ReadDB path still used when `ISUREAL_DB_READ_HOST` is set in the operator env paste. Save `conn-readhost.txt`. Pass when the paste shows the env.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. isubench overall `avg_ms` at trunk and head.
- [ ] Probe. Operator runs `make isubench/check` at trunk then head.
- [ ] Baseline. Record trunk overall `avg_ms` first.
- [ ] Rule. Head must not be more than 10 percent slower. Fail on connection errors in either paste.

**Review gate.** None. PR-conn is not review-gated.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends it to the base-branch stack and the operator lands it bottom-up.

## Raise friends-of-friends feed ceiling (PR-fof)

**Depends on.** PR-vis.

**Files.**

- [ ] Edit `go/feed_api.go`.
- [ ] Edit `go/post.go`.
- [ ] Create `go/feed_fof_test.go`.

**Build.**

- [ ] Keep author filter for ranked feed. Add a hard SQL or Go ceiling before `recommendationScores`, and push score order pagination without rescanning the whole active window when safe.

**You see.**

- [ ] Friends-of-friends first page returns at most 51 scored items and matches characterization fixtures.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] `go/feed_fof_test.go` gains table cases for visibility and pagination. Run `cd go && go test -run FeedFoF ./...`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `grok-4.7-high-fast` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Same friends feed page at trunk and head. Save `fof-friends-reg.txt`. Pass when item ids match after normalize.
- [ ] Lane 2. Friends-of-friends first page. Save `fof-page1.txt`. Pass when length is at most 50 and hasMore is consistent.
- [ ] Lane 3. Friends-of-friends second page. Save `fof-page2.txt`. Pass when no duplicate ids with page 1.
- [ ] Lane 4. Blocked author never appears. Save `fof-block.txt`. Pass when blocked id is absent.
- [ ] Lane 5. friends-only post hidden from non-friend FoF. Save `fof-vis.txt`. Pass when the post is absent.
- [ ] Lane 6. Public post from FoF appears. Save `fof-public.txt`. Pass when the post is present.
- [ ] Lane 7. isubench check OK. Save `fof-isubench.txt`. Pass when compare prints OK.
- [ ] Lane 8. Post then friends feed verify path. Save `fof-verify.txt`. Pass when the new post is visible to a friend.
- [ ] Lane 9. Empty FoF graph returns empty items. Save `fof-empty.txt`. Pass when items length is 0.
- [ ] Lane 10. Critical unauthorized post stays absent. Save `fof-crit.txt`. Pass when forbidden post is absent.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. `GET /api/feeds/friends-of-friends` `avg_ms` from isubench or alp at trunk and head.
- [ ] Probe. Operator runs `make isubench/check` or pastes alp rows for that path at trunk then head.
- [ ] Baseline. Record trunk `avg_ms` first.
- [ ] Rule. Head must be at least 20 percent faster, or absolute under 100ms if trunk lacked a stable FoF row. Fail if slower than trunk.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 1 and lane 8 screenshots into `tmp/media/PR-fof-review-fof-friends-reg.txt` and `tmp/media/PR-fof-review-fof-verify.txt`.
- [ ] Record a 30 to 60 second video of the change on a lane VM. Save it as `tmp/media/PR-fof-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at merge-ready. Wait for the operator's click.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends it to the base-branch stack and the operator lands it bottom-up.

## Stop notification polling (PR-sse)

**Depends on.** PR-conn.

**Files.**

- [ ] Edit `go/notification_api.go`.
- [ ] Edit `go/admin_posting_window_api.go`.
- [ ] Create `go/notification_sse_test.go`.

**Build.**

- [ ] Replace the 500ms DB poll with an in-process wait woken by posting-window start, using LISTEN or an HTTP fan-in that finishes before the create-window response returns.

**You see.**

- [ ] Connected SSE clients receive the new window event without a per-tick `users` and `posting_windows` query.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] `go/notification_sse_test.go` gains a case that a wake delivers one event. Run `cd go && go test -run NotificationSSE ./...`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `grok-4.7-high-fast` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Connect SSE then start a window at trunk and head. Save `sse-reg.txt`. Pass when both emit one event with the same fields after normalize.
- [ ] Lane 2. Region change disconnects. Save `sse-region.txt`. Pass when the stream ends.
- [ ] Lane 3. Deleted user disconnects. Save `sse-deleted.txt`. Pass when the stream ends.
- [ ] Lane 4. No event for windows that started before connect. Save `sse-old.txt`. Pass when no early event.
- [ ] Lane 5. Two clients in one region both fire. Save `sse-two.txt`. Pass when both get one event.
- [ ] Lane 6. Other region client stays quiet. Save `sse-other.txt`. Pass when that client gets no event.
- [ ] Lane 7. isubench check OK. Save `sse-isubench.txt`. Pass when compare prints OK.
- [ ] Lane 8. Initialize clears waiters. Save `sse-init.txt`. Pass when post-init connect waits cleanly.
- [ ] Lane 9. Abuse ban still blocks. Save `sse-ban.txt`. Pass when banned IP gets 403.
- [ ] Lane 10. Session required. Save `sse-auth.txt`. Pass when missing session is 401.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Slowlog count for the SSE poll SQL shape, or app CPU from pprof during idle SSE, at trunk and head.
- [ ] Probe. Operator pastes slowlog or pprof summary for a one-minute idle with N SSE clients at trunk then head.
- [ ] Baseline. Record trunk poll query count or CPU first.
- [ ] Rule. Head idle poll queries must be zero. Fail if the old 500ms query still appears in slowlog.

**Review gate.** None. PR-sse is not review-gated.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends it to the base-branch stack and the operator lands it bottom-up.

## Stream exports (PR-export)

**Depends on.** PR-vis.

**Files.**

- [ ] Edit `go/export_api.go`.
- [ ] Edit `go/post_image_store.go` if batch reads need a streaming helper.

**Build.**

- [ ] Write the zip to the response writer in order. Keep `posts.json` shape and `images/%06d.jpg` paths.

**You see.**

- [ ] Large export peaks stay flat in a local memory profile while the client receives a valid zip.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Add `go/export_stream_test.go` that checks zip member names and order against a fixture. Run `cd go && go test -run ExportStream ./...`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `grok-4.7-high-fast` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Export the same initial user at trunk and head. Save `export-reg.txt`. Pass when unzipped posts.json matches after normalize.
- [ ] Lane 2. Image bytes match initial data. Save `export-img.txt`. Pass when hashes match.
- [ ] Lane 3. Empty user export. Save `export-empty.txt`. Pass when zip has empty posts list.
- [ ] Lane 4. Deleted post omitted. Save `export-del.txt`. Pass when deleted id is absent.
- [ ] Lane 5. Order by posted_at then id. Save `export-order.txt`. Pass when order matches.
- [ ] Lane 6. Auth required. Save `export-auth.txt`. Pass when 401.
- [ ] Lane 7. isubench check OK. Save `export-isubench.txt`. Pass when compare prints OK.
- [ ] Lane 8. Memories still consistent with export. Save `export-mem.txt`. Pass when overlapping ids match.
- [ ] Lane 9. Initialize then export initial user. Save `export-init.txt`. Pass when critical error 5 fixtures hold.
- [ ] Lane 10. Concurrent export and feed. Save `export-concurrent.txt`. Pass when both return 200.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Peak RSS or alloc during export of a heavy initial user at trunk and head.
- [ ] Probe. Run the same export under `go test` with alloc metrics or operator pastes `avg_ms` for export.
- [ ] Baseline. Record trunk peak or `avg_ms` first.
- [ ] Rule. Head peak memory must drop at least 30 percent, or `avg_ms` must not worsen by more than 10 percent if memory tooling is missing.

**Review gate.** None. PR-export is not review-gated.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends it to the base-branch stack and the operator lands it bottom-up.

## Flatten image_objects partitions (PR-img)

**Depends on.** PR-export.

**Files.**

- [ ] Edit `sql/001-schema.sql`.
- [ ] Edit `sql/reset.sql`.
- [ ] Edit any Go query that assumes partition pruning on `created_at`.

**Build.**

- [ ] Replace partitioned `image_objects` with a plain table keyed by `id` only. Keep initialize and initial snapshot restore working.

**You see.**

- [ ] `GET /api/images/:imageId` plans hit one heap or index on `id`, and initial images still load after initialize.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Add a SQL or Go smoke that selects by `id` alone after reset. Run `cd go && go test -run ImageObjectPK ./...` or the reset smoke the owner adds.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `grok-4.7-high-fast` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Fetch the same initial image at trunk and head. Save `img-reg.txt`. Pass when bytes match.
- [ ] Lane 2. Post-origin image still serves. Save `img-origin.txt`. Pass when status is 200.
- [ ] Lane 3. Deleted post image stops within five seconds. Save `img-del.txt`. Pass when status becomes 403 or 404 after the grace.
- [ ] Lane 4. Bad signature stays 403. Save `img-sig.txt`. Pass when status is 403.
- [ ] Lane 5. Export after initialize matches initial fixtures. Save `img-export.txt`. Pass when critical error 5 fixtures hold.
- [ ] Lane 6. Memories images load. Save `img-mem.txt`. Pass when status is 200.
- [ ] Lane 7. isubench check OK. Save `img-isubench.txt`. Pass when compare prints OK.
- [ ] Lane 8. Profile image get. Save `img-prof.txt`. Pass when status is 200.
- [ ] Lane 9. RealMoji image get. Save `img-rm.txt`. Pass when status is 200.
- [ ] Lane 10. Initialize twice leaves images readable. Save `img-init2.txt`. Pass when second initialize still serves.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Image get `avg_ms` from isubench or alp at trunk and head.
- [ ] Probe. Operator runs `make isubench/check` at trunk then head and pastes the image row.
- [ ] Baseline. Record trunk `avg_ms` first.
- [ ] Rule. Head must be at least 10 percent faster on cold metadata lookups, or absolute under trunk. Fail if slower than trunk by more than 10 percent.

**Review gate.** None. PR-img is not review-gated.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends it to the base-branch stack and the operator lands it bottom-up.

## Cache friendship graph in memory (PR-graph)

**Depends on.** PR-fof.

**Files.**

- [ ] Create `go/graph_cache.go`.
- [ ] Edit `go/post.go`.
- [ ] Edit relationship write handlers to purge peers before the success response.
- [ ] Edit initialize purge internals.

**Build.**

- [ ] Keep friendships, blocks, and follows in process memory. On mutate, purge every app peer over the existing internal HTTP paths before returning success.

**You see.**

- [ ] Feed viewer context no longer hits friendships SQL on a warm cache, and a cross-region friend add is visible before the write response returns.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] `go/graph_cache_test.go` covers hit, purge, and initialize clear. Run `cd go && go test -run GraphCache ./...`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `grok-4.7-high-fast` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Friends feed after accept at trunk and head. Save `graph-reg.txt`. Pass when visibility matches.
- [ ] Lane 2. Accept then immediate remote feed. Save `graph-remote.txt`. Pass when the peer appears before timeout.
- [ ] Lane 3. Unfriend hides. Save `graph-unfriend.txt`. Pass when the peer disappears.
- [ ] Lane 4. Block hides both ways. Save `graph-block.txt`. Pass when both feeds hide.
- [ ] Lane 5. Follow affects following feed only. Save `graph-follow.txt`. Pass when following feed updates.
- [ ] Lane 6. Initialize clears cache. Save `graph-init.txt`. Pass when post-init state matches DB.
- [ ] Lane 7. isubench check OK. Save `graph-isubench.txt`. Pass when compare prints OK.
- [ ] Lane 8. Region mismatch still 403 on mutate. Save `graph-region.txt`. Pass when 403.
- [ ] Lane 9. Restart loses memory and reloads from DB. Save `graph-restart.txt`. Pass when first feed after restart is correct.
- [ ] Lane 10. Critical unauthorized post stays absent. Save `graph-crit.txt`. Pass when forbidden post is absent.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Friends feed `avg_ms` and friendships SQL count in slowlog at trunk and head.
- [ ] Probe. Operator pastes isubench and slowlog for friends feed at trunk then head.
- [ ] Baseline. Record trunk `avg_ms` and SQL count first.
- [ ] Rule. Head friendships SQL count on warm feed must fall at least 50 percent. Fail if feed `avg_ms` rises more than 10 percent.

**Review gate.** None. PR-graph is not review-gated.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends it to the base-branch stack and the operator lands it bottom-up.

## Collapse remaining write roundtrips (PR-write)

**Depends on.** PR-vis.

**Files.**

- [ ] Edit `sql/reset.sql` for remaining multi-statement writes.
- [ ] Edit `go/realmoji_api.go`, `go/comment_api.go`, and related handlers.
- [ ] Edit `go/profile_image_api.go` to keep bytes off the sync WAL when safe.

**Build.**

- [ ] Move RealMoji and comment create and delete into one-roundtrip SQL functions where locks already allow it. Stop inserting RealMoji JPEG `bytea` into Primary. Use the same local disk origin path as posts (`go/realmoji_api.go` still writes `bytea` today).

**You see.**

- [ ] RealMoji create does one Primary roundtrip, stores empty `data` with `origin_region`, and still scores on isubench.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Add handler characterization tests for RealMoji and comment create. Run `cd go && go test -run RealMojiOrComment ./...`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `grok-4.7-high-fast` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. RealMoji create at trunk and head. Save `write-rm-reg.txt`. Pass when bodies match after normalize.
- [ ] Lane 2. Comment create. Save `write-comment.txt`. Pass when 201 and body match.
- [ ] Lane 3. RealMoji replace. Save `write-rm-repl.txt`. Pass when only the new image serves.
- [ ] Lane 4. Comment delete. Save `write-cdel.txt`. Pass when list omits it.
- [ ] Lane 5. Unauthorized RealMoji rejected. Save `write-unauth.txt`. Pass when 403 or 404 per trunk.
- [ ] Lane 6. Profile image change. Save `write-prof.txt`. Pass when signed URL changes.
- [ ] Lane 7. isubench check OK. Save `write-isubench.txt`. Pass when compare prints OK.
- [ ] Lane 8. Window ended blocks create. Save `write-window.txt`. Pass when forbidden.
- [ ] Lane 9. Initialize restores initial RealMoji expectations. Save `write-init.txt`. Pass when memories or export fixtures hold.
- [ ] Lane 10. Post create still one roundtrip. Save `write-post.txt`. Pass when 201.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. RealMoji and comment `avg_ms` at trunk and head.
- [ ] Probe. Operator runs `make isubench/check` at trunk then head.
- [ ] Baseline. Record trunk `avg_ms` first.
- [ ] Rule. Head must be at least 15 percent faster on RealMoji create. Fail if slower.

**Review gate.** None. PR-write is not review-gated.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends it to the base-branch stack and the operator lands it bottom-up.

## Optionally run app on asia2 (PR-asia2)

**Depends on.** PR-graph.

**Files.**

- [ ] Edit `Makefile` deploy lists only if asia2 gains an app unit.
- [ ] Create or edit asia2 systemd unit under `infra/s4/` as needed.

**Build.**

- [ ] Serve Asia app traffic from asia2 only when pprotein shows asia1 CPU saturated and s4 CPU free. Keep the router path. Do not move regions.

**You see.**

- [ ] Asia requests still hit the router and return 200 from the asia2 unit.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] A dry-run script or Makefile target prints the intended hosts without SSHing. Run that target locally.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `grok-4.7-high-fast` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Asia feed at trunk and head. Save `asia2-reg.txt`. Pass when bodies match after normalize.
- [ ] Lane 2. Health of asia2 unit from operator paste. Save `asia2-health.txt`. Pass when active.
- [ ] Lane 3. isubench check OK. Save `asia2-isubench.txt`. Pass when compare prints OK.
- [ ] Lane 4. Europe still on europe1. Save `asia2-eu.txt`. Pass when region routing unchanged.
- [ ] Lane 5. America still on america1. Save `asia2-am.txt`. Pass when region routing unchanged.
- [ ] Lane 6. Initialize reaches all app peers. Save `asia2-init.txt`. Pass when initialize 200.
- [ ] Lane 7. Image cache fill still works. Save `asia2-img.txt`. Pass when image 200.
- [ ] Lane 8. Session purge peer list includes asia2. Save `asia2-purge.txt`. Pass when purge 204.
- [ ] Lane 9. Failover back to asia1 documented. Save `asia2-rollback.txt`. Pass when rollback steps exist in the PR body.
- [ ] Lane 10. Critical feed case green. Save `asia2-crit.txt`. Pass when unauthorized posts stay absent.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Official bench score or asia httplog p95 at trunk and head.
- [ ] Probe. Operator runs one short bench or pastes asia alp at trunk then head.
- [ ] Baseline. Record trunk score or p95 first.
- [ ] Rule. Head asia capacity must rise or p95 fall by at least 10 percent. Fail if score drops.

**Review gate.** None. PR-asia2 is not review-gated.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] The root appends it to the base-branch stack and the operator lands it bottom-up.

## Close the program

- [ ] Every box above is checked with its evidence.
- [ ] Reply to the operator with the report the execution playbook names.

## Appendix A. Prototype evidence

Open question on hash sharding versus Primary plus sync standbys. Settled by repo evidence only. No live bench. Choice is Primary s4 with sync standbys on s2 and s3, plus app caches. Kill criteria for dropping sync are in Appendix B. Source notes from [Topology and score path](66b624f2-cd8a-4c0b-99e5-1449fa095fd0).

Open question on friend-request `synchronous_commit=local` safety. Unproven at runtime. Code and manual conflict. PR-vis exists to settle it with a failing test first.

Open question on whether asia2 app helps. Unproven until pprotein shows free CPU on s4 and saturated asia1.

Remaining versus `docs/doc-idea.md`, from [Audit done vs remaining](b7e22950-fc8b-49e1-a5bf-71dedd623572). Done or mostly done are session and viewer, abuse BAN, middleware, and replica ReadDB. Still open in this stack are FoF ceiling, SSE poll, export buffer, RealMoji `bytea`, `image_objects` partitions, graph memory, and initialize declaration. Search `pg_trgm` stays parked unless user-search `avg_ms` shows up in alp.

Verification surface, from [Verification surface map](49d77943-ac8c-4410-9a65-bf45386aa202). Live truth is operator-host `make isubench/check`. Perf truth is pasted pprotein httplog and slowlog. Laptop `go test` covers unit only.

## Appendix B. Alternatives rejected

Hash or virtual-bucket sharding. Feeds, FoF ranking, and search cross users. Ownership would force scatter-gather on the score path.

Independent region Primary databases as the first move. Cross-region friendships need immediate visibility. That recreates sync cost with worse joins.

Drop sync standbys first. Only if a standby outage stalls all commits, restart recovery fails, or a controlled READ_HOST off A/B shows no europe and america score loss.

## Appendix C. Risks

PR-vis. Paying remote_apply on friend writes can raise latency. Watch isubench friend-request `avg_ms`.

PR-conn. Too low a pool stalls requests. Watch connection errors in isubench.

PR-fof. Wrong visibility is a critical FAIL. Characterization tests and review gate are mandatory.

PR-sse. Missed wake stalls the posting cycle. Keep a slow fallback path behind a flag until isubench is green.

PR-graph. Failed cross-region purge breaks immediate visibility. Purge must finish before the success response.

PR-img. Initialize and initial snapshot must keep every historical image. Watch critical error 5 after reset.

PR-asia2. Extra peer URLs can break initialize. Skip unless measured.

SSH ban. Agents that need host evidence ask the operator for pastes. They do not open SSH.

## Appendix D. Links and reading list

Read `docs/contest/manual.md`, `docs/doc-idea.md`, `docs/pprotein-api.md`, `.claude/skills/isubench/SKILL.md`, and `CLAUDE.md` before editing.

PR-vis and PR-fof get `pstack/skills/how/SKILL.md` before coding and `pstack/skills/interrogate/SKILL.md` if visibility disputes remain.

Decision trail follows `pstack/skills/show-me-your-work/SKILL.md` for the autonomous ticks.
