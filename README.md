# tempvm

`tempvm` creates a fresh Linux microVM for every SSH connection:

```console
ssh linux@tempvm
```

Open `http://tempvm/` from the tailnet for the landing and status page.

The SSH gateway creates a Kubernetes
[`Sandbox`](https://github.com/kubernetes-sigs/agent-sandbox) using the
configured `RuntimeClass`, waits for its Linux guest to become ready, and
relays the SSH connection into it. The default maximum session lifetime is
four hours and the default concurrency limit is three VMs. A session is
warned shortly before its TTL, then the SSH relay and per-session Kubernetes
objects are deleted. Set `--max-session-ttl=0` or `TEMPVM_MAX_SESSION_TTL=0`
to disable the TTL, or `--max-sessions=0` / `TEMPVM_MAX_SESSIONS=0` to disable
the cap.

Every per-session Sandbox, Service, and Secret has the labels
`app.kubernetes.io/managed-by=tempvm`, `tempvm/session`, and
`tempvm/started-at`. The gateway also sets the Sandbox `shutdownTime` backstop
(supported by the deployed CRD) and removes labeled session resources left by a
gateway restart on startup.

The gateway emits one JSON `slog` line for each session transition:
`session_start`, `session_boot_failed`, `session_refused`,
`session_ttl_expired`, and `session_stop`. Audit fields include `ts`, session
and VM names, source IP, SSH user, Tailscale login/node/tags/IP when available,
and stop duration/reason. No key material is logged. `/healthz` and the
landing page expose active and maximum sessions.

Tailscale WhoIs is optional and disabled unless a socket is configured. When a
mounted `tailscaled` LocalAPI socket is available, set `--tailscale-socket`
(or `TEMPVM_TAILSCALE_SOCKET`) to resolve identity. `--allow-login` /
`TEMPVM_ALLOW_LOGIN` and
`--allow-tag` / `TEMPVM_ALLOW_TAG` add an allow-list; without those flags the
gateway remains permissive when identity is unavailable. Configuring an
allow-list without a LocalAPI socket is rejected at startup. WhoIs is useful
only when the gateway sees the original tailnet source address; a separate
Tailscale proxy may replace it with the proxy address, leaving identity
`unknown` even when LocalAPI is reachable.

## Images

- `ghcr.io/keiretsu-labs/tempvm-gateway` contains the SSH gateway.
- `ghcr.io/keiretsu-labs/tempvm-linux` contains the ephemeral Ubuntu userland.

Neither image contains credentials. Each connection gets a newly generated SSH
key, while the guest generates a new host key at boot.

## Development

```console
make test
```
