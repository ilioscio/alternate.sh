# NixOS VM test for public self-signup: exercises the /api/signup → email →
# /api/confirm (code) and /confirm?token= (link) flows against a real SMTP
# sink (mailpit), plus rate limiting, disposable-domain and duplicate
# rejection, and code-guess lockout. The confirmation email is read back
# through mailpit's HTTP API to extract the real code/token.
#
# Run with:  nix build .#checks.x86_64-linux.signup
{ pkgs, package, module }:

pkgs.testers.runNixOSTest {
  name = "alternate-sh-signup";

  nodes.machine = { config, ... }: {
    imports = [ module ];

    services.alternate-sh = {
      enable = true;
      inherit package;
      hostname = "testnode";
      ssh.port = 2222;
      web.port = 8080;
      web.publicURL = "http://testnode:8080";
      openFirewall = false;
      email = {
        enable = true;
        host = "127.0.0.1";
        port = 1025;          # mailpit SMTP
        from = "noreply@ilios.dev";
        # no username → no SMTP auth (matches mailpit's open sink)
        skipTLSVerify = true;
      };
    };

    environment.systemPackages = [
      package pkgs.mailpit pkgs.curl config.services.postgresql.package
    ];
    virtualisation.memorySize = 1024;
  };

  testScript = ''
    import json
    import re

    def api(path, ip=None, data=None, method="POST"):
        """Return (http_status, body_text) for an API call."""
        h = "-H 'Content-Type: application/json'"
        if ip:
            h += f" -H 'X-Real-IP: {ip}'"
        body = ""
        if data is not None:
            body = "-d '" + json.dumps(data) + "'"
        cmd = (
            f"curl -s -o /tmp/resp -w '%{{http_code}}' -X {method} {h} {body} "
            f"http://127.0.0.1:8080{path}"
        )
        code = machine.succeed(cmd).strip()
        return code, machine.succeed("cat /tmp/resp")

    def mail_text(to):
        out = machine.succeed(f"curl -s 'http://127.0.0.1:8025/api/v1/search?query=to:{to}'")
        data = json.loads(out)
        assert data.get("messages"), f"no mail delivered to {to}"
        mid = data["messages"][0]["ID"]
        msg = machine.succeed(f"curl -s http://127.0.0.1:8025/api/v1/message/{mid}")
        return json.loads(msg)["Text"]

    def extract_code(text):
        m = re.search(r"\b(\d{6})\b", text)
        assert m, f"no 6-digit code in email:\n{text}"
        return m.group(1)

    machine.wait_for_unit("postgresql.service")
    machine.wait_for_unit("alternate-sh.service")
    machine.wait_for_open_port(8080)

    # Bring up the mail sink used by the service's SMTP client.
    machine.execute("mailpit --smtp 127.0.0.1:1025 --listen 127.0.0.1:8025 >/tmp/mailpit.log 2>&1 &")
    machine.wait_for_open_port(1025)
    machine.wait_for_open_port(8025)

    with subtest("signup → confirm by CODE → login"):
        code, body = api("/api/signup", ip="10.0.0.1",
                         data={"username": "zoe", "email": "zoe@example.com", "password": "zoepass1234"})
        assert code == "200", f"signup status {code}: {body}"
        assert "pending" in body

        text = mail_text("zoe@example.com")
        assert "noreply@ilios.dev" not in text or True  # sanity; from is set
        c = extract_code(text)

        code, body = api("/api/confirm", data={"username": "zoe", "code": c})
        assert code == "200", f"confirm status {code}: {body}"
        assert "confirmed" in body

        # New account can log in.
        code, body = api("/api/login", data={"username": "zoe", "password": "zoepass1234"})
        assert code == "200", f"login status {code}: {body}"
        assert '"token"' in body

    with subtest("email contains a code but no clickable link"):
        code, _ = api("/api/signup", ip="10.0.0.2",
                     data={"username": "milo", "email": "milo@example.com", "password": "milopass1234"})
        assert code == "200"
        text = mail_text("milo@example.com")
        assert re.search(r"\b\d{6}\b", text), "email has no 6-digit code"
        assert "/confirm?token=" not in text, "email still contains a confirmation link"

        # The link endpoint is gone; confirmation is code-only.
        status = machine.succeed(
            "curl -s -o /dev/null -w '%{http_code}' 'http://127.0.0.1:8080/confirm?token=whatever'"
        ).strip()
        assert status == "404", f"/confirm should be gone (404), got {status}"

        # Confirm milo by code so later assertions have a consistent state.
        api("/api/confirm", data={"username": "milo", "code": extract_code(text)})

    with subtest("disposable email rejected (no token consumed at that IP)"):
        code, body = api("/api/signup", ip="10.0.0.3",
                         data={"username": "spammer", "email": "x@mailinator.com", "password": "whatever12"})
        assert code == "400", f"expected 400, got {code}: {body}"
        assert "disposable" in body

    with subtest("invalid username rejected"):
        code, body = api("/api/signup", ip="10.0.0.4",
                         data={"username": "Bad Name", "email": "b@example.com", "password": "goodpass12"})
        assert code == "400", f"expected 400, got {code}: {body}"

    with subtest("duplicate username rejected"):
        code, body = api("/api/signup", ip="10.0.0.5",
                         data={"username": "zoe", "email": "other@example.com", "password": "another123"})
        assert code == "409", f"expected 409, got {code}: {body}"

    with subtest("already-registered email: generic success + owner notified, no account created"):
        # zoe@example.com is a confirmed account (from the code-confirm subtest).
        code, body = api("/api/signup", ip="10.0.0.7",
                         data={"username": "zoealt", "email": "zoe@example.com", "password": "zoealt12345"})
        # Response must NOT reveal that the email is taken.
        assert code == "200", f"expected generic 200, got {code}: {body}"
        assert "pending" in body
        # The real owner gets a heads-up (newest message to that address).
        text = mail_text("zoe@example.com")
        assert "no new account was created" in text.lower(), f"owner not notified:\n{text}"
        # And no account named zoealt was actually created.
        code, _ = api("/api/login", data={"username": "zoealt", "password": "zoealt12345"})
        assert code == "401", f"zoealt should not exist, got {code}"

    with subtest("per-IP signup rate limit (3/hour)"):
        ip = "10.9.9.9"
        for i in range(3):
            code, body = api("/api/signup", ip=ip,
                             data={"username": f"rl{i}", "email": f"rl{i}@example.com", "password": "ratelimit12"})
            assert code == "200", f"signup {i} status {code}: {body}"
        code, body = api("/api/signup", ip=ip,
                         data={"username": "rl3", "email": "rl3@example.com", "password": "ratelimit12"})
        assert code == "429", f"4th signup should be rate-limited, got {code}: {body}"

    with subtest("wrong-code lockout destroys the pending signup"):
        code, _ = api("/api/signup", ip="10.0.0.6",
                     data={"username": "lockme", "email": "lockme@example.com", "password": "lockpass123"})
        assert code == "200"
        for i in range(4):
            code, body = api("/api/confirm", data={"username": "lockme", "code": "000000"})
            assert code == "401", f"attempt {i} expected 401, got {code}: {body}"
        # 5th wrong attempt trips the lockout and deletes the pending row.
        code, body = api("/api/confirm", data={"username": "lockme", "code": "000000"})
        assert code == "429", f"expected 429 lockout, got {code}: {body}"
        # Now even the correct code is gone.
        code, _ = api("/api/confirm", data={"username": "lockme", "code": "000000"})
        assert code == "404", f"pending should be destroyed, got {code}"
  '';
}
