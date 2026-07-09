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
Places a live audio/video call — the retro-futurism layer of §9. The terminal is the control surface: `call` rings the target, shows call status, and Ctrl+C hangs up; the actual media surface is the browser's phosphor-tinted call panel, which opens beside the terminal when the call connects (media is **web-only** — see §9.1). The recipient accepts by typing `call <caller>` back, mirroring talk's symmetric-join model, or ignores it until the ring times out. `call -a` places an audio-only call. Over SSH, `call` explains that calls need the web client, and SSH recipients see a text notice ("ilios is calling — join on the web"). Respects `mesg n` and is rate-limited like `talk`.

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

Accessible via `games` or by name directly. These are cooperative/competitive terminal games running on the server, in the tradition of BBS door games.

Initial candidates:
- A simple multi-player text RPG with persistent state
- A trading/economy game (TradeWars-inspired)
- Chess with `write`-based async play and `talk`-based live play

`games` shows the lobby — active players, high scores, last played.

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

### 8.4 Mail & News (planned)

Cross-node mail (`user@host`) and newsgroup propagation will ride the same ASSP control channel as sync operations, reusing the peer registry and auth. Namespace convention: `<nodename>.local` groups stay local; others federate with willing peers.

### 8.5 Talk: Federated Relay

Cross-node `talk <user@host>` opens a dedicated ASSP connection to the target's server, negotiates with a `TALK_OPEN` control message, then bridges a local talk "room" to the connection's stream channel on each side. A relay stand-in member represents the remote user in each node's room; bytes typed locally are forwarded to the peer, bytes from the peer are injected into the room. Both users' I/O flows through their own servers — neither gets direct access to the other's infrastructure. **This room-to-stream bridge is the reusable primitive the A/V features will build on**: only the bytes on the wire differ.

### 8.6 Peering

Admins add peers via:
```
node add <node> [address]   (prompts for the shared secret, entered hidden)
node remove <node>
node list
```

Peering is bilateral: both admins must add each other with the same secret. The peer registry lives in the DB; secrets are resolved live per handshake.

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

## 13. Implementation Phases

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

### Phase 6 — Retro-Futurism: Live A/V ← NEXT
`call <user[@host]>`: 1-bit blue-noise-dithered monochrome video (~128×96 @ 24fps) and lofi narrowband audio, both bespoke client-side codecs (with Go twins cross-validated by shared test vectors), streamed over the WebSocket→ASSP substrate reusing the Phase-5 room-to-stream relay. 1:1 first, group-ready. Web-only (first feature outside the terminal emulator). See §9.

### Phase 6.1 — Federated Mail & News
Cross-node mail (`user@host`) and newsgroup propagation over the ASSP control channel (§8.4), reusing the peer registry and peering auth. Split out of Phase 6 so the A/V work gets undivided attention.

### Phase 7 — Games & Polish
Door games framework, initial games, community fortune submission, mailing lists, advanced moderation tools.
