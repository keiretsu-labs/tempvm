# tempvm

`tempvm` creates a fresh Linux microVM for every SSH connection:

```console
ssh linux@tempvm
```

Open `http://tempvm/` from the tailnet for the landing and status page.

The SSH gateway creates a Kubernetes
[`Sandbox`](https://github.com/kubernetes-sigs/agent-sandbox) using the
configured `RuntimeClass`, waits for its Linux guest to become ready, and
relays the SSH connection into it. The Sandbox and its session credentials are
deleted as soon as the client disconnects. There is no fixed TTL and no
application-level concurrency limit.

## Images

- `ghcr.io/keiretsu-labs/tempvm-gateway` contains the SSH gateway.
- `ghcr.io/keiretsu-labs/tempvm-linux` contains the ephemeral Ubuntu userland.

Neither image contains credentials. Each connection gets a newly generated SSH
key, while the guest generates a new host key at boot.

## Development

```console
make test
```
