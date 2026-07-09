# Testing alternate.sh

This document describes the two test layers in this repository, how they work
internally, and the conventions to follow when extending them. Read it before
adjusting the tests.

| Layer | Location | Runs | What it covers |
|---|---|---|---|
| Go unit tests | `internal/shell/readline_test.go` | `go test ./internal/shell/` | Line-editing correctness against an emulated terminal |
| Go unit tests | `internal/presence/room_test.go` | `go test ./internal/presence/` | Talk room broker: join/invite/data-routing/leave/teardown semantics |
| Go unit tests | `internal/valid/valid_test.go` | `go test ./internal/valid/` | Username/password/email validation + disposable-domain blocking |
| Go unit tests | `internal/email/email_test.go` | `go test ./internal/email/` | SMTP message building (header-injection guard) + full send over a mock sink |
| Go unit tests | `internal/ratelimit/ratelimit_test.go` | `go test ./internal/ratelimit/` | Sliding-window per-key limiter |
| Go unit tests | `internal/av/*_test.go` | `go test ./internal/av/` | Media codecs (dither, PackBits, video key/delta, ADPCM): round trips, drop recovery, idle bitrate, shared-vector conformance, JS-mask parity |
| Go unit tests | `internal/calls/calls_test.go` | `go test ./internal/calls/` | Call signaling: ring/accept/hangup lifecycle, busy, ring timeout, rate limit, param clamping |
| Go unit tests | `internal/server/callws_test.go` | `go test ./internal/server/` | `/ws/call` media relay: auth, packet fan-out, source-spoof rejection, teardown both directions |
| Go unit tests | `internal/federation/call_test.go` | `go test ./internal/federation/` | Cross-node CALL_OPEN over real TLS: deferred accept, decline, cancel, decodable media across the bridge |
| JS codec parity tests | `web/js/test/codecs.test.mjs` | `node --test web/js/test/*.test.mjs` (also `nix build .#checks.<sys>.codecs-js`) | Browser codecs match `internal/av` byte-for-byte on `internal/av/testdata/vectors.json` |
| NixOS VM integration test | `nix/tests.nix` | `nix build .#checks.x86_64-linux.commands` | Every shell command, end to end, over real SSH with a real PostgreSQL |
| NixOS VM integration test | `nix/tests-signup.nix` | `nix build .#checks.x86_64-linux.signup` | Web self-signup → email → confirm (code + link) → login, with mailpit as SMTP sink |
| Go unit tests | `internal/federation/sync_test.go` | `go test ./internal/federation/` | Mail/news federation verbs over real TLS: MAIL_SEND (delivery, permanent rejection, vacation-in-response), NEWS_ARTICLE/CANCEL, NEWS_SINCE batching and mark resume |
| NixOS VM integration test | `nix/tests-federation.nix` | `nix build .#checks.x86_64-linux.federation` | Two-node peering, cross-node finger/rwho/talk, a full cross-node call (scripted web clients over `/ws/call`→ASSP), and federated mail & news: queued delivery + reply, MAILER-DAEMON bounce, cross-node vacation auto-reply, article propagation + threading + cancel, catch-up sync across a service restart, and local-namespace containment |

All run as part of `nix flake check` (the VM tests on Linux systems only —
NixOS VM tests cannot run on darwin).

**Codec parity convention:** the browser codecs (`web/js/*.js`) and the Go
twins (`internal/av/`) are locked together by the committed vectors in
`internal/av/testdata/vectors.json`. Any intentional codec change must be
made in both implementations, then regenerate vectors with
`go test ./internal/av -run TestVectors -update` and re-run both suites. The
blue-noise mask is generated (`go generate ./internal/av`) into both
languages at once; `TestBlueNoiseMaskParity` fails if they ever diverge.

---

## Layer 1: Go unit tests (readline)

### Why they exist

The readline (`internal/shell/readline.go`) went through a series of bugs
where redrawing a line that wrapped across terminal rows either duplicated
lines below the prompt or — much worse — moved the cursor up too far and
destroyed scrollback content *above* the prompt. The root cause of the final
regression was a **width mismatch**: the readline believed the terminal was 80
columns wide (the WebSocket session default before the browser's resize
message arrives) while the terminal was actually wider, so cursor-up
arithmetic based on assumed wrap points overshot.

