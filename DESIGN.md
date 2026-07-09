# alternate.sh — Design Document

> *An alternate history of the internet: what if the terminal never left?*

---

## 1. Vision

alternate.sh is a retro-future social network that imagines a world where Unix timeshare systems became the dominant computing paradigm — where BBS culture, social media, and instant messaging evolved inside text-based multiuser environments rather than graphical ones.

It is not a skin over a modern chat app. It is a genuine attempt to build the software that *would have existed* in that alternate timeline, running on the infrastructure of today.

---

## 2. Core Principles

- **Authenticity over convenience.** Commands, files, and concepts should feel like they came from real Unix culture, not like a Halloween costume of it.
- **Hostable by anyone.** Both the server and the web frontend ship as single Go binaries, configured via a single config file, deployable on a $5 VPS.
- **Federation-aware from day one.** Servers know about each other. Users on one node can see, message, and read news from users on others.
- **The web frontend is a convenience, not a different product.** It renders the same terminal experience a real SSH client would see.

---

## 3. Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│                     alternate.sh server                  │
│                                                          │
│  ┌──────────────┐   ┌──────────────┐   ┌─────────────┐ │
│  │  SSH Server  │   │   WebSocket  │   │  Federation │ │
│  │  (port 22)   │   │  (port 443)  │   │  Listener   │ │
│  └──────┬───────┘   └──────┬───────┘   └──────┬──────┘ │
│         │                  │                   │        │
│         └──────────────────┼───────────────────┘        │
│                            │                            │
│                    ┌───────▼────────┐                   │
│                    │  Session Layer │                   │
│                    │  (command REPL)│                   │
│                    └───────┬────────┘                   │
│                            │                            │
│         ┌──────────────────┼──────────────────┐         │
│         │                  │                  │         │
│  ┌──────▼──────┐  ┌────────▼──────┐  ┌───────▼──────┐  │
│  │  User Store │  │  Mail / News  │  │  Presence /  │  │
│  │  (profiles, │  │  (messages,   │  │  Events      │  │
│  │   dotfiles) │  │   newsgroups) │  │  (who, talk) │  │
│  └─────────────┘  └───────────────┘  └──────────────┘  │
│                            │                            │
│                    ┌───────▼────────┐                   │
│                    │   PostgreSQL   │                   │
│                    └────────────────┘                   │
└─────────────────────────────────────────────────────────┘
```

**Clients:**
- Any SSH client (OpenSSH, PuTTY, etc.) connecting to port 22 (or configured port)
- A web browser connecting to the web frontend, which streams terminal I/O over WebSocket

---

## 4. The Simulated Unix Environment

Users do not get a real shell. They enter a custom command REPL that presents the aesthetic and mental model of a Unix session without the security surface of one. There is no filesystem access beyond well-defined virtual paths, no arbitrary command execution, no pipes to real system binaries.

### 4.1 Virtual Filesystem

A small set of meaningful paths are stored as database records per user. There is no general-purpose VFS — only the paths that matter:

| Virtual Path | Purpose | Stored as |
|---|---|---|
| `~/.plan` | Finger status — "what you're working on" | text field |
| `~/.project` | Finger project — one-line current project | text field |
| `~/.signature` | Appended to outgoing mail | text field |
| `~/.hushlogin` | Suppress MOTD on login | boolean flag |
| `~/.newsrc` | News reading state (read/unread per group) | structured JSON |
| `~/.vacation.msg` | Auto-reply message when on vacation | text field |
| `~/.calendar` | Personal reminders | text lines |
| `~/public/` | User's public page, readable by all | text field (rendered verbatim) |
| `/etc/motd` | System message of the day | server config / DB record |

### 4.2 User Model

```
User {
  id          UUID
  username    string        // [a-z][a-z0-9_]{1,31}
  display_name string       // real name (chfn)
  email       string        // for account recovery only, never exposed
  pubkey      []string      // SSH public keys for passwordless login
  password    string        // bcrypt hash for password auth
  created_at  time.Time
  last_login  time.Time
  home_phone  string        // finger info (optional)
  office      string        // finger info (optional)
  mesg_on     bool          // whether write/talk is allowed
  vacation    bool          // whether vacation auto-reply is active
  admin       bool
}
```

### 4.3 Session & REPL

Each SSH or WebSocket connection creates a `Session` with:
- The authenticated user
- A PTY size (rows/cols)
- A context for cancellation
- A reference to the presence system (registering as "online")

The REPL loop:
1. Print prompt: `username@hostname:~$ ` (or customized per theme)
2. Read a line of input
3. Parse as `command [args...]`
4. Dispatch to the command handler
5. Write output back to the terminal
6. Repeat

A minimal readline implementation (history, cursor movement) is provided.

---

## 5. Command Reference

### 5.1 Identity & Discovery

**`finger [user[@host]]`**
The cornerstone social command. With no arguments, lists all logged-in users (like `who`). With a username, shows their profile.

```
Login: ilios                          Name: Ilios Cio
Directory: /home/ilios                Shell: /bin/sh
On since Fri Jul  4 09:23 (EDT) on pts/0, idle 0:02
Mail last read Fri Jul  4 08:55
No Plan.
```

With `~/.plan` set:
```
Plan:
Working on the federation layer for alternate.sh. Send me a note if you
want to run a node.
```

Supports cross-server lookup: `finger ilios@other.host`

**`chfn`**
Change finger info (name, office, phone). Interactive prompt.

**`passwd`**
Change password. Interactive.

**`pubkey [add|remove|list]`**
Manage SSH public keys for passwordless login.

---

### 5.2 Presence

**`who`**
Lists currently logged-in users.
```
ilios    pts/0   Jul  4 09:23  (192.168.1.1)
nova     pts/1   Jul  4 09:41  (web)
```

**`w`**
Extended `who` — shows what each user is currently doing (their current "area" of the system).
```
09:45:32 up 12 days, 3:22,  2 users,  load average: 0.01
USER     TTY   FROM          LOGIN@   IDLE  WHAT
ilios    pts/0 192.168.1.1   09:23    0:02  reading news (alt.dreams.computing)
nova     pts/1 web           09:41    0:00  writing mail
```

**`last [user]`**
Shows login history.
```
ilios    pts/0   Fri Jul  4 09:23   still logged in
ilios    pts/1   Thu Jul  3 22:11 - 23:45  (01:34)
nova     pts/0   Thu Jul  3 20:00 - 20:32  (00:32)
```

**`uptime`**
System uptime and load. In the alternate world, "load" is active user count.

---

### 5.3 Messaging

**`write <user> [message]`**
Send an immediate message to a logged-in user's terminal. If message is inline, sends it. If not, opens a line-input session (terminate with Ctrl+D or `.`).

Respects the recipient's `mesg` setting. Produces a notification like:
```
Message from ilios (pts/0) [09:47:12]...
Hey, did you see the new federation spec?
EOF
```

**`mesg [y|n]`**
Enable or disable receipt of `write` messages. Shows current state if no argument.

**`wall <message>`**
Admin-only. Broadcasts a message to all logged-in users.

**`msgs [-q]`**
Read queued system messages (lighter-weight than wall; users read at their own pace). `-q` for quiet mode (just shows count).

---

### 5.4 Mail

**`mail`** / **`mail <user[@host]>`**
The system mailer. With no arguments, opens the mailbox reader. With a recipient, composes a new message (interactive line-by-line, or piped).

Features:
- Threaded conversations (In-Reply-To header, stored in DB)
- `~/.signature` automatically appended
- Cross-server mail via federation (see §8)
- Mailing lists managed by admins (think early mailing list culture)

**`vacation`**
Enables vacation auto-reply. Sets `~/.vacation.msg`. The system auto-replies once per sender per 7-day window with the message.

**`vacation -m "I'm at a retreat until July 14th"`**
Set the message inline.

**`vacation -off`**
Disable vacation mode.

---

### 5.5 Real-time Communication

**`talk <user[@host]>`**
Opens a split-screen real-time conversation. The top half shows what the other user is typing (character by character, in the classic `talk` style — ephemeral, not logged). The bottom half is your input. Ctrl+C to end.

Cross-server talk is supported via federation.

Implementation note: uses a pub/sub channel in the session layer, not TCP between users. All I/O routes through the server.

**`ytalk <user1> [user2...]`**
Multi-party talk. Same split-screen model but with N participants, each in their own pane. Useful for small group coordination.

**`call <user[@host]>`**
Places a live audio/video call — the retro-futurism layer of §9. The terminal is the control surface: `call` rings the target, shows call status, and Ctrl+C hangs up; the actual media surface is the browser's phosphor-tinted call panel, which opens beside the terminal when the call connects (media is **web-only** — see §9.1). The recipient accepts by typing `call <caller>` back, mirroring talk's symmetric-join model, declines with `call -r <caller>`, or ignores it until the ring times out. `call -a` places an audio-only call. Over SSH, `call` explains that calls need the web client, and SSH recipients see a text notice ("ilios is calling — join on the web") — declining works from SSH too, since it's pure signaling. Respects `mesg n` and is rate-limited like `talk`.

---

### 5.6 News (Bulletin Boards)

The heart of alternate.sh's social life. Organized into newsgroups with hierarchical names:

```
alt.dreams.computing        (the main vision discussion)
alt.announce                (admin announcements, moderated)
alt.chat                    (general off-topic)
alt.games                   (games discussion and scores)
<hostname>.local            (node-specific local board)
<hostname>.intro            (new user introductions)
```

**`news`** / **`rn`** / **`nn`**
Enter the newsreader. Tracks read/unread state per group in `~/.newsrc`. Shows thread trees. Navigate with arrow keys or vi-style keys (j/k/n/p).

**`post [newsgroup]`**
Compose and post an article. Opens a simple line editor. Subject, body, then confirm.

**`followup`**
Reply to the current article in the same newsgroup.

**`cancel`**
Cancel (delete) one of your own articles.

News federation propagates over ASSP (see §8.4).

---

### 5.7 Public Pages

**`public`**
Edit your `~/public/` page — a plain text page visible to all users.

```
$ finger ilios
...
Public page: available. Type 'public ilios' to read.

