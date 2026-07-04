{ config, lib, pkgs, ... }:

let
  cfg = config.services.alternate-sh;

  # Generate the TOML config file from module options.
  configFile = pkgs.writeText "alternate-sh-config.toml" ''
    [server]
    hostname = "${cfg.hostname}"
    motd     = "${lib.escape [ ''"'' "\\" ] cfg.motd}"

    [ssh]
    port     = ${toString cfg.ssh.port}
    host_key = "${cfg.stateDir}/ssh_host_ed25519_key"

    [web]
    port     = ${toString cfg.web.port}
    ${lib.optionalString (cfg.web.tlsCert != "") ''tls_cert = "${cfg.web.tlsCert}"''}
    ${lib.optionalString (cfg.web.tlsKey  != "") ''tls_key  = "${cfg.web.tlsKey}"''}

    [database]
    dsn = "${
      if cfg.database.createLocally
      then "postgres:///${cfg.database.name}?host=/var/run/postgresql"
      else cfg.database.dsn
    }"

    [federation]
    assp_port = ${toString cfg.federation.asspPort}
    nntp_port = ${toString cfg.federation.nntpPort}
    smtp_port = ${toString cfg.federation.smtpPort}
    enabled   = ${if cfg.federation.enable then "true" else "false"}

    [limits]
    max_users     = ${toString cfg.limits.maxUsers}
    mail_per_hour = ${toString cfg.limits.mailPerHour}
    news_per_day  = ${toString cfg.limits.newsPerDay}
  '';