The current design makes this class of bug structurally impossible:

1. Width is queried live per keystroke via `widthFn` (never snapshotted).
2. The readline **commits every wrap itself** by emitting `\r\n` at each
   width boundary during rendering. Rows therefore only exist because the
   readline created them, so cursor-up counts are exact by construction.
   If the width is *under*estimated, output wraps early (cosmetic). If
   *over*estimated, lines may duplicate below (cosmetic). Neither case can
   destroy content above the prompt.
3. Repositioning uses relative vertical moves (`CSI A`/`CSI B`) plus absolute
   column addressing (`CSI G`), which works across wrapped rows — bare
   left/right sequences (`CSI C`/`CSI D`) cannot cross a row boundary.

### The vterm emulator

`readline_test.go` contains `vterm`, a minimal terminal emulator implementing
exactly the behaviors readline output depends on:

- printable characters with **xterm-style deferred autowrap** (`wrapPending`:
  after writing the last column, the cursor parks on that cell until the next
  printable character commits the wrap — this is the subtle behavior that
  caused the original off-by-one bugs)
- `\r`, `\n`
- CSI sequences `A` (up, clamped at row 0), `B` (down), `G` (column absolute),
  `K` (erase to end of line), `J` (erase below)

The clamping of `CSI A` at row 0 is what lets tests detect the destructive
bug: an overshooting cursor-up lands on the sentinel row and the redraw
destroys it.

### Test conventions

Every test writes a `SENTINEL` line **above** the prompt and asserts it
survives — this is the core invariant. The `runReadline` helper takes
*separate* real and told widths so mismatch scenarios are first-class:

```go
runReadline(t, 168 /* real */, 80 /* told */, input)
```

Input is a raw byte script: printable chars, `\x7f` (backspace), `\x1b[D`
(left arrow), `\r` (enter), etc. The prompt constant matches production
(`ilios@alternate.sh:~$ `, 22 chars) so column arithmetic in tests mirrors
reality.

Current cases: wrap+backspace at matching width, wrap+backspace at
mismatched width (the historical bug), exact-boundary crossing both
directions, mid-line editing, mid-line editing across a wrap, Enter pressed
mid-line, history recall shrinking a wrapped line, and the no-width fallback
(must never emit cursor-up at all).

### Adding a case

Add a function using `runReadline` (or construct `Readline` directly for
multi-`ReadLine` scenarios like the history test). Always assert the
sentinel; assert returned line content; assert on-screen rows via `v.line(n)`
(row 0 = sentinel, row 1 = prompt row).

---

## Layer 2: NixOS VM integration test

### Architecture

`nix/tests.nix` is a `pkgs.testers.runNixOSTest` expression wired into the
flake as `checks.<system>.commands` (Linux only, via `optionalAttrs`). It is
deliberately built on **our own NixOS module** (`nix/module.nix`), so every
run also validates:

- module config generation (TOML from options)
- PostgreSQL provisioning (`createLocally`, peer auth over Unix socket)
- database migrations on service start
- systemd unit + SSH host key generation
- the real SSH server (port 2222) and web server (port 8080)

The VM gets `sshpass`, `curl`, the alternate-sh package, and the postgres
client in `systemPackages`.

### Fixtures

- **Users** are created with the real `adduser` CLI, run as the service user
  for peer auth: `runuser -u alternate-sh -- alternate-sh adduser --config
  /tmp/cli.toml ...`. The minimal CLI config (`/tmp/cli.toml`) contains only
  the DSN; everything else uses config defaults.
  - `alice` / `alicepass123` — **admin** (needed for `motd set`, `wall`)
  - `bob` / `bobpass12345` — regular user, counterpart for mail/news/write
  - `carol` / `carolpass123` — reserved for the `passwd` test, because that
    test permanently changes her password (to `carolnewpass1`). **Do not use
    carol in tests that run after the passwd subtest** unless you use the new
    password.
- **Fortunes** are replaced with a single `FORTUNE-CANARY` row via psql so
  `fortune` output is deterministic.
- **System messages** are injected via psql (there is no shell command to
  post them; `wall` is live-only).