$ public ilios
─────────────────────────────────────────────────────────
 ilios's public page — last updated Jul 4 2026
─────────────────────────────────────────────────────────
Welcome to my corner of the system.

I'm interested in retro computing, alternate history, and
distributed systems. Ask me about the federation spec.

Currently reading: Anathem (Neal Stephenson)
─────────────────────────────────────────────────────────
```

On the web frontend, public pages are also accessible via a URL: `https://hostname/~username`

---

### 5.8 System & Utility

**`motd`**
Re-display the message of the day.

**`fortune`**
Print a random quote from the system fortune database. Admins can submit fortunes. Community-contributed pool.

**`calendar`**
Read `~/.calendar` and show upcoming events. Format: `month/day description` per line. The system can optionally mail you reminders.

**`plan`**
Interactive editor for `~/.plan`. Opens a simple in-terminal text editor.

**`project`**
Set `~/.project` (one-line current project). Takes the line as an argument or prompts.

**`help [command]`**
The manual. With no arguments, lists all commands. With a command name, shows usage.

**`clear`**
Clear the screen.

**`exit`** / **`logout`** / Ctrl+D
End the session.

---

### 5.9 Games (Door Games)

Cooperative/competitive terminal games running on the server, in the tradition of BBS door games. `games` shows the lobby — the installed games, who's playing right now (via presence), high scores, and recent activity; each game is also a command in its own right (`chess`, `wumpus`, `trade`).

