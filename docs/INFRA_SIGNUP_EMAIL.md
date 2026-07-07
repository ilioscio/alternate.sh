# Signup email infrastructure — deployment checklist

Transactional mail for account confirmation sends from `noreply@ilios.dev`
via your existing `simple-nixos-mailserver` on `mail.ilios.dev`. Two repos are
involved and one shared password links them:

- **ilios.dev** (mail server host) stores the *bcrypt hash* of the SMTP
  password as a mail login account.
- **nrvps** (alternate.sh host) stores the *plaintext* of the same password
  for the SMTP client to authenticate with.

Generate the password once; both secrets derive from it.

---

## 1. Generate the shared password (on your laptop)

```sh
# A strong random password:
SMTP_PW=$(head -c 24 /dev/urandom | base64 | tr -d '/+=' | head -c 32)
echo "plaintext: $SMTP_PW"

# The bcrypt hash the mailserver stores:
nix shell nixpkgs#mkpasswd -c mkpasswd -sm bcrypt "$SMTP_PW"
# copy this hash for step 2
```

Keep `$SMTP_PW` (plaintext) for step 3.

---

## 2. ilios.dev — add the noreply mail account

**`secrets.nix`** — register a new secret:
```nix
"secrets/mail-noreply-password-hash.age".publicKeys = allKeys;
```

Create it with the **bcrypt hash** from step 1:
```sh
cd ~/Projects/ilios.dev
agenix -e secrets/mail-noreply-password-hash.age
# paste the hash, save
```

**`configuration.nix`** — declare the secret and the login account:
```nix
# near the other age.secrets:
age.secrets.mail-noreply-password-hash = {
  file = ./secrets/mail-noreply-password-hash.age;
};

# inside `mailserver.loginAccounts`:
loginAccounts = {
  "admin@ilios.dev" = {
    hashedPasswordFile = config.age.secrets.mail-admin-password.path;
  };
  "noreply@ilios.dev" = {
    hashedPasswordFile = config.age.secrets.mail-noreply-password-hash.path;
    # Send-only account; no aliases needed. It can still receive bounces.
  };
};
```

Deploy ilios.dev. Verify the account exists (e.g. `swaks` or an authenticated
submission test to `mail.ilios.dev:587`).

> DKIM/SPF/DMARC already cover `ilios.dev` (selector `mail`), so mail from
> `noreply@ilios.dev` authenticates cleanly — this is exactly why we send from
> your owned domain rather than the not-yet-registered product domain.

---

## 3. nrvps — give alternate.sh the SMTP password + enable email

**`secrets.nix`** (or wherever nrvps maps secrets) — register:
```nix
"alternate-sh-smtp-password.age".publicKeys = allKeys;  # laptop + alternate.sh host key
```

Create it with the **plaintext** `$SMTP_PW` from step 1:
```sh
cd ~/Projects/nrvps   # branch: alternate
agenix -e secrets/alternate-sh-smtp-password.age
# paste the plaintext password, save
```

Declare the secret readable by the service user, and wire the module options:
```nix
age.secrets.alternate-sh-smtp-password = {
  file = ./secrets/alternate-sh-smtp-password.age;
  owner = "alternate-sh";   # the service user must read it at send time
  group = "alternate-sh";
  mode  = "0400";
};

services.alternate-sh = {
  # ...existing config...
  web.publicURL = "https://alternate.sh";   # or the current public URL until the name lands
  email = {
    enable       = true;
    host         = "mail.ilios.dev";
    port         = 465;                       # SMTPS / implicit TLS (auto-detected)
    username     = "noreply@ilios.dev";
    from         = "noreply@ilios.dev";
    fromName     = "alternate.sh";            # branding; change when the name is chosen
    passwordFile = config.age.secrets.alternate-sh-smtp-password.path;
    # skipTLSVerify stays false — mail.ilios.dev has real ACME certs.
  };
};
```

Deploy nrvps.

> **Port 465 vs 587.** Your mailserver currently listens on **465 (implicit
> TLS)** only; `enableSubmission = true` (587/STARTTLS) is not set. The client
> auto-detects implicit TLS for port 465, so **use 465 and no mail-server
> change is needed.** If you'd rather use 587, add `mailserver.enableSubmission
> = true;` on ilios.dev and set `port = 587;` here (STARTTLS is used for any
> non-465 port; `implicitTLS = true;` can force it otherwise).

---

## 4. Verify end to end

1. Open the web frontend → **[ register ]** → submit a real address you control.
2. Confirm the email arrives from `noreply@ilios.dev` (check SPF/DKIM pass in
   the headers) with both a link and a 6-digit code.
3. Click the link **or** enter the code → account is created → log in.

If mail lands in spam, check the receiving side's DMARC report; the sending
domain is fully aligned so it should pass, but new sending patterns sometimes
need a warm-up.

---

## Notes / knobs

- **`web.publicURL`** must be the externally reachable base (no trailing
  slash); it builds the `/confirm?token=...` link. Until `alternate.sh` is
  registered, point it at the current public URL (IP or interim domain).
- **Branding** in email copy comes from `services.alternate-sh.hostname` and
  `email.fromName` — the two places to change when the product name is final.
- **Rate limits** currently in code: 3 signups/IP/hour, 10 logins/IP/5min,
  plus `limits.mail_per_hour` (50) and `limits.news_per_day` (20) enforced for
  non-admins. Tune in the module's `limits` block / by editing the limiter
  constructors if needed.
- If you ever want to **pause signups**, set `email.enable = false` — the
  signup endpoint then returns "signups are currently closed" and the
  registration form's submit fails gracefully.