in {
  options.services.alternate-sh = {
    enable = lib.mkEnableOption "alternate.sh retro Unix timeshare social network";

    package = lib.mkOption {
      type = lib.types.package;
      description = "The alternate-sh package to use.";
    };

    hostname = lib.mkOption {
      type = lib.types.str;
      description = "Public hostname of this node (shown in prompts and finger output).";
      example = "alternate.sh";
    };

    motd = lib.mkOption {
      type = lib.types.str;
      default = "Welcome to alternate.sh";
      description = "Message of the day shown to users on login.";
    };

    stateDir = lib.mkOption {
      type = lib.types.str;
      default = "/var/lib/alternate-sh";
      description = "Directory for persistent state (SSH host key, etc.).";
    };

    ssh = {
      port = lib.mkOption {
        type = lib.types.port;
        default = 2222;
        description = "Port for the SSH server. Use 22 for standard SSH (requires CAP_NET_BIND_SERVICE).";
      };
    };

    web = {
      port = lib.mkOption {
        type = lib.types.port;
        default = 8080;
        description = "Port for the WebSocket/HTTP server.";
      };
      tlsCert = lib.mkOption {
        type = lib.types.str;
        default = "";
        description = "Path to TLS certificate (leave empty for plain HTTP, use nginx for TLS termination).";
      };
      tlsKey = lib.mkOption {
        type = lib.types.str;
        default = "";
        description = "Path to TLS private key.";
      };
    };

    database = {
      createLocally = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = "Create a local PostgreSQL database. Uses peer auth via Unix socket.";
      };
      name = lib.mkOption {
        type = lib.types.str;
        default = "alternate-sh";
        description = "Database name (used when createLocally = true).";
      };
      dsn = lib.mkOption {
        type = lib.types.str;
        default = "";
        description = "Full PostgreSQL DSN (used when createLocally = false).";
        example = "postgres://user:pass@localhost/alternate-sh";
      };
    };

    federation = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = "Enable federation with other alternate.sh nodes.";
      };
      asspPort = lib.mkOption {
        type = lib.types.port;
        default = 4119;
        description = "Port for the ASSP (Alternate Shell Server Protocol) federation listener.";
      };
      nntpPort = lib.mkOption {
        type = lib.types.port;
        default = 119;
        description = "Port for NNTP news federation.";
      };
      smtpPort = lib.mkOption {
        type = lib.types.port;
        default = 25;
        description = "Port for SMTP inter-server mail.";
      };
    };

    limits = {
      maxUsers = lib.mkOption {
        type = lib.types.int;
        default = 500;
      };
      mailPerHour = lib.mkOption {
        type = lib.types.int;
        default = 50;
      };
      newsPerDay = lib.mkOption {
        type = lib.types.int;
        default = 20;
      };
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Open SSH and web ports in the firewall.";
    };

    nginx = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = "Configure nginx as a TLS-terminating reverse proxy for the web frontend.";
      };
      domain = lib.mkOption {
        type = lib.types.str;
        default = cfg.hostname;
        description = "Domain name for the nginx virtual host and ACME certificate.";
      };
    };
  };

  config = lib.mkIf cfg.enable {
    # ── System user ──────────────────────────────────────────────────────────
    users.users.alternate-sh = {
      isSystemUser = true;
      group = "alternate-sh";
      home = cfg.stateDir;
      description = "alternate.sh service user";
    };
    users.groups.alternate-sh = {};

    # ── PostgreSQL ────────────────────────────────────────────────────────────
    services.postgresql = lib.mkIf cfg.database.createLocally {
      enable = true;
      ensureDatabases = [ cfg.database.name ];
      ensureUsers = [{
        name = "alternate-sh";
        ensureDBOwnership = true;
      }];
    };

    # ── State directory ───────────────────────────────────────────────────────
    systemd.tmpfiles.rules = [
      "d '${cfg.stateDir}' 0750 alternate-sh alternate-sh - -"
    ];

    # ── Systemd service ───────────────────────────────────────────────────────
    systemd.services.alternate-sh = {
      description = "alternate.sh retro Unix timeshare social network";
      wantedBy = [ "multi-user.target" ];
      after    = [ "network.target" ]
             ++ lib.optional cfg.database.createLocally "postgresql.service";
      requires = lib.optional cfg.database.createLocally "postgresql.service";

      # Generate SSH host key on first start if missing.
      preStart = ''
        if [ ! -f "${cfg.stateDir}/ssh_host_ed25519_key" ]; then
          ${pkgs.openssh}/bin/ssh-keygen \
            -t ed25519 \
            -f "${cfg.stateDir}/ssh_host_ed25519_key" \
            -N "" \
            -C "alternate-sh@${cfg.hostname}"
          echo "Generated SSH host key."
        fi
      '';

      serviceConfig = {
        User  = "alternate-sh";
        Group = "alternate-sh";

        ExecStart = "${cfg.package}/bin/alternate-sh serve --config ${configFile}";

        Restart          = "on-failure";
        RestartSec       = "5s";

        # State directory
        StateDirectory     = "alternate-sh";
        StateDirectoryMode = "0750";

        # Hardening
        PrivateTmp          = true;
        ProtectSystem       = "strict";
        ProtectHome         = true;
        NoNewPrivileges     = true;
        ReadWritePaths      = [ cfg.stateDir ];

        # Allow binding to low ports if configured.
        AmbientCapabilities =
          lib.optionals (cfg.ssh.port < 1024 || cfg.web.port < 1024)
            [ "CAP_NET_BIND_SERVICE" ];
        CapabilityBoundingSet =
          lib.optionals (cfg.ssh.port < 1024 || cfg.web.port < 1024)
            [ "CAP_NET_BIND_SERVICE" ];
      };
    };

    # ── Firewall ──────────────────────────────────────────────────────────────
    networking.firewall.allowedTCPPorts = lib.optionals cfg.openFirewall (
      [ cfg.ssh.port cfg.web.port ]
      ++ lib.optionals cfg.federation.enable [
        cfg.federation.asspPort
        cfg.federation.nntpPort
        cfg.federation.smtpPort
      ]
    );

    # ── Nginx reverse proxy (optional) ────────────────────────────────────────
    services.nginx = lib.mkIf cfg.nginx.enable {
      enable = true;
      recommendedProxySettings = true;
      recommendedTlsSettings   = true;
      recommendedGzipSettings  = true;

      virtualHosts.${cfg.nginx.domain} = {
        enableACME = true;
        forceSSL   = true;

        locations = {
          # WebSocket upgrade
          "/ws" = {
            proxyPass            = "http://127.0.0.1:${toString cfg.web.port}";
            proxyWebsockets      = true;
            extraConfig = ''
              proxy_read_timeout 3600s;
              proxy_send_timeout 3600s;
            '';
          };
          # Everything else (static frontend, public pages)
          "/" = {
            proxyPass = "http://127.0.0.1:${toString cfg.web.port}";
          };
        };
      };
    };
  };
}