**The framework.** A game is a Go implementation of a small `Game` interface (name, lobby metadata, and a `Play` entry point receiving a context: the player, terminal I/O, database, a notify hook into the presence system, and a seedable RNG). Games get real, purpose-built Postgres tables — no generic state blobs — plus a shared high-score table the lobby reads. Games are node-local in v1; cross-node play (chess challenges over ASSP) is a natural later step, not built yet.

**`chess`** — the flagship. Full rules enforced by a bespoke engine (castling, en passant, promotion, check/mate/stalemate, draw by fifty moves / threefold repetition / insufficient material), proven correct by perft tests against the standard published node counts. Challenge any user (`mesg` respected); play is **asynchronous by default** — make your move and log off, your opponent gets a write-style notice ("chess: ilios played Nf3 in game #12") and replies at their leisure — and **live when you're both looking**: two players viewing the same board see moves land instantly. Coordinate move entry (`e2e4`, `e7e8q`, `O-O`), ASCII board with inverse-video dark squares, flipped for black. Resign and draw offers included; no computer opponent in v1 — humans play humans.

**`wumpus`** — Hunt the Wumpus, faithfully 1973: twenty rooms on a dodecahedron, super bats, bottomless pits, five crooked arrows with multi-room flight, and the warnings that made it ("I smell a wumpus!"). Single-player, quick, with a wins leaderboard — the game that proves the lobby plumbing.

**`trade`** — a TradeWars-inspired trading universe, deliberately bounded in v1: a generated sector map with a warp graph, ports dealing in three commodities with prices that drift with stock, a ship with cargo holds and credits, and a daily turn budget that makes visits count. Leaderboard by net worth. **No combat in v1** — piracy and fighters are a future chapter once the economy proves fun.

---

### 5.10 Admin Commands

Only available to users with `admin: true`.

**`wall <message>`** — broadcast to all users
**`adduser <username>`** — provision a new account
**`rmuser <username>`** — remove an account
**`motd set`** — update the MOTD
**`ban <username> [reason]`** — suspend an account
**`news modpost`** — post to moderated newsgroups
**`node list`** — show known federated nodes and their status

---

## 6. In-Terminal Text Editor

Several commands require editing multi-line text (`plan`, `public`, mail composition, news posting). We provide a minimal embedded editor — think nano, not vim.

- Single-buffer, line-oriented
- Arrow keys + home/end for navigation
- Ctrl+S to save, Ctrl+X to exit, Ctrl+G for help
- Displayed inline in the terminal session
- Works correctly with xterm.js and real terminal clients

---

## 7. Web Frontend

### 7.1 Architecture

A static site (single HTML file + bundled JS) that:
1. Opens a WebSocket connection to the server
2. Creates an xterm.js instance
3. Pipes WebSocket messages ↔ xterm I/O bidirectionally

The server-side WebSocket handler creates an identical `Session` to what SSH produces. From the REPL's perspective, there is no difference.

### 7.2 Themes

Selectable at login or via `theme` command. Stored in user preferences.

| Theme | Description |
|---|---|
| `phosphor-green` | Classic green-on-black VT100 |
| `phosphor-amber` | Warm amber, Apple II / IBM 3270 |
| `white-paper` | Black on white, daisy-wheel printer aesthetic |
| `blue-steel` | IBM 3270 blue |

### 7.3 Visual Effects (Optional)

Opt-in CRT effects via CSS/WebGL:
- Scanline overlay
- Subtle screen curvature
- Phosphor glow/bloom
- Slight screen flicker (very subtle, accessibility toggle)

These are strictly cosmetic, applied client-side, and can be disabled.

### 7.4 Public Page Rendering

`https://hostname/~username` renders the user's `~/public/` page in the browser as preformatted text, styled to match the server's default theme. No JavaScript required for this route.

---

## 8. Federation

### 8.1 Design Philosophy

Servers know about each other through a simple peer registry. Federation is opt-in per server; a node can run fully standalone. Everything federated — presence, finger, mail/news sync, talk relay, and future real-time A/V — runs over a **single purpose-built protocol, ASSP**, on one port (default 4119). We deliberately do *not* run NNTP or SMTP servers: consolidating onto one authenticated protocol gives us one peering/auth model, no public mail/news abuse surface, and a wire format designed for the media features to come. (Interop with real Usenet/standard mail was never a goal for an in-universe alternate network.)

### 8.2 ASSP: the federation protocol

ASSP is a small multiplexed **binary** protocol over TLS. Every frame is an 8-byte header — `uint32 length | uint16 channel | uint8 type | uint8 flags` — followed by a payload.

- **Channel 0 is control**: request/response JSON (`WHO`, `FINGER`, and later mail/news sync). Low-rate, so clarity beats compactness.
- **Channels 1+ are independent raw-binary streams**, one per live session. A `talk` session claims a channel today; an audio call or dithered-mono video feed is just another channel tomorrow — same mux, no protocol change.
- The **`FlagDroppable`** flag lets a sender mark frames it will drop under load (e.g. a stale video frame). Media codecs live entirely above the wire; the transport never needs to know text from audio from video.

