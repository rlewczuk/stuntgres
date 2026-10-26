---
name: remote-run
description: Build and test a task worktree on a remote host over SSH. Mirrors the worktree to the host with rsync, runs the command there and can copy generated files back. Use when the user names a host for builds, tests or benchmarks (e.g. "run quickcheck on apu1").
---

# Remote run

Run builds and tests for a task worktree on a remote host. The local worktree is the
source of truth: edit locally; `run` mirrors the worktree to the host, then runs the
command there over SSH.

## Rules

- The user names the host: a hostname, `user@host`, IP address or `~/.ssh/config`
  alias. If they didn't, ask. Never pick one yourself.
- Work in the task's worktree, never the main checkout (the script refuses it). Run
  the script from inside the worktree or pass `-C <worktree>`.
- Call `scripts/remote-run` from this skill's directory by absolute path.
- Never edit files in the remote mirror. Edit locally and run again.
- SSH runs without prompts (`BatchMode`). If it fails (exit 255), report the error
  to the user instead of working around it.
- The host needs bash, rsync and GNU `timeout` (or pass `--timeout 0`).

## Commands

```sh
remote-run [-C DIR] sync  HOST                                  # mirror only
remote-run [-C DIR] run   [--timeout SECS] [--no-sync] HOST 'COMMAND'
remote-run [-C DIR] pull  HOST PATH...                          # copy files back
remote-run [-C DIR] clean HOST                                  # delete the mirror
```

- `run` syncs, runs COMMAND with bash in the mirror and returns its exit status.
  COMMAND is shell code, as with ssh; pass it as one quoted string:
  `remote-run run --timeout 900 apu1 "make check RUN='^TestFoo$'"`.
- `--timeout` (default 1800 s) kills the remote command; exit 124 means it timed out.
  Use the deadlines from AGENTS.md. If the command can outlast your tool's own time
  limit, run it in the background; the remote timeout still applies.
- The mirror is `~/remote-run/<repo>/<worktree>` (`REMOTE_RUN_BASE` replaces
  `remote-run`). Each worktree has its own mirror, so parallel tasks don't collide.

## What is synced

Tracked and untracked files, so new files need no `git add`. Ignored paths and `.git`
stay local, so git commands don't work in the mirror. Files deleted locally are
deleted on the host; remote outputs in ignored paths survive later syncs.

## Copying results back

When a command writes files you want to keep (`make generate`, a profile), pull them
by path relative to the worktree root, then review `git diff`:

```sh
remote-run run apu1 'make generate'
remote-run pull apu1 internal/parser/grammar_gen.go
```

`pull` overwrites local files, prints what changed and never deletes anything.

## Remote environment

Commands run in a non-interactive login shell, which skips `~/.bashrc`. If a tool is
missing (`command not found`), find it on the host and add it to `env.sh` in the
remote base directory; every command sources it first. Tell the user what you added.

```sh
ssh HOST 'ls -d ~/go/*/bin /usr/local/go/bin'
ssh HOST 'mkdir -p ~/remote-run && echo "export PATH=\$HOME/go/latest/bin:\$PATH" >> ~/remote-run/env.sh'
```

## Cleanup

Run `remote-run clean HOST` when the task is finished, unless the user wants the
mirror kept.
