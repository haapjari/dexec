# dexec

Small wrapper that pipes commands from your machine to devcontainer.

## Examples

```bash
dexec --run=opencode
dexec -r=opencode
dexec run=opencode
dexec --run="opencode --version"
dexec --run="opencode run arg" > test.txt
dexec --run=opencode --no-tty
dexec --run=opencode --startup-timeout=90s --status-interval=1s
```

## Flags

- `--run`, `-r`: command to run inside the workspace.
- `--no-tty`: disable pseudo-TTY allocation for `docker exec`.
- `--startup-timeout`: maximum time to wait for startup/lock contention (default `60s`).
- `--status-interval`: status polling interval during startup checks (default `500ms`).

---

# License

MIT