**Authentication (peer-only).** All ASSP traffic requires the peering handshake — there is no anonymous access, so a node's user list can't be enumerated by strangers. Both peers prove possession of a shared secret via an HMAC-SHA256 challenge-response binding both nonces, both node names, a direction label (anti-reflection), and **TLS channel-binding material** (RFC 5705 exported keying). The channel binding is what makes self-signed node certificates safe: a man-in-the-middle terminating two separate TLS sessions gets different keying material on each leg, so its relayed proofs won't verify. **Trust is the peering secret + the TLS channel, not a CA** — nodes generate ephemeral self-signed certs at startup and need no PKI.

### 8.3 Presence & Finger

`rwho` aggregates logged-in users across the local node and every peer (control-channel `WHO`). `finger user@host` performs a cross-node lookup (`FINGER`). Unreachable peers are noted, never fatal.

### 8.4 Mail & News

Cross-node mail (`user@host`) and newsgroup propagation ride the ASSP control channel as request/response verbs, reusing the peer registry and peering auth. Nothing here opens a stream channel — these are low-rate, store-and-forward operations.

**Mail (`MAIL_SEND`).** Mail is asynchronous by nature, so delivery is **queued, not synchronous**: `mail nova@peer` composes exactly like local mail, then lands in a local **outbox**. A delivery worker attempts it immediately, and on failure retries with backoff (roughly 1m → 4h) for 24 hours before returning the message to the sender's inbox as a bounce from `MAILER-DAEMON@<host>` — a permanent rejection from the peer ("no such user") bounces immediately. On the receiving node the message is stored with a **remote sender address** (`ilios@nodea`) rather than a local user reference; replying to it routes back over federation the same way. Vacation auto-replies work across nodes: the receiving node includes the vacation message in its delivery response (deduplicated per remote sender per 7 days, as locally), and the sender's node drops it into the sender's inbox. Receiving nodes rate-limit deliveries per peer and per remote sender — peering is trust, not a blank check.

**News (`NEWS_ARTICLE`, `NEWS_CANCEL`, `NEWS_SINCE`).** A group federates when **both nodes carry a group of the same name** — the seeded `alt.*` groups match everywhere — except `<hostname>.*` groups, which never leave their node (§5.6). Creating a group stays a deliberate local act; articles for groups a node doesn't carry (or carries as moderated) are refused. Propagation is **push plus catch-up**: a new post pushes to every peer immediately (fire-and-forget), and a periodic sync — at startup, hourly, and when a peer is added — pulls anything missed while a node was down, using a per-peer high-water mark on the *peer's* clock (no cross-node clock comparison). Every article carries its **origin identity** (`origin node + origin id`), which deduplicates push-vs-sync overlap, resolves cross-node reply threading (an unresolvable parent degrades to a thread root), and scopes `cancel`: only an article's origin node can cancel it remotely, while local admins can always cancel locally (§10.5). Propagation is **direct peers only** — a node relays nothing it didn't author, so there are no flood loops to prevent; a full multi-hop mesh would need Usenet-style path tracking and is deliberately out of scope.

### 8.5 Talk: Federated Relay

Cross-node `talk <user@host>` opens a dedicated ASSP connection to the target's server, negotiates with a `TALK_OPEN` control message, then bridges a local talk "room" to the connection's stream channel on each side. A relay stand-in member represents the remote user in each node's room; bytes typed locally are forwarded to the peer, bytes from the peer are injected into the room. Both users' I/O flows through their own servers — neither gets direct access to the other's infrastructure. **This room-to-stream bridge is the reusable primitive the A/V features will build on**: only the bytes on the wire differ.

### 8.6 Peering

Admins add peers via:
```
node add <node> [address]   (prompts for the shared secret, entered hidden)
node remove <node>
node list
```

Peering is bilateral: both admins must add each other with the same secret. The peer registry lives in the DB; secrets are resolved live per handshake. Adding a peer immediately pulls its news backlog and flushes any mail queued while it was unreachable — no waiting for the hourly sync.

---

## 9. Retro-Futurism: Live Audio & Video

### 9.1 The Premise

Phone calls from an alternate 1979. In our timeline the terminal era ended when Xerox, Apple, and Microsoft ushered in the desktop GUI; here it never did, and real-time media grew *inside* the text-terminal world instead of alongside a windowing system. So alternate.sh grows a voice-and-video layer that looks and sounds like it was invented by people who had timesharing, phosphor CRTs, and 1-bit framebuffers — but not color, not megabits, and not the luxury of wasting cycles.

The guiding thesis: **the lofi aesthetic is not decoration bolted onto a normal video chat — it is the compression strategy.** Every stylistic constraint (no color, low detail, narrowband, dithered) is also the reason the whole thing runs on almost nothing. We lean into 1979's limits precisely because doing so makes the system tiny, fast, and unmistakably itself. "Discord, if it had been built in 1979."

This is the first feature that steps *outside* the terminal emulator. It is **web-only** — it needs a real graphical surface and audio hardware. SSH/text-tier users can't join a call's media; when a call starts they see a text notice ("nova started a video call — join on the web"). This is exactly the fork we anticipated when we kept SSH as the classic text tier rather than removing it: the rich client lives beside xterm.js, not inside it.

### 9.2 Video: 1-bit Dithered Monochrome

