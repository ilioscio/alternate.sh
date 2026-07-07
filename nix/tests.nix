# NixOS VM integration test canvassing the shell commands end to end.
#
# Boots a VM with PostgreSQL + the alternate-sh service (via our own module),
# creates users with the adduser CLI, then drives real SSH sessions with
# scripted input and asserts on the text output. DB side effects are verified
# with psql where output scraping is not ergonomic.
#
# Run with:  nix build .#checks.x86_64-linux.commands
{ pkgs, package, module }:

pkgs.testers.runNixOSTest {
  name = "alternate-sh-commands";

  nodes.machine = { config, ... }: {
    imports = [ module ];

    services.alternate-sh = {
      enable = true;
      inherit package;
      hostname = "testnode";
      ssh.port = 2222;
      web.port = 8080;
      openFirewall = false;
    };

    environment.systemPackages = [
      package
      pkgs.sshpass
      pkgs.curl
      config.services.postgresql.package
    ];

    virtualisation.memorySize = 1024;
  };

  testScript = ''
    import base64
    from datetime import datetime, timedelta

    SSH = (
        "sshpass -p {pw} ssh -tt "
        "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "
        "-o LogLevel=ERROR -o ConnectTimeout=10 -p 2222 {user}@127.0.0.1"
    )

    def sh(user, pw, lines):
        """Run one SSH REPL session feeding the given input lines; return transcript."""
        script = "\n".join(lines + ["logout"]) + "\n"
        b64 = base64.b64encode(script.encode()).decode()
        _, out = machine.execute(
            f"echo {b64} | base64 -d | " + SSH.format(user=user, pw=pw),
            timeout=60,
        )
        return out

    def expect(out, *needles):
        for n in needles:
            if n not in out:
                print("---- transcript ----")
                print(out)
                print("--------------------")
                raise AssertionError(f"expected {n!r} in session output")

    def psql(query):
        return machine.succeed(
            f"runuser -u alternate-sh -- psql -d alternate-sh -tAc \"{query}\""
        ).strip()

    machine.wait_for_unit("postgresql.service")
    machine.wait_for_unit("alternate-sh.service")
    machine.wait_for_open_port(2222)
    machine.wait_for_open_port(8080)

    # ── Fixtures ──────────────────────────────────────────────────────────────
    machine.succeed(
        "printf '[database]\\ndsn = \"postgres:///alternate-sh?host=/var/run/postgresql\"\\n'"
        " > /tmp/cli.toml && chmod 644 /tmp/cli.toml"
    )
    adduser = "runuser -u alternate-sh -- alternate-sh adduser --config /tmp/cli.toml"
    machine.succeed(f"{adduser} --username alice --password alicepass123 --name Alice --admin")
    machine.succeed(f"{adduser} --username bob --password bobpass12345 --name Bob")
    machine.succeed(f"{adduser} --username carol --password carolpass123 --name Carol")

    # Deterministic fortune
    psql("DELETE FROM fortunes")
    psql("INSERT INTO fortunes (body) VALUES ('FORTUNE-CANARY')")

    # ── Basic commands + profile editors (alice) ──────────────────────────────
    with subtest("banner, help, who, w, uptime, last, mesg, fortune, motd"):
        cal_date = datetime.now() + timedelta(days=7)
        out = sh("alice", "alicepass123", [
            "help",
            "help mail",
            "help rn",
            "who",
            "w",
            "uptime",
            "last",
            "mesg",
            "fortune",
            "motd",
            "chfn", "Alice Cooper", "Machine Room", "555-0100",
            "project ALICE-PROJECT-CANARY",
            "plan", "PLAN-CANARY", ".",
            "public", "PUBLIC-CANARY", ".",
            "calendar",
            "calendar edit", f"{cal_date.month}/{cal_date.day} CAL-CANARY", ".",
        ])
        expect(out,
            "alice@testnode",                        # prompt/banner hostname
            "Welcome to alternate.sh",               # seeded MOTD at login
            "FORTUNE-CANARY",                        # login fortune
            "alternate.sh — available commands",     # help
            "Type 'help <command>' for details.",
            "read your mailbox",                     # help mail
            "browse newsgroups",                     # help rn (alias resolution)
            "USER",                                  # w header
            "user",                                  # uptime
            "mesg: messages are on",
            "Finger information updated.",
            "Project set to: ALICE-PROJECT-CANARY",
            "Plan updated.",
            "Public page updated.",
            "No calendar entries.",
            "Calendar saved.",
            "Goodbye.",
        )

    with subtest("finger, calendar, public reflect saved data (fresh session)"):
        out = sh("alice", "alicepass123", [
            "finger",
            "finger alice",
            "calendar",
            "public alice",
        ])
        expect(out,
            "Login",                    # finger list header
            "Login: alice",
            "Alice Cooper",             # chfn name persisted
            "Plan:",
            "PLAN-CANARY",
            "Project: ALICE-PROJECT-CANARY",
            "CAL-CANARY",               # upcoming calendar entry
            "PUBLIC-CANARY",            # public page body
        )
        assert psql("SELECT office FROM users WHERE username='alice'") == "Machine Room"
        assert psql("SELECT home_phone FROM users WHERE username='alice'") == "555-0100"

    with subtest("web: index and public page"):
        idx = machine.succeed("curl -sf http://127.0.0.1:8080/")
        assert "alternate.sh" in idx
        pub = machine.succeed("curl -sf http://127.0.0.1:8080/~alice")
        assert "PUBLIC-CANARY" in pub

    # ── MOTD set + system messages ────────────────────────────────────────────
    with subtest("motd set (admin) and permission denial (non-admin)"):
        out = sh("alice", "alicepass123", [
            "msgs",
            "motd set", "MOTD-CANARY", ".",
        ])
        expect(out, "No new system messages.", "MOTD updated.")
        out = sh("bob", "bobpass12345", ["motd set", "wall nope"])
        expect(out, "motd set: permission denied", "wall: permission denied")

    with subtest("msgs: unread hint, read once, then empty"):
        psql("INSERT INTO system_messages (body) VALUES ('SYSMSG-CANARY')")
        out = sh("alice", "alicepass123", ["msgs", "msgs"])
        expect(out,
            "MOTD-CANARY",                     # new MOTD in login banner
            "new system message(s)",           # login hint
            "--- Message 1 ---",
            "SYSMSG-CANARY",
            "No new system messages.",         # second msgs: already read
        )

    # ── Mail ──────────────────────────────────────────────────────────────────
    with subtest("mail: send"):
        out = sh("alice", "alicepass123", [
            "mail bob", "MAIL-SUBJ-CANARY", "MAIL-BODY-CANARY", ".", "y",
        ])
        expect(out, "To: bob", "Message sent to bob.")
        n = psql(
            "SELECT count(*) FROM mail m JOIN users u ON u.id = m.recipient_id"
            " WHERE u.username='bob'"
        )
        assert n == "1", f"expected 1 mail row for bob, got {n}"

    with subtest("mail: unread hint on login, read, body shown"):
        out = sh("bob", "bobpass12345", ["mail", "1", "q"])
        expect(out,
            "unread mail message(s)",   # login hint
            "MAIL-SUBJ-CANARY",         # mailbox list
            "From:    alice",
            "MAIL-BODY-CANARY",
        )

    # ── Vacation auto-reply ───────────────────────────────────────────────────
    with subtest("vacation: set message, enable, auto-reply fires once"):
        out = sh("bob", "bobpass12345", [
            "vacation msg", "VACATION-CANARY", ".",
            "vacation on",
        ])
        expect(out, "Vacation message saved.", "Vacation mode enabled.")

        out = sh("alice", "alicepass123", [
            "mail bob", "Hi again", "second message", ".", "y",
        ])
        expect(out, "Message sent to bob.", "[Auto-reply from bob received]")

        out = sh("alice", "alicepass123", ["mail", "1", "q"])
        expect(out, "Auto-reply:", "VACATION-CANARY")

    with subtest("vacation off + mail delete"):
        out = sh("bob", "bobpass12345", [
            "vacation off",
            "mail", "d1", "q",
        ])
        expect(out, "Vacation mode disabled.", "Message 1 deleted.")

    # ── News ──────────────────────────────────────────────────────────────────
    with subtest("news: browse groups, post article"):
        out = sh("alice", "alicepass123", [
            "news", "alt.chat",
            "p", "NEWS-SUBJ-CANARY", "NEWS-BODY-CANARY", ".", "y",
            "q", "q",
        ])
        expect(out, "alt.announce", "alt.chat", "Article posted.")

    with subtest("news: another user sees and reads the article"):
        out = sh("bob", "bobpass12345", [
            "news", "alt.chat", "1", "x", "q", "q",
        ])
        expect(out, "NEWS-SUBJ-CANARY", "NEWS-BODY-CANARY")

    with subtest("news: cancel — non-author denied, author succeeds"):
        out = sh("bob", "bobpass12345", [
            "news", "alt.chat", "c1", "y", "q", "q",
        ])
        expect(out, "cancel: you can only cancel your own articles")

        out = sh("alice", "alicepass123", [
            "news", "alt.chat", "c1", "y", "q", "q",
        ])
        expect(out, "Article cancelled.", "no articles remain.")
        n = psql(
            "SELECT count(*) FROM articles a JOIN newsgroups g ON g.id = a.newsgroup_id"
            " WHERE g.name='alt.chat' AND NOT a.cancelled"
        )
        assert n == "0", f"expected 0 live articles in alt.chat, got {n}"

    with subtest("post: direct posting to a group"):
        out = sh("alice", "alicepass123", [
            "post alt.dreams.computing", "POST-SUBJ-CANARY", "POST-BODY-CANARY", ".", "y",
        ])
        expect(out, "Posting to: alt.dreams.computing", "Article posted.")
        n = psql(
            "SELECT count(*) FROM articles a JOIN newsgroups g ON g.id = a.newsgroup_id"
            " WHERE g.name='alt.dreams.computing'"
        )
        assert n == "1", f"expected 1 article in alt.dreams.computing, got {n}"

    # ── write / wall (multi-user, live delivery) ──────────────────────────────
    with subtest("write and wall reach a logged-in user"):
        # bob sits at the prompt with no input for 12s, capturing notices
        machine.execute(
            "sleep 12 | " + SSH.format(user="bob", pw="bobpass12345")
            + " > /tmp/bob-bg.out 2>&1 &"
        )
        machine.sleep(3)  # let bob register

        out = sh("alice", "alicepass123", [
            "who",
            "write bob WRITE-CANARY",
            "write carol hello",
            "wall WALL-CANARY",
        ])
        expect(out,
            "bob",                                # who shows bob online
            "write: carol is not logged in",
            "wall: broadcast sent to 1 user",
        )

        machine.sleep(11)  # bob's stdin closes after 12s → session ends
        bg = machine.succeed("cat /tmp/bob-bg.out")
        expect(bg,
            "Message from alice",
            "WRITE-CANARY",
            "Broadcast from alice",
            "WALL-CANARY",
        )

    with subtest("mesg persists and blocks write"):
        out = sh("bob", "bobpass12345", ["mesg n"])
        expect(out, "mesg: messages disabled")
        assert psql("SELECT mesg_on FROM users WHERE username='bob'") == "f"

        machine.execute(
            "sleep 8 | " + SSH.format(user="bob", pw="bobpass12345")
            + " > /tmp/bob-bg2.out 2>&1 &"
        )
        machine.sleep(3)
        out = sh("alice", "alicepass123", ["write bob blocked?"])
        expect(out, "write: bob has messages turned off")
        machine.sleep(6)

        out = sh("bob", "bobpass12345", ["mesg y"])
        expect(out, "mesg: messages enabled")
        assert psql("SELECT mesg_on FROM users WHERE username='bob'") == "t"

    # ── passwd ────────────────────────────────────────────────────────────────
    with subtest("passwd changes the password and the new one works"):
        out = sh("carol", "carolpass123", [
            "passwd", "carolpass123", "carolnewpass1", "carolnewpass1",
        ])
        expect(out, "Password changed.")

        out = sh("carol", "carolnewpass1", ["uptime"])
        expect(out, "carol@testnode", "user")

        # Old password must no longer authenticate.
        status, _ = machine.execute(
            "echo | " + SSH.format(user="carol", pw="carolpass123"), timeout=30
        )
        assert status != 0, "old password still authenticates after passwd"

    # ── last shows accumulated history ────────────────────────────────────────
    with subtest("last shows login records for multiple users"):
        out = sh("alice", "alicepass123", ["last"])
        expect(out, "alice", "bob", "carol")
  '';
}
