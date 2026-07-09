# Two-node NixOS VM test for federation. Boots nodea and nodeb, each running
# alternate-sh with federation enabled, peers them with a shared secret, and
# exercises cross-node finger, rwho, and talk over real ASSP connections
# between the two machines.
#
# Run with:  nix build .#checks.x86_64-linux.federation
{ pkgs, package, module }:

let
  # Shared peering secret used by both nodes.
  peerSecret = "federation-shared-secret-abc123";

  # fake-browser: a scripted stand-in for the web client, exercising the real
  # call path end-to-end — login, terminal WebSocket, `call` at the REPL,
  # the call-start control message, the /ws/call media socket, one media
  # packet each way, and hangup via Ctrl+C on the terminal.
  fakeBrowser = pkgs.writeScriptBin "fake-browser" ''
    #!${pkgs.python3.withPackages (ps: [ ps.websocket-client ])}/bin/python3
    import json, sys, time, urllib.request
    import websocket
    from websocket import ABNF

    user, pw, role, target = sys.argv[1:5]
    host = "127.0.0.1:8080"

    req = urllib.request.Request(
        f"http://{host}/api/login",
        data=json.dumps({"username": user, "password": pw}).encode(),
        headers={"Content-Type": "application/json"},
    )
    token = json.loads(urllib.request.urlopen(req).read())["token"]

    term = websocket.WebSocket()
    term.connect(f"ws://{host}/ws?token={token}")
    term.settimeout(60)

    def type_line(s):
        term.send((s + "\r").encode(), opcode=ABNF.OPCODE_BINARY)

    # The caller places the call; the callee answers once the ring arrives.
    time.sleep(3 if role == "caller" else 8)
    type_line(f"call {target}")

    # Wait for the call-start control message (a text frame).
    call = None
    while call is None:
        op, frame = term.recv_data()
        if op == ABNF.OPCODE_TEXT:
            m = json.loads(frame)
            if m.get("type") == "call-start":
                call = m
    print("CALL-START", call["role"], call["peer"], call["params"]["width"], flush=True)

    media = websocket.WebSocket()
    media.connect(f"ws://{host}/ws/call?token={token}&call={call['callId']}")
    media.settimeout(60)
    time.sleep(2)  # let both media sockets and the ASSP bridge settle

    # One valid video keyframe: header | 128x96 | PackBits(1536 zero bytes).
    pkt = bytes([1, call["source"], 0, 0, 0, 128, 0, 96]) + bytes([0x81, 0x00] * 12)
    media.send(pkt, opcode=ABNF.OPCODE_BINARY)

    got = media.recv()
    print("MEDIA-RECV", bytes(got).hex(), flush=True)

    time.sleep(2)
    if role == "caller":
        term.send(b"\x03", opcode=ABNF.OPCODE_BINARY)  # Ctrl+C: hang up
    # Both sides: the media socket must close when the call ends.
    try:
        media.recv()
        print("MEDIA-STILL-OPEN", flush=True)
    except Exception:
        print("MEDIA-CLOSED", flush=True)
    print("DONE", flush=True)
  '';

  mkNode = name: { config, ... }: {
    imports = [ module ];
    networking.hostName = name;
    services.alternate-sh = {
      enable = true;
      inherit package;
      hostname = name;               # ASSP node identity == machine name
      ssh.port = 2222;
      web.port = 8080;
      federation.enable = true;      # ASSP listener on 4119
      openFirewall = true;           # nodes must reach each other on 4119
    };
    environment.systemPackages = [
      package pkgs.sshpass pkgs.curl fakeBrowser
      config.services.postgresql.package
    ];
    virtualisation.memorySize = 1024;
  };