Video is **1 bit per pixel** — pure black and white, no grays, no color — at a small resolution (target ~128×96, tunable down to ~96×72) and a **stable 24fps**. The grayscale-to-1-bit conversion is done with a **dither** so the eye reconstructs tone from patterns of dots, the way newsprint halftones or a 1984 Macintosh did.

Dither choice is a real engineering decision, not just a look:

- **Error-diffusion (Floyd–Steinberg)** looks organic per frame but *shimmers* — tiny input changes ripple across the whole frame, so consecutive frames share almost no bits. That shimmer looks alive, but it destroys inter-frame compression.
- **Ordered / blue-noise dithering** uses a fixed threshold mask, so a static region produces *identical bits every frame*. Blue-noise masks in particular give the grainy, filmic, high-contrast look we want ("high grain black and white") while staying temporally stable.

We choose **blue-noise ordered dithering** as the default: it delivers the intended grain *and* it is what makes the codec cheap, because static regions cost nothing to transmit. (Floyd–Steinberg remains available as a "shimmer" style for those who want it, at higher bitrate.)

The rendered surface is a `<canvas>`, upscaled nearest-neighbor for crisp fat pixels, **tinted to the active phosphor theme** (green/amber/blue/paper/mono) and framed to match the CRT effects — so a call feels like another window on the same imagined machine, not a foreign embed.

### 9.3 Audio: Lofi Intercom Voice

Audio is **narrowband mono, ~8kHz**, deliberately colored to sound like a 1979 intercom / CB radio / long-distance telephone call: a **300–3400 Hz bandpass** (the telephone band), light **bit-crushing**, and a touch of saturation grit. It is not "clean audio, quieter" — it is *characterful* audio, degraded on purpose, in a way that is also extremely cheap to transmit.

### 9.4 The Pipeline

All media is captured, stylized, and **encoded on the client**, streamed as tiny frames to the user's own server, and relayed to the peer — the same server-mediated model as `talk`. Nothing is peer-to-peer; no client ever learns another's IP; there is no WebRTC.

```
  ┌─ sender (browser) ─────────────┐        ┌─ your server ─┐   ┌─ peer server ─┐   ┌─ receiver (browser) ─┐
  │ getUserMedia                   │        │               │   │               │   │                      │
  │  video → downscale → luma      │  WS    │  relay stream │   │  relay stream │   │  decode → 1-bit bitmap│
  │        → blue-noise dither     │ ─────► │  channel      │──►│  channel      │──►│  → canvas (themed)    │
  │        → XOR-delta + RLE       │ (bin)  │  (local room  │ASSP  (federated   │WS │  decode → ADPCM → PCM │
  │  audio → 8kHz → bandpass       │        │   or ASSP)    │   │   talk-style) │   │  → bandpass → speaker │
  │        → bitcrush → ADPCM      │        │               │   │               │   │                      │
  └────────────────────────────────┘        └───────────────┘   └───────────────┘   └──────────────────────┘
```

For a same-node call the server relays browser↔browser directly (like a local talk room); for a cross-node call it rides ASSP exactly like federated talk. **The media stream *is* a room stream** — this reuses the Phase-5 room-to-stream relay wholesale; only the bytes on the wire differ. Audio and video travel on **separate stream channels** so either can be dropped independently (the `FlagDroppable` bit, already in the ASSP frame header, exists for exactly this: a late video frame or audio chunk is discarded rather than queued, because stale realtime media is worthless).

### 9.5 The Codecs (built from the ground up)

Both codecs are bespoke and small enough to read in one sitting — that is the point, and it is feasible *because* the inputs are so constrained.

- **Video codec.** A frame is a 1-bit bitmap. Encoding: periodic **keyframes** (full bitmap, RLE-compressed) plus **delta frames** that XOR against the previous frame and run-length-encode the result. With blue-noise dithering, a still talking head produces near-empty deltas, so a call idles at a few hundred bytes per second and only spends bandwidth on motion. A full 128×96 keyframe is 1,536 bytes before RLE; deltas are typically a fraction of that.
- **Audio codec.** Narrowband 8kHz mono through a simple **ADPCM-style** 4-bit encoder (~4 KB/s), framed in ~20–40ms chunks. Trivial to implement, tiny on the wire, and the lofi coloring both precedes encoding (shaping the sound) and can be reapplied on playback.

No standard codec is involved. Opus would be "better," but bespoke-and-lofi *is the design*: total control, total legibility, and a sound/look that is ours.

### 9.6 Call Model & Signaling

**v1 is 1:1**, mirroring `talk`. A `CALL_OPEN` control message (analogous to `TALK_OPEN`) negotiates participants, media types (audio, video, or both), and codec parameters (resolution, fps, sample rate). Calls are **offered and accepted interactively**, respect `mesg`/do-not-disturb, and can be declined.

The user-facing verb is **`call <user[@host]>`** (`-a` for audio-only), and the terminal remains the control surface even though media is web-only: `call` rings the target, shows call status, and Ctrl+C hangs up; the browser's call panel opens and closes in response to JSON control messages on the terminal's own WebSocket, then attaches a second, media-only WebSocket (`/ws/call`). The recipient answers by typing `call <caller>` back — the same symmetric-join model as `talk`. Unlike `TALK_OPEN`, a `CALL_OPEN` response is **deferred until a human answers** (or the ring times out, ~45s); codec parameters are negotiated by the callee's node clamping the caller's proposal to its own configured limits, and the final parameters ride back in the response.

