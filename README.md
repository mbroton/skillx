# skillx

Collect AI agent skills — directories with a `SKILL.md`, as used by Claude
Code, Codex, Cursor, and friends — in a private git repo, and use them on
any machine. A private replacement for `npx skills`.

## The model: a bag of skills

Your skills repo is a **bag**: a collection you add to over time. A local
clone of it is the editable copy. Using a skill on a machine just means
symlinking it out of the bag — **the filesystem is the database**; there is
no state to sync, no drift to reconcile:

```
bag        <clone>/skills/<name>/...        (real files, git-tracked)
hub        ~/.agents/skills/<name>     ->   <clone>/skills/<name>
spokes     ~/.claude/skills/<name>     ->   ~/.agents/skills/<name>
           ~/.codex/skills/<name>      ->   ~/.agents/skills/<name>
```

Third-party skills are **vendored**: files are copied into your repo, with
provenance (source repo, path, commit) recorded in `skillx.toml` so
`skillx update <skill>` can re-vendor them. Your own skills need no
manifest entry at all — the `skills/` directory listing is the inventory.
All git operations shell out to your `git` (your SSH agent and credential
helpers just work); nothing ever talks to any index.

**New machine:**

```sh
skillx init && skillx use --all
```

## Managing the bag

| Command | What it does |
|---|---|
| `skillx add <ref>` | Vendor skills from `owner/repo`, a GitHub URL (optionally `/tree/<branch>/<subpath>`), or a local path into the bag; record provenance for remote sources; commit (push manually or `--push`). Then installs them here — `--agent` limits, `--no-use` skips. A same-named skill from a *different* source is never overwritten silently (confirm, or `--force`). |
| `skillx rm <skill>` | Delete a skill from the bag (files + provenance, committed) and unlink it on this machine in the same breath. |
| `skillx update` | Fetch + fast-forward the bag (confirm, or `--yes`), then prune owned dangling links — skills removed on another machine disappear here automatically. The only reconciliation skillx does, and it only touches links verified as its own. |
| `skillx update <skill>...` | Re-vendor third-party skills from their recorded provenance (same clone URL and branch they were added from): show what changed since the recorded commit, confirm, update, commit. |
| `skillx list` | The bag: every skill, its source (own / origin@commit), and where it's installed on this machine (read live from disk). |

## Using skills on this machine

| Command | What it does |
|---|---|
| `skillx use` | **The flagship:** one multi-select of every bag skill, pre-checked = currently installed. Check to install, uncheck to remove, Enter applies both. Install + uninstall + overview in one screen. |
| `skillx use <skill>...` | Install those skills (all enabled agents; `--agent` to limit). |
| `skillx use --all` | Install everything. |
| `skillx use --drop <skill>` | Uninstall (repeatable; the scripting/non-TTY form). Files stay in the bag. With `--agent` only those agents' spokes go; the hub link follows once no agent's spoke remains. |
| `skillx use <skill> --local` | Copy the skill into the current project (`<project>/.agents/skills/<name>` + relative spokes, committable). Plain `use --local` re-fastens a project's existing links. |
| `skillx use ... --copy` | Escape hatch: real copies in the agent dirs instead of symlinks. Copies carry a `.skillx-copy` marker so later runs can refresh them; real directories *without* the marker are never overwritten. |
| `skillx status` (`doctor`) | Inspect this machine: bag freshness (`--offline` skips the network fetch), then the hub and every agent dir grouped, one line per entry (`dir` / `link` / `broken`), unmanaged installs flagged as adoptable, summary hints. |
| `skillx adopt` | Migrate skills installed by other tools (real dirs in the hub, copies in agent dirs) into the bag, preserving exactly where they were installed. `--all`, `--skill`, `--dry-run`, `--force`. |
| `skillx init` | Staged onboarding: (1) validate the repo URL with `git ls-remote` before touching anything, (2) clone/reuse + scaffold, (3) detect/pick agents, (4) write machine config, (5) offer adoption, (6) offer cleanup of unmanaged leftovers — defaults to **No**; non-interactive runs never delete anything unless `--clean` is passed (`--yes` does not imply cleanup), (7) pick which skills to use here. Idempotent. |

Every prompt has a flag equivalent; when stdin is not a TTY skillx never
prompts — it fails with a message naming the flag.

**Safety rule:** skillx only creates, modifies, or deletes symlinks it owns —
links whose whole chain resolves into the clone. Skills installed by other
tools (e.g. real directories in `~/.agents/skills` from `npx skills`), your
own files, and unattributable broken links are reported by `status`, never
modified. Migrate them with `skillx adopt`, or clear them with init's
opt-in cleanup stage.

## Manifest — `skillx.toml` (root of the skills repo, provenance only)

```toml
# only vendored third-party skills appear here:
[skills.pr-review]
source = "github.com/someone/repo"
path = "skills/pr-review"
commit = "abc1234..."
# recorded only when they differ from the defaults:
# branch = "dev"                            # added from /tree/dev/...
# url = "git@github.com:someone/repo.git"   # added over SSH; updates keep the transport
```

Your own skills have no entry. (A legacy `agents` field from older versions
is ignored.)

## Machine config — `~/.config/skillx/config.toml` (respects `XDG_CONFIG_HOME`)

```toml
repo = "git@github.com:you/skills.git"
clone_path = "~/.local/share/skillx/repo"   # default respects XDG_DATA_HOME
hub = "~/.agents/skills"

[agents.claude]
path = "~/.claude/skills"

[agents.codex]
path = "~/.codex/skills"
# disabled = true                            # per-machine override

# add any agent you like:
# [agents.cursor]
# path = "~/.cursor/skills"
```

`claude` and `codex` ship as defaults. In local (project) scope an agent's
skills dir is `.<name>/skills` under the project root.

## Build

```sh
go build -o skillx .
go test ./...
```

To build a binary on Linux that you can copy to macOS:

```sh
make darwin
```

This writes:

- `dist/skillx-darwin-arm64` for Apple Silicon Macs
- `dist/skillx-darwin-amd64` for Intel Macs

On the Mac:

```sh
chmod +x skillx-darwin-arm64
./skillx-darwin-arm64 --help
```

Requires Go ≥ 1.26 and `git` on `PATH`. POSIX symlink targets only (v1).
