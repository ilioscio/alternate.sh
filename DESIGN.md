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

News federation uses NNTP (see §8.2).

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

## 9. Security Model

### 9.1 No Real Shell

The REPL provides zero access to the underlying OS. There is no `exec`, no filesystem access, no process spawning. Commands are a closed, enumerated set handled by Go functions.

### 9.2 Authentication

- Password auth: bcrypt, minimum 10 rounds
- SSH key auth: standard public key authentication via the SSH protocol
- Web frontend: session token (UUID) stored in browser localStorage, issued on login, expires after configurable idle period
- No OAuth, no third-party auth providers — in-universe, this system is its own identity provider

### 9.3 Rate Limiting

- Login attempts: 5 failures per minute per IP before 60-second lockout
- Mail sending: configurable per-hour limit per user
- News posting: configurable per-day limit per user
- `write` / `talk` initiation: rate-limited to prevent harassment

### 9.4 Write/Talk Permissions

- `mesg n` is respected absolutely
- `talk` can be declined interactively
- Users can block specific other users (`block <username>`)
- `wall` is admin-only, rate-limited even for admins

### 9.5 Content & Moderation

- Admins can cancel any news article
- Admins can delete any mail on the server (with audit log)
- Newsgroups can be set moderated (posts require admin approval)
- Ban system: banned users can connect but see only a ban notice

---

## 10. Deployment

### 10.1 Single Binary

The server compiles to a single Go binary with no runtime dependencies beyond PostgreSQL. The web frontend assets are embedded in the binary via `go:embed`.

```
alternate-sh serve --config /etc/alternate-sh/config.toml
```

### 10.2 Configuration (`config.toml`)

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
nntp_port = 119
smtp_port = 25
enabled = true

[limits]
max_users = 500
mail_per_hour = 50
news_per_day = 20
```

### 10.3 Database Migrations

Schema migrations are embedded in the binary and run automatically on startup via a lightweight migration runner. No separate migration tool required.

### 10.4 Docker

A `Dockerfile` and `docker-compose.yml` are provided for quick deployment alongside PostgreSQL.

---

## 11. Repository Structure

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
│   ├── federation/          // AFSP, NNTP peering, SMTP relay
│   ├── presence/            // online user tracking, events
│   ├── editor/              // in-terminal text editor
│   └── theme/               // terminal theme definitions
├── web/                     // xterm.js frontend (embedded)
│   ├── index.html
│   ├── terminal.js
│   └── themes/
├── migrations/              // SQL migration files
├── config.example.toml
├── Dockerfile
├── docker-compose.yml
└── DESIGN.md
```

---

## 12. Implementation Phases

### Phase 1 — Core Shell (MVP)
SSH server, WebSocket server, user auth, REPL, `finger`, `who`, `w`, `last`, `write`, `mesg`, `motd`, `fortune`, `plan`, `project`, `passwd`, `chfn`, `help`, `logout`. PostgreSQL schema and migrations.

### Phase 2 — Web Frontend
xterm.js client, WebSocket relay, phosphor/paper themes, opt-in CRT effects, `~/public/` rendering at `/~username`. The WebSocket server is already wired in Phase 1 — this phase is the client-side work and public-facing polish that makes the project presentable.

### Phase 3 — Mail & News
`mail`, `vacation`, MOTD editing, `msgs`, `calendar`, newsgroups, `rn`/`news`, `post`, `followup`.

### Phase 4 — Real-time
`talk`, `ytalk`, presence events, notification system (`biff`-style new mail alerts on login and during session).

### Phase 5 — Federation
ASSP server, NNTP peering, SMTP inter-server mail, `finger user@host`, `talk user@host`, node registry, admin peering commands.

### Phase 6 — Games & Polish
Door games framework, initial games, community fortune submission, mailing lists, advanced moderation tools.