in
pkgs.testers.runNixOSTest {
  name = "alternate-sh-federation";

  nodes.nodea = mkNode "nodea";
  nodes.nodeb = mkNode "nodeb";

  testScript = ''
    import base64
    import re

    def clean(s):
        return re.sub(r"\x1b\[[0-9;?]*[A-Za-z]", "", s)

    SSH = (
        "sshpass -p {pw} ssh -tt "
        "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "
        "-o LogLevel=ERROR -o ConnectTimeout=10 -p 2222 {user}@127.0.0.1"
    )

    def sh(machine, user, pw, lines):
        script = "\n".join(lines + ["logout"]) + "\n"
        b64 = base64.b64encode(script.encode()).decode()
        _, out = machine.execute(
            f"echo {b64} | base64 -d | " + SSH.format(user=user, pw=pw), timeout=60
        )
        return out

    def expect(out, *needles):
        for n in needles:
            if n not in out:
                print("---- transcript ----\n" + out + "\n--------------------")
                raise AssertionError(f"expected {n!r} in output")

    def psql(machine, query):
        return machine.succeed(
            f"runuser -u alternate-sh -- psql -d alternate-sh -tAc \"{query}\""
        ).strip()

    def psql_check(query, want):
        # A shell command for wait_until_succeeds: true when the query
        # returns exactly `want`.
        return (
            f"runuser -u alternate-sh -- psql -d alternate-sh -tAc \"{query}\""
            f" | grep -qx '{want}'"
        )

    for m in (nodea, nodeb):
        m.wait_for_unit("postgresql.service")
        m.wait_for_unit("alternate-sh.service")
        m.wait_for_open_port(2222)
        m.wait_for_open_port(4119)

    # Nodes must resolve each other by name (runNixOSTest wires this up).
    nodea.succeed("ping -c1 nodeb")
    nodeb.succeed("ping -c1 nodea")

    # ── Fixtures: an admin user on each node ──────────────────────────────────
    def adduser(machine, user, pw, name, admin=True):
        machine.succeed(
            "printf '[database]\\ndsn = \"postgres:///alternate-sh?host=/var/run/postgresql\"\\n'"
            " > /tmp/cli.toml && chmod 644 /tmp/cli.toml"
        )
        flag = " --admin" if admin else ""
        machine.succeed(
            f"runuser -u alternate-sh -- alternate-sh adduser --config /tmp/cli.toml"
            f" --username {user} --password {pw} --name '{name}'{flag}"
        )

    adduser(nodea, "alice", "alicepass123", "Alice Anderson")
    adduser(nodeb, "bob", "bobpass12345", "Bob Baker")

    # ── Peering via the real `node add` command (secret read hidden) ──────────
    with subtest("peer the two nodes and list"):
        # `node add nodeb` prompts for the secret, consumed as the next line.
        out = sh(nodea, "alice", "alicepass123", ["node add nodeb", "${peerSecret}"])
        expect(out, "Peer nodeb added")
        out = sh(nodeb, "bob", "bobpass12345", ["node add nodea", "${peerSecret}"])
        expect(out, "Peer nodea added")

        out = sh(nodea, "alice", "alicepass123", ["node list"])
        expect(out, "nodeb", "nodeb:4119")

    # ── Cross-node finger (bob need not be online) ────────────────────────────
    with subtest("finger bob@nodeb from nodea"):
        out = sh(nodea, "alice", "alicepass123", ["finger bob@nodeb"])
        expect(out, "bob@nodeb", "Bob Baker")

    with subtest("finger unknown@nodeb is reported, and unknown host fails"):
        out = sh(nodea, "alice", "alicepass123", ["finger ghost@nodeb"])
        expect(out, "no such user")
        out = sh(nodea, "alice", "alicepass123", ["finger bob@nowhere"])
        expect(out, "not a federation peer")

    # ── rwho aggregates presence across nodes ─────────────────────────────────
    with subtest("rwho shows local + remote users"):
        # bob idles on nodeb so he shows up in the aggregate.
        nodeb.execute(
            "sleep 10 | " + SSH.format(user="bob", pw="bobpass12345")
            + " > /tmp/bob-idle.out 2>&1 &"
        )
        nodeb.sleep(3)
        out = sh(nodea, "alice", "alicepass123", ["rwho"])
        expect(out,
            "alice", "nodea (local)",   # our own session
            "bob", "nodeb",             # bob on the peer node
        )
        nodeb.sleep(8)

    # ── Cross-node talk relay ─────────────────────────────────────────────────
    with subtest("talk alice@nodea <-> bob@nodeb"):
        # bob comes online, waits, then answers alice's ring and types a canary.
        nodeb.execute(
            "{ sleep 4; printf 'talk alice@nodea\\n'; sleep 2; printf 'BOB-CANARY'; "
            "sleep 6; printf '\\003'; } | "
            + SSH.format(user="bob", pw="bobpass12345")
            + " > /tmp/bob-talk.out 2>&1 &"
        )
        nodea.sleep(2)
        # alice initiates the cross-node talk and types her canary.
        nodea.execute(
            "{ printf 'talk bob@nodeb\\n'; sleep 6; printf 'ALICE-CANARY'; "
            "sleep 4; printf '\\003'; } | "
            + SSH.format(user="alice", pw="alicepass123")
            + " > /tmp/alice-talk.out 2>&1 &"
        )
        nodea.sleep(14)

        alice_out = clean(nodea.succeed("cat /tmp/alice-talk.out"))
        bob_out = clean(nodeb.succeed("cat /tmp/bob-talk.out"))

        expect(alice_out, "Ringing bob@nodeb", "BOB-CANARY")
        expect(bob_out, "talk request from alice@nodea", "ALICE-CANARY")

    # ── The A/V layer (§9) ────────────────────────────────────────────────────
    with subtest("call panel assets are served by the embedded frontend"):
        # Fetch to a file, then grep: piping curl into `grep -q` races EPIPE
        # (grep exits at first match, curl fails with code 23 under pipefail).
        def asset_has(path, needle):
            nodea.succeed(
                f"curl -sf http://127.0.0.1:8080{path} -o /tmp/asset"
                f" && grep -q '{needle}' /tmp/asset"
            )
        asset_has("/js/app.js", "connectWS")
        asset_has("/js/call.js", "call-start")
        asset_has("/js/bluenoise.js", "BLUE_NOISE_MASK")
        asset_has("/js/worklets.js", "capture-processor")
        asset_has("/", "call-panel")

    with subtest("call over ssh explains the web requirement"):
        out = sh(nodea, "alice", "alicepass123", ["call bob@nodeb"])
        expect(out, "needs the web client")

    with subtest("cross-node call: full web path, media both ways, hangup"):
        # Two scripted web clients: alice (caller, nodea) and bob (callee,
        # nodeb). Each sends one valid keyframe and must receive the peer's.
        nodeb.execute(
            "fake-browser bob bobpass12345 callee alice@nodea"
            " > /tmp/bob-call.out 2>&1 &"
        )
        nodea.execute(
            "fake-browser alice alicepass123 caller bob@nodeb"
            " > /tmp/alice-call.out 2>&1 &"
        )
        # Poll for completion rather than guessing a sleep.
        nodea.wait_until_succeeds("grep -q DONE /tmp/alice-call.out", timeout=90)
        nodeb.wait_until_succeeds("grep -q DONE /tmp/bob-call.out", timeout=90)

        alice_out = nodea.succeed("cat /tmp/alice-call.out")
        bob_out = nodeb.succeed("cat /tmp/bob-call.out")

        # The packet each side receives is the peer's: same body, peer's
        # source id (alice=0, bob=1) — byte-identical across the bridge.
        body = (bytes([0, 0, 0, 128, 0, 96]) + bytes([0x81, 0x00] * 12)).hex()
        expect(alice_out,
            "CALL-START caller bob@nodeb 128",
            "MEDIA-RECV " + (bytes([1, 1]).hex() + body),  # from bob (source 1)
            "MEDIA-CLOSED", "DONE",
        )
        expect(bob_out,
            "CALL-START callee alice@nodea 128",
            "MEDIA-RECV " + (bytes([1, 0]).hex() + body),  # from alice (source 0)
            "MEDIA-CLOSED", "DONE",
        )

    # ── Federated mail (§8.4) ─────────────────────────────────────────────────
    with subtest("cross-node mail: queued, delivered, read on the peer"):
        out = sh(nodea, "alice", "alicepass123", [
            "mail bob@nodeb", "FEDMAIL-SUBJ", "FEDMAIL-BODY", ".", "y",
        ])
        expect(out, "To: bob@nodeb", "queued for delivery")
        nodeb.wait_until_succeeds(psql_check(
            "SELECT count(*) FROM mail WHERE remote_sender='alice@nodea'", "1"
        ), timeout=60)
        out = sh(nodeb, "bob", "bobpass12345", ["mail", "1", "q"])
        expect(out, "alice@nodea", "FEDMAIL-SUBJ", "FEDMAIL-BODY")

    with subtest("cross-node mail: reply routes back over federation"):
        out = sh(nodeb, "bob", "bobpass12345", [
            "mail", "r1", "FEDREPLY-BODY", ".", "y", "q",
        ])
        expect(out, "queued for delivery")
        nodea.wait_until_succeeds(psql_check(
            "SELECT count(*) FROM mail WHERE remote_sender='bob@nodeb'", "1"
        ), timeout=60)
        out = sh(nodea, "alice", "alicepass123", ["mail", "1", "q"])
        expect(out, "bob@nodeb", "Re: FEDMAIL-SUBJ", "FEDREPLY-BODY")

    with subtest("cross-node mail: unknown user bounces via MAILER-DAEMON"):
        out = sh(nodea, "alice", "alicepass123", [
            "mail ghost@nodeb", "GHOST-SUBJ", "GHOST-BODY", ".", "y",
        ])
        expect(out, "queued for delivery")
        nodea.wait_until_succeeds(psql_check(
            "SELECT count(*) FROM mail WHERE remote_sender='MAILER-DAEMON@nodea'", "1"
        ), timeout=60)
        out = sh(nodea, "alice", "alicepass123", ["mail", "1", "q"])
        expect(out, "MAILER-DAEMON", "Returned mail: GHOST-SUBJ", "no such user")

    with subtest("cross-node vacation auto-reply"):
        sh(nodeb, "bob", "bobpass12345", [
            "vacation msg", "FEDVAC-CANARY", ".", "vacation on",
        ])
        out = sh(nodea, "alice", "alicepass123", [
            "mail bob@nodeb", "VAC-TRIGGER", "are you there?", ".", "y",
        ])
        expect(out, "queued for delivery")
        nodea.wait_until_succeeds(psql_check(
            "SELECT count(*) FROM mail WHERE remote_sender='bob@nodeb'"
            " AND subject='Auto-reply: VAC-TRIGGER'", "1"
        ), timeout=60)
        out = sh(nodea, "alice", "alicepass123", ["mail", "1", "q"])
        expect(out, "Auto-reply: VAC-TRIGGER", "FEDVAC-CANARY")
        sh(nodeb, "bob", "bobpass12345", ["vacation off"])

    # ── Federated news (§8.4) ─────────────────────────────────────────────────
    with subtest("news: post on nodea propagates to nodeb"):
        out = sh(nodea, "alice", "alicepass123", [
            "post alt.chat", "FEDNEWS-SUBJ", "FEDNEWS-BODY", ".", "y",
        ])
        expect(out, "Article posted.")
        nodeb.wait_until_succeeds(psql_check(
            "SELECT count(*) FROM articles WHERE remote_author='alice@nodea'"
            " AND subject='FEDNEWS-SUBJ'", "1"
        ), timeout=60)
        out = sh(nodeb, "bob", "bobpass12345", ["news", "alt.chat", "1", "x", "q", "q"])
        expect(out, "FEDNEWS-SUBJ", "FEDNEWS-BODY", "alice@nodea")

    with subtest("news: cross-node followup threads under the original"):
        out = sh(nodeb, "bob", "bobpass12345", [
            "news", "alt.chat", "f1", "FEDNEWS-REPLY", ".", "y", "q", "q",
        ])
        expect(out, "Article posted.")
        nodea.wait_until_succeeds(psql_check(
            "SELECT count(*) FROM articles WHERE remote_author='bob@nodeb'"
            " AND parent_id IS NOT NULL", "1"
        ), timeout=60)

    with subtest("news: cancel propagates to the peer"):
        out = sh(nodea, "alice", "alicepass123", [
            "news", "alt.chat", "c1", "y", "q", "q",
        ])
        expect(out, "Article cancelled.")
        nodeb.wait_until_succeeds(psql_check(
            "SELECT count(*) FROM articles WHERE subject='FEDNEWS-SUBJ'"
            " AND cancelled", "1"
        ), timeout=60)

    with subtest("news: catch-up sync recovers posts made while a node was down"):
        nodeb.succeed("systemctl stop alternate-sh")
        out = sh(nodea, "alice", "alicepass123", [
            "post alt.chat", "CATCHUP-SUBJ", "CATCHUP-BODY", ".", "y",
        ])
        expect(out, "Article posted.")
        nodeb.succeed("systemctl start alternate-sh")
        nodeb.wait_for_unit("alternate-sh.service")
        # Startup sync pulls everything nodea authored since the last mark.
        nodeb.wait_until_succeeds(psql_check(
            "SELECT count(*) FROM articles WHERE subject='CATCHUP-SUBJ'", "1"
        ), timeout=90)

    with subtest("news: <hostname>.* groups never leave their node"):
        # The group exists on BOTH nodes, so a refusal is namespace policy,
        # not a missing group.
        for m in (nodea, nodeb):
            psql(m, "INSERT INTO newsgroups (name, description)"
                    " VALUES ('nodea.local', 'local board') ON CONFLICT DO NOTHING")
        out = sh(nodea, "alice", "alicepass123", [
            "post nodea.local", "LOCAL-ONLY-SUBJ", "stays home", ".", "y",
        ])
        expect(out, "Article posted.")
        nodea.sleep(8)  # give any (wrong) push time to land
        n = psql(nodeb, "SELECT count(*) FROM articles WHERE subject='LOCAL-ONLY-SUBJ'")
        assert n == "0", f"local-namespace article leaked to the peer ({n} rows)"
  '';
}
