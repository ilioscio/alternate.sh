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
    environment.systemPackages = [ package pkgs.sshpass config.services.postgresql.package ];
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
  '';
}
