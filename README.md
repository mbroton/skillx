# skillx

All your AI agent skills in one git repo you own, on every machine you use.

Skills — directories with a `SKILL.md`, as used by Claude Code, Codex,
Cursor, and friends — end up scattered per machine and per agent. skillx
keeps them in a single git repo (private if you want) and symlinks them
into every agent:

```
your repo   skills/pr-review/            the only real files
hub         ~/.agents/skills/pr-review   -> repo
agents      ~/.claude/skills/pr-review   -> hub
            ~/.codex/skills/pr-review    -> hub
```

Add a skill once, push, `skillx update` everywhere else. No registry, no
daemon, no state files. Installed *means* linked — the symlinks are the
state, git is the sync. All git operations go through your `git`, so SSH
keys and credential helpers just work.

## Install

Grab a binary from [releases](https://github.com/mbroton/skillx/releases)
(Linux/macOS), or:

```sh
go install github.com/mbroton/skillx@latest
```

Needs `git` on `PATH`.

## Start

```sh
skillx init                    # once per machine: clone your skills repo
skillx add anthropics/skills   # vendor skills from any repo (or local path)
skillx use                     # pick which skills are active
```

New machine: `skillx init && skillx use --all`. That's it.

## Commands

| | |
|---|---|
| `skillx add <ref>` | Copy skills into your repo from `owner/repo`, a git URL, or a local path. Origin (repo, path, commit, branch) is recorded. Commits for you. |
| `skillx use` | Multi-select of every skill: check to install, uncheck to remove. `--all` installs everything, `--drop <x>` uninstalls, `--agent claude` limits scope. |
| `skillx use <skill> --local` | Copy a skill into the current project instead of linking globally (committable). |
| `skillx update` | Pull your repo. Links follow: skills removed on one machine get unlinked on the others. |
| `skillx update <skill>` | Re-fetch a vendored skill from its origin, show what changed, confirm, commit. |
| `skillx list` | Every skill: where it came from, which agents use it. |
| `skillx rm <skill>` | Delete a skill from the repo and remove its links. |
| `skillx status` | Inspect the hub and agent dirs: every link and directory, managed or not. `--offline` skips the fetch. |
| `skillx adopt` | Move skills installed by other tools (e.g. `npx skills`) into your repo, keeping them installed. |

Every prompt has a flag equivalent; without a terminal skillx never
prompts — it fails and names the flag.

## Safety

skillx only creates, changes, or deletes symlinks that resolve into its
own clone of your repo. Your files, other tools' installs, and anything
it can't identify are reported, never touched.

## Config

`~/.config/skillx/config.toml` (created by `skillx init`):

```toml
repo = "git@github.com:you/skills.git"
hub = "~/.agents/skills"

[agents.claude]
path = "~/.claude/skills"

[agents.codex]
path = "~/.codex/skills"

# any agent works:
# [agents.cursor]
# path = "~/.cursor/skills"
```

Vendored skills' origins live in `skillx.toml` at the root of your skills
repo, so `skillx update <skill>` can re-fetch them. Your own skills need
no entry — the `skills/` directory listing is the inventory.

## Design

The full mental model — bag/hub/spokes, the ownership rule that makes
deleting symlinks safe, and the plan/apply engine — is a five-minute
read: [ARCHITECTURE.md](ARCHITECTURE.md).

## Build

```sh
go build -o skillx .   # Go >= 1.26
go test ./...
```