### The session driver

```python
def sh(user, pw, lines):  # returns the full transcript
```

Joins `lines` with newlines, appends `logout`, base64-encodes the script (to
sidestep shell quoting entirely), and pipes it through
`sshpass ... ssh -tt -p 2222 user@127.0.0.1`. Because the REPL reads stdin
byte-by-byte and dispatches sequentially, pre-buffered input is consumed
deterministically: each line answers whatever prompt is next, whether that is
the shell prompt, a sub-REPL prompt (`mail`, `news`), or a field prompt
(`chfn`, `passwd`).

`machine.execute` is used instead of `machine.succeed` because the SSH exit
status after a server-side `logout` is not meaningful — assertions are done
on transcript content via `expect(out, *needles)`, which prints the full
transcript on failure.

`psql(query)` runs a query as the service user with `-tAc` (tuples only,
unaligned) and returns the stripped result — used for DB side-effect
assertions.

### ⚠️ The echo caveat (most important convention)

The readline **echoes typed input back into the transcript**. If a test types
`plan` / `PLAN-CANARY` / `.` and then asserts `PLAN-CANARY` in the same
session's output, the assertion passes vacuously on the echo, verifying
nothing.

**Rule: a canary is always asserted in a different session (or a different
user's transcript, or psql, or curl) than the one that typed it.**

The suite is structured around this:

- Session 1 (alice) *sets* chfn/plan/project/public/calendar and asserts only
  server confirmations (`Plan updated.`, `Finger information updated.`, ...).
- Session 2 (alice, fresh) runs `finger alice`, `calendar`, `public alice`
  and asserts the canaries — nothing canary-like is typed there.
- Mail/news canaries typed by alice are asserted in bob's reading sessions.
- `write`/`wall` canaries typed by alice are asserted in bob's captured
  background transcript.

Within one session it is safe to assert **server-generated framing** around a
canary if needed (e.g. `Project set to: ALICE-PROJECT-CANARY` — the echo of
the command is `project ALICE-PROJECT-CANARY`, without the `set to:` text),
but prefer the separate-session pattern.

### Multi-user live delivery (write / wall)

Real-time delivery needs the recipient online. The technique:

```python
machine.execute("sleep 12 | " + SSH.format(user="bob", ...) + " > /tmp/bob-bg.out 2>&1 &")
machine.sleep(3)                      # let bob's session register in the hub
... alice writes / walls ...
machine.sleep(11)                     # sleep ends → stdin EOF → bob logs out
bg = machine.succeed("cat /tmp/bob-bg.out")
```

`sleep N` as stdin produces no input but keeps the pipe (and therefore the
SSH session) open for N seconds; EOF then triggers a clean logout. Notices
arrive asynchronously via the presence hub and land in bob's transcript file.
The `machine.sleep` numbers matter: registration must complete before the
sender acts, and the file is only complete after the background session ends.

### Interactive input scripts (prompt sequences)

When scripting a command, the input lines must match the prompt order
exactly. Reference for every interactive command:

| Command | Input lines after the command |
|---|---|
| `chfn` | name, office, home phone (blank keeps current) |
| `passwd` | current password, new password, confirm new password |
| `plan` / `public` | body lines…, `.` |
| `project` (no args) | one line (blank clears) |
| `project <text>` | none (inline args form) |
| `calendar edit` | entry lines (`M/D description`)…, `.` (immediate `.` cancels) |
| `mail <user>` | subject, body lines…, `.`, `y`/`n` confirm |
| `mail` (mailbox sub-REPL) | any of: number, `d<n>`, `r<n>` (then reply compose: body…, `.`, `y`), `n`, `l`, `q` |
| `vacation msg` | body lines…, `.` |
| `vacation on` / `off` | none |
| `news` (sub-REPL) | group name → article-list sub-REPL: number (then one line consumed by the inline `[f/n/q]` prompt), `f<n>` (compose), `c<n>` (cancel: `y`/`n` confirm; author-only unless admin), `p` (compose: subject, body…, `.`, `y`), `m`, `q` → back at group list → `q` to exit |
| `post <group>` | subject, body lines…, `.`, `y` |
| `motd set` (admin) | body lines…, `.` |
| `wall` (admin, no args) | body lines…, `.` |
| `wall <text>` / `write <user> <text>` | none (inline args form) |
| `mesg y` / `n`, `biff y` / `n`, `who`, `w`, `last`, `uptime`, `fortune`, `motd`, `msgs`, `help`, `finger` | none |
| `talk <users…>` / `ytalk` | raw character stream once in the room; `\003` (Ctrl+C) leaves. Scripted input needs real-time pacing: `{ printf 'talk alice\n'; sleep 12; printf '\003'; }` as ssh stdin |

Note the news reading quirk: after displaying an article, an inline
`[f=followup, n=next unread, q=back]` prompt consumes **one** line; any
input other than `f`/`n` returns to the article list. Tests feed a throwaway
`x` there.

### Subtest inventory

Order matters — later subtests depend on earlier state (users' saved data,
changed MOTD, carol's changed password). Current sequence:

1. **banner/basics + editors** — login banner (hostname, seeded MOTD,
   fortune canary), `help`, `who`, `w`, `uptime`, `last`, `mesg` status,
   `fortune`, `motd`; sets chfn/project/plan/public/calendar; asserts only
   confirmations.
2. **profile verification** — fresh session: `finger`, `finger alice`,
   `calendar`, `public alice` assert all canaries; psql checks office/phone.
3. **web** — `curl /` (index served from the embedded frontend) and
   `curl /~alice` (public page contains canary).
4. **motd set + denials** — alice sets `MOTD-CANARY`; bob gets
   `motd set: permission denied` and `wall: permission denied`.
5. **msgs** — psql-injected `SYSMSG-CANARY`; login hint appears; first
   `msgs` shows it, second reports none (read-marking verified).
6. **mail send** — alice → bob; asserts confirmation + psql row count.
7. **mail read** — bob sees unread hint at login, subject in list, sender
   and body on read.
8. **vacation** — bob sets message + enables; alice's next mail triggers
   `[Auto-reply from bob received]`; alice reads the auto-reply and finds
   the vacation canary.
9. **vacation off + delete** — bob disables vacation, deletes message 1.
10. **news post** — alice posts in `alt.chat` via the browser flow.
11. **news read** — bob sees the subject in the article list and the body.
12. **news cancel** — bob (non-author, non-admin) is denied; alice cancels
    her own article; list reports none remain; psql confirms zero
    non-cancelled rows. *(Must run after bob's read and before anything
    that expects the alt.chat article to exist.)*
13. **post direct** — alice posts to `alt.dreams.computing`; psql row count.
14. **write/wall live** — background bob captures `Message from alice` +
    canary and `Broadcast from alice` + canary; `write` to offline carol
    fails; `wall` reports `sent to 1 user`.
15. **mesg persistence** — `mesg n` persists (psql `mesg_on = f`), blocks
    `write` against a fresh background session, `mesg y` restores. *(This
    subtest exists because `cmdMesg` originally forgot the DB write — the
    setting silently reverted on logout. Regression guard.)*
16. **biff live** — background bob at his prompt receives
    `New mail from alice` with the subject when alice sends mail.
17. **biff toggle** — `biff n` persists (psql), `biff y` restores. Leave
    biff ON afterwards or the live test breaks on reordering.
18. **talk** — bob idles at his prompt (receives the invitation notice),
    answers `talk alice`, alice's typed canary renders on bob's screen,
    alice's Ctrl+C shows `alice (left)` on bob's side. Ordering within the
    subtest is delicate: bob's session must exist *before* alice initiates
    (or she aborts with "no one to talk to"), and all output assertions go
    through `clean()` because the talk screen is drawn with cursor-addressing
    escapes. Real-time pacing comes from `sleep` between `printf`s in the
    stdin pipeline.
19. **passwd** — carol changes her password; new password logs in (prompt
    asserted); old password is rejected (nonzero ssh exit).
20. **last** — shows records for all three users.

### Timing notes

- The whole script runs in ~40s inside the VM; the two background-session
  subtests account for ~24s of that (fixed `sleep` windows).
- bcrypt verification adds ~100–300ms per login; ~25 sessions total.
- If a new subtest needs a background session, follow the sleep pattern:
  3s after starting it (hub registration), and wait out the stdin sleep
  plus a margin before reading the capture file.

### Gotchas

- **Nix flakes only see git-tracked files.** After creating a new file
  referenced by the flake (like `nix/tests.nix`), `git add` it or evaluation
  fails with "path is not tracked by Git".
- The transcript contains ANSI escapes (`\r\x1b[K` from readline renders).
  Assert on substrings of *server-printed* lines, which are plain text; never
  assert on exact whole-line matches.
- Canary strings must avoid shell/regex-meaningful characters — stick to
  `UPPERCASE-WITH-DASHES`. Input goes through base64 so quoting is safe, but
  assertion readability matters.
- The `calendar` upcoming-window test computes a date 7 days out in the test
  script (`datetime.now() + timedelta(days=7)`) so it always lands inside the
  30-day "upcoming" window regardless of when the test runs. Entries beyond
  30 days display under a separate heading instead.
- `checks.commands` requires KVM (or falls back to slow emulation). It is
  excluded on darwin systems.
- The MOTD shown at login comes from the **database** (seeded by migration
  001 as `Welcome to alternate.sh`), not from the module's `motd` option —
  don't be surprised that the module option has no effect on the banner.
  After subtest 4 the banner shows `MOTD-CANARY`.
- Talk rooms are keyed by the **sorted participant set** (`talk:alice+bob`),
  so `talk bob` from alice and `talk alice` from bob deterministically meet.
  ytalk is the same command with more names (`ytalk` is an alias of `talk`).
  The invitation notice tells the invitee the exact command to type.
- Notification gating: `mesg` gates write and talk invitations, `biff` gates
  new-mail alerts, `wall` is always delivered (changed in Phase 4 — walls
  used to respect mesg).

### Adding a new command test

1. Find the prompt sequence (read the `cmd_*.go` handler; update the table
   above).
2. Choose a canary and decide where it can be asserted **without** the echo
   problem (fresh session, other user, psql, or curl).
3. Insert the subtest respecting the ordering dependencies listed above; if
   it mutates durable state (passwords, mesg, vacation), either restore the
   state or document the dependency here.
4. Run `nix build .#checks.x86_64-linux.commands -L` and check the subtest
   timing output.

---

## Layer 3: signup VM test (`nix/tests-signup.nix`)

A separate VM check (`checks.<system>.signup`) for the public self-signup flow,
because it needs an SMTP sink and email config the commands test doesn't.

**Mail sink:** `mailpit` is launched in the test script (`--smtp 127.0.0.1:1025
--listen 127.0.0.1:8025`) and the service is configured with `email.enable`
pointing at it (no auth, since mailpit is an open sink). The confirmation email
is read back through mailpit's HTTP API (`/api/v1/search?query=to:<addr>` →
`/api/v1/message/<id>` → `.Text`), and the 6-digit code / `token=` link are
regex-extracted from the body to complete confirmation.

**Simulating distinct client IPs:** the handlers derive the client IP from the
`X-Real-IP` header (set by our nginx in production). The test sends
`X-Real-IP: 10.0.0.N` per request so each sub-test has an independent rate-limit
bucket, and the rate-limit sub-test deliberately reuses one IP (`10.9.9.9`) to
trip the 3-signups/hour cap on the 4th request. **When adding signup sub-tests,
give each an unused `X-Real-IP` or you'll consume another test's rate budget.**

**Sub-tests:** confirm-by-code + login; confirm-by-link + login; disposable
email rejected (400); invalid username rejected (400); duplicate username
rejected (409); per-IP rate limit (4th → 429); wrong-code lockout (5 bad codes
destroy the pending row, then even the right code 404s).

**Ordering note:** the disposable/invalid sub-tests rely on validation
happening before the pending row is created, so they leave no state. The
duplicate-username test depends on the confirm-by-code test having created
`zoe`.

---

## Running everything

```sh
# Go unit tests (fast, run these on any readline/validation change)
nix develop --command go test ./... 

# Full command integration suite (~2–4 min including VM build)
nix build .#checks.x86_64-linux.commands -L

# Signup flow suite
nix build .#checks.x86_64-linux.signup -L

# Everything the flake knows about
nix flake check
```