The design is **group-ready**: the room abstraction already supports N members, and media frames carry a per-source identifier so that group calls become a fan-out (the server relays each participant's stream to the others) with the client **tiling video and mixing audio**. Group rooms are a later phase, not a v1 rewrite.

### 9.7 Wire Format

Every media payload — a binary message on the `/ws/call` WebSocket and the payload of an ASSP stream frame alike — is a self-describing **media packet** with a 4-byte header:

```
u8 kind | u8 source | u16 seq        (big-endian)
```

- `kind` — `0x01` video keyframe, `0x02` video delta, `0x03` audio chunk
- `source` — participant id within the call (the group-readiness hook of §9.6; a 1:1 call uses 0 and 1)
- `seq` — per-source, per-media wrapping counter, incremented **only for transmitted packets**

**Video payload:** `u16 width | u16 height`, then the packed 1-bit bitmap (keyframe) or the XOR against the previous frame (delta), both compressed with **PackBits RLE**. Width is a multiple of 8; rows pack MSB-first. Keyframes go out every ~2 seconds (48 frames); an all-zero delta (nothing moved) is simply **not sent** — with temporally-stable blue-noise dither this makes a still scene nearly free. A receiver that sees a `seq` gap (a relay shed a late frame) freezes and waits for the next keyframe rather than decoding garbage.

**Audio payload:** `i16 predictor | u8 step_index | u8 reserved`, then 4-bit IMA-ADPCM samples — 20ms chunks (160 samples at 8kHz → 80 bytes + header). Every chunk carries its own decoder state, so any chunk is independently decodable: a dropped chunk costs 20ms of silence, never desync.

Over ASSP, a call's dedicated connection maps **video to stream channel 2 and audio to channel 3**, every media frame flagged `FlagDroppable`. On the web leg, the browser's `/ws/call` binary messages are media packets verbatim; the server relays them into the same room fan-out that talk uses.

**Codec parity.** The codecs are implemented twice — canonical JS in the browser, and a Go twin the server's integration tests use to prove real decodable media flowed end-to-end — cross-validated byte-for-byte against shared, committed test vectors (including the blue-noise mask itself), so the two implementations cannot drift.

### 9.8 Non-Goals

No color. No HD, ever — low detail is a feature. No WebRTC, no P2P, no TURN. **Nothing is stored**: media is ephemeral and never touches the database, consistent with `talk`. No interoperability with standard videoconferencing — this is an in-universe medium, not a Zoom client.

---

## 10. Security Model

### 10.1 No Real Shell

The REPL provides zero access to the underlying OS. There is no `exec`, no filesystem access, no process spawning. Commands are a closed, enumerated set handled by Go functions.

### 10.2 Authentication

- Password auth: bcrypt, minimum 10 rounds
- SSH key auth: standard public key authentication via the SSH protocol
- Web frontend: session token (UUID) stored in browser localStorage, issued on login, expires after configurable idle period
- No OAuth, no third-party auth providers — in-universe, this system is its own identity provider

### 10.3 Rate Limiting

- Login attempts: 5 failures per minute per IP before 60-second lockout
- Mail sending: configurable per-hour limit per user
- News posting: configurable per-day limit per user
- `write` / `talk` initiation: rate-limited to prevent harassment

### 10.4 Write/Talk Permissions

- `mesg n` is respected absolutely
- `talk` can be declined interactively
- Users can block specific other users (`block <username>`)
- `wall` is admin-only, rate-limited even for admins

### 10.5 Content & Moderation

- Admins can cancel any news article
- Admins can delete any mail on the server (with audit log)
- Newsgroups can be set moderated (posts require admin approval)
- Ban system: banned users can connect but see only a ban notice

---

## 11. Deployment

### 11.1 Single Binary

The server compiles to a single Go binary with no runtime dependencies beyond PostgreSQL. The web frontend assets are embedded in the binary via `go:embed`.

```
alternate-sh serve --config /etc/alternate-sh/config.toml
```

### 11.2 Configuration (`config.toml`)

```toml
[server]
hostname = "mynode.example.com"
motd = "Welcome to mynode. Be excellent to each other."

[ssh]
port = 22
host_key = "/etc/alternate-sh/ssh_host_ed25519_key"

[web]
port = 443
tls_cert = "/etc/letsencrypt/live/mynode.example.com/fullchain.pem"
tls_key  = "/etc/letsencrypt/live/mynode.example.com/privkey.pem"

[database]
dsn = "postgres://alternateuser:pass@localhost/alternatedb"

[federation]
assp_port = 4119
enabled = true

[calls]
enabled = true
width = 128
height = 96
fps = 24

[limits]
max_users = 500
mail_per_hour = 50
news_per_day = 20
```

### 11.3 Database Migrations

Schema migrations are embedded in the binary and run automatically on startup via a lightweight migration runner. No separate migration tool required.

### 11.4 Docker

A `Dockerfile` and `docker-compose.yml` are provided for quick deployment alongside PostgreSQL.

---

## 12. Repository Structure

```
alternate.sh/
├── cmd/
│   └── alternate-sh/
│       └── main.go          // entry point, cobra CLI
├── internal/
│   ├── server/              // SSH server, WebSocket server
│   ├── session/             // REPL, session state
│   ├── commands/            // one file per command group
│   │   ├── finger.go
│   │   ├── who.go
│   │   ├── mail.go
│   │   ├── news.go
│   │   ├── talk.go
│   │   ├── write.go
│   │   └── ...
│   ├── db/                  // PostgreSQL queries (sqlc generated)
│   ├── assp/                // ASSP wire protocol (frames, handshake, TLS)
│   ├── federation/          // ASSP server/client: presence, finger, talk/call relay
│   ├── presence/            // online user tracking, events, rooms
│   ├── calls/               // call signaling (offer/ring/accept/hangup)
│   ├── av/                  // media codecs, Go twin (dither, RLE, video, ADPCM)
│   ├── editor/              // in-terminal text editor
│   └── theme/               // terminal theme definitions
├── web/                     // frontend (embedded)
│   ├── index.html
│   └── js/                  // ES modules: terminal glue, codecs, call UI
│       ├── app.js           //   login + xterm + control channel
│       ├── call.js          //   capture→dither→encode pipeline, media WS
│       ├── videocodec.js    //   canonical JS codecs (Go twins in internal/av)
│       └── ...
├── migrations/              // SQL migration files
├── config.example.toml
├── Dockerfile
├── docker-compose.yml
└── DESIGN.md
```

---

## 13. Inline Graphics & the Mobile Terminal

### 13.1 The Premise

Side panels are antithetical to the illusion. A terminal is one surface — a stream of output flowing up the screen — and everything the system shows you should live *inside* that stream. Phase 6's call panel was scaffolding; this phase retires it. The in-universe justification is, delightfully, real history: DEC shipped sixel graphics on the VT340 in 1987. In the alternate timeline where terminals never lost, inline raster graphics in the byte stream are simply how pictures work.

### 13.2 Still Images: Real Sixel in the Stream

Static images are rendered **server-side into sixel escape sequences**, sent as ordinary terminal output. The web client's terminal understands sixel (via renderer support); so does any SSH user running a sixel-capable terminal (foot, WezTerm, xterm) — **SSH users get inline images for free**, which is exactly the kind of dividend authenticity pays. Capability negotiation is the real mechanism: terminals advertise sixel in their Device Attributes response, and clients that lack it get a text placeholder (`[image: sunset.pix — 128×96 — view on web or a sixel terminal]`).

### 13.3 Live Video: Anchored in the Scrollback

Video keeps the Phase-6 codec and media socket — pushing 24fps through sixel encoding would burn 10–40× the bandwidth and abandon the delta coder — but the *rendering* moves inline: the call reserves rows in the scrollback and draws its frames on a canvas anchored to those cells, scrolling with the text like any other output. It looks like sixel; it costs like ASSP. The side panel's buttons become what they always should have been: keystrokes in the terminal (mute, style, hangup live on the in-call key loop that Ctrl+C already inhabits).

### 13.4 The Image Ecosystem

Uploads are **dithered to 1-bit by the client** before they ever leave the user's machine — the browser already carries the blue-noise ditherer (it's the video pipeline's first stage), so a photo becomes a packed 1-bit bitmap + RLE in the codec's own format. The server **verifies** every upload is genuinely that format (clients are never trusted), enforces a **fixed per-user quota** (configurable; 1-bit RLE images are so small that a modest cap holds dozens), and renders to sixel on demand. There is deliberately no way to store a full-color image — the aesthetic is enforced at the wire.

Where images appear:
- **Mail & news attachments** — attach from the composer; rendered inline for rich clients, placeholder text otherwise. Admins can delete any image (§10.5 applies).
- **Finger portraits** — a tiny 1-bit portrait (~64×64) on your profile, shown by `finger` on capable clients.
- **Public pages** — images embedded in `~/public/`, rendered inline and at `/~username`.

### 13.5 The Mobile Terminal

The web client becomes a first-class phone experience without ceasing to be a terminal: responsive layout, touch scrolling and selection, a soft key bar for what phone keyboards lack (Esc, Ctrl, Tab, arrows, Ctrl+C), font scaling, sensible call layout in both orientations, and a PWA manifest so it installs to the home screen. No native APIs — that is Phase 9's job.

---

## 14. Native Clients

A real Flutter/Dart client for Android (iOS-ready by construction, shipped later). Not a WebView: a native app speaking the same wire — the login API, the terminal WebSocket with its JSON control channel, `/ws/call`, and the delegation protocol of §15. Terminal emulation with the same inline-graphics behavior as the web (sixel + anchored video); camera, microphone, and keyboard handled natively.

**Codec parity extends to a third implementation:** Dart twins of the dither/RLE/video/ADPCM codecs, validated byte-for-byte against the *same* committed test vectors that already lock JS to Go. The vectors are the spec; a third consumer strengthens them.

Tooling rides existing, known-good ground: the app builds via a Nix flake modeled on the maintainer's working Flutter example projects, so the fight is the product, not the toolchain. Distribution starts as **direct self-hosted APKs** (fitting the hostable-by-anyone ethos); F-Droid, Play Store, and iOS remain open paths — which means no proprietary dependencies, ever.

---

## 15. The Local Home

### 15.1 The Premise

Every user's home directory grows one special entry: `~/local/`. It looks like part of your account. It is not. **It lives on your own machine** — browser storage on the web, an app directory on Android — and its contents never touch the server, are never shared, and are invisible to every other user. Privacy here isn't a policy, it's physics: the server *cannot* read what it never receives. In-universe, it's the machine on your desk finally talking to the machine downtown.

### 15.2 Storage

- **Web:** OPFS (origin-private file system) — works in every modern browser including Firefox. Explicit `export`/`import` commands move files between `~/local` and the host OS (download/file-picker); on Chromium, a real directory can optionally be mounted via the File System Access API.
- **Android (Phase 9):** a real app-scoped directory, no compromises.
- **Non-goals:** no sharing, no server-side backup, no cross-device sync. Your files, your machine, your problem — that's the point.

### 15.3 Server-Delegated Execution

The server's REPL remains the single prompt. When a command touches `~/local` (or is a local-only tool), the server doesn't execute it — it sends a **delegation control message** ("run `vi notes.txt` locally") down the terminal's control channel. The client's toolbox executes against its own filesystem and streams output/UI through the same terminal surface. The server orchestrates the experience but never sees file contents, names beyond the command line, or listings. Over SSH there is no local machine to delegate to: `vi ~/local/...` explains that local files need the web or app client.

### 15.4 The Toolbox

A curated set of **bespoke tools written against a clean VFS interface** over OPFS: `ls`, `cat`, `cp`, `mv`, `rm`, `mkdir`, `grep`, `wc`, `head`, `tail`, `diff` — and `ed`, which is not a joke: it is the editor 1979 actually had, it is small, and it teaches the interaction model everything else builds on. Bespoke beats porting here: compiling busybox/toybox to WASM means WASI shims and fork/exec emulation for tools designed around POSIX processes, and the result integrates worse with our VFS and terminal.

**Phase 10.1 — vi** (its own subphase, as it deserves): evaluate porting a real vi (busybox vi, or a WASI vim build) against growing our own visual editor on the `ed` core — decided when we get there, behind the same VFS, judged on authenticity, size, and integration.

---

## 16. Implementation Phases

### Phase 1 — Core Shell (MVP) ✅
SSH server, WebSocket server, user auth, REPL, `finger`, `who`, `w`, `last`, `write`, `mesg`, `motd`, `fortune`, `plan`, `project`, `passwd`, `chfn`, `help`, `logout`. PostgreSQL schema and migrations.

### Phase 2 — Web Frontend ✅
xterm.js client, WebSocket relay, phosphor/paper themes, opt-in CRT effects, `~/public/` rendering at `/~username`.

### Phase 3 — Mail & News ✅
`mail`, `vacation`, MOTD editing, `msgs`, `calendar`, newsgroups, `rn`/`news`, `post`, `followup`.

### Phase 4 — Real-time ✅
`talk`, `ytalk`, presence idle tracking, `biff`-style new-mail alerts on login and during session.

### Phase 4.1 — Public Signup & Hardening ✅
Web-only email-confirmed signup, per-IP rate limiting, disposable-email blocking, input/XSS hardening. (SSH kept for login; signup web-only.)

### Phase 5 — Federation ✅
All-ASSP: multiplexed binary protocol on 4119 with peer-only HMAC + TLS-channel-binding auth, node registry (`node add/remove/list`), cross-node `rwho` and `finger user@host`, cross-node `talk` relay. No NNTP/SMTP servers (see §8).

### Phase 6 — Retro-Futurism: Live A/V ✅
`call <user[@host]>`: 1-bit blue-noise-dithered monochrome video (~128×96 @ 24fps) and lofi narrowband audio, both bespoke client-side codecs (with Go twins cross-validated by shared test vectors), streamed over the WebSocket→ASSP substrate reusing the Phase-5 room-to-stream relay. 1:1 first, group-ready. Web-only (first feature outside the terminal emulator). See §9.

### Phase 6.1 — Federated Mail & News ✅
Cross-node mail (`user@host`) and newsgroup propagation over the ASSP control channel (§8.4), reusing the peer registry and peering auth: queued mail with MAILER-DAEMON bounces and cross-node vacation replies, push + catch-up news sync with origin-identity dedupe/threading/cancel, shared-name group federation with `<hostname>.*` containment.

### Phase 6.2 — Federation Polish ✅
Two small follow-ups before the games phase: an explicit call decline (`call -r <user[@host]>`, usable from SSH since it's pure signaling), and `node add` triggering an immediate news sync + outbox flush instead of waiting for the hourly tick.

### Phase 7 — Door Games ✅
The games framework (§5.9) and its three launch tenants: chess (bespoke perft-proven engine, async + live play), Hunt the Wumpus (1973, faithfully), and the trade economy v1 (sectors, ports, daily turns — no combat yet). Node-local; per-game Postgres tables; lobby with presence and high scores.

### Phase 7.1 — Community Polish ← NEXT
Community fortune submission, mailing lists, and advanced moderation tools (approval queues for moderated groups, audit surfaces) — split out so the games get undivided attention.

### Phase 8 — Inline Graphics & the Mobile Terminal
Retire the call side panel: everything renders inside the terminal stream (§13). Server-side sixel for stills (SSH sixel terminals included, capability-negotiated), cell-anchored inline rendering for call video (keeping the Phase-6 codec), the 1-bit image ecosystem (client-dithered uploads, server-verified format, per-user quota; mail/news attachments, finger portraits, public page images), and a first-class mobile web experience (responsive, touch, soft key bar, PWA).

### Phase 9 — Native Clients
Flutter/Dart Android app (§14): native terminal + inline graphics parity, Dart codec twins validated against the shared test vectors (third parity implementation), native camera/mic, Nix-flake build based on known-good Flutter tooling. Direct self-hosted APK first; F-Droid/Play/iOS kept viable (no proprietary deps).

### Phase 10 — The Local Home
`~/local/` — a directory in your home that lives on your own machine (§15): OPFS on web with export/import, real directory on Android, server-delegated execution so the one prompt stays seamless while the server never sees file contents, and a bespoke WASM/JS toolbox (`ls`, `cat`, `grep`, `ed`, …) against a clean VFS.

### Phase 10.1 — vi
The visual editor for `~/local`, as its own undertaking: evaluate a real vi port (busybox vi / WASI vim) against a bespoke editor grown from `ed`, behind the same VFS.
