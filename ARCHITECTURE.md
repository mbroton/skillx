# Architecture

How skillx works and why it is built the way it is.

## The mental model: three layers, one source of truth

Everything in skillx derives from one picture:

```
BAG      <clone>/skills/<name>/     real files, git-tracked (the only truth)
HUB      ~/.agents/skills/<name>    symlink -> bag
SPOKES   ~/.claude/skills/<name>    symlink -> hub
         ~/.codex/skills/<name>     symlink -> hub
```

- **The bag** is your skills git repo, cloned locally. Files exist *only*
  there.
- **The hub** is one neutral directory of symlinks into the bag. Agents
  never point at the bag directly; they point at the hub. That indirection
  means adding a new agent never touches the bag, and the hub doubles as
  the machine-wide "what's installed" ledger.
- **Spokes** are per-agent symlinks into the hub.

Two axioms follow, and the whole tool is built on them:

1. **The filesystem is the database.** "Installed" *means* "the link
   exists." There is no state file that could drift from reality —
   `list`, `status`, and the interactive `use` answer questions by
   reading links off disk, never from a record.
2. **Git is the sync.** Machines never talk to each other and skillx
   never talks to a registry. You push the bag; other machines pull it.
   The only "reconciliation" that exists is mechanical: after a pull,
   links pointing at skills that no longer exist are dangling, and
   `skillx update` prunes them.

The manifest (`skillx.toml`) deliberately does *not* break axiom 1: it
records **provenance only** — where a vendored skill came from (source,
path, branch, commit, URL) so `skillx update <skill>` can re-fetch it. It
is not an inventory (the `skills/` directory listing is), and it says
nothing about machines or agents (the symlinks do).

## The safety invariant

One rule makes a tool that deletes symlinks in shared directories safe:

> skillx may only create, modify, or delete a symlink whose chain
> **resolves into the clone's skills directory** — the only directory
> skillx exclusively owns.

This is deliberately stricter than "points into the hub", because the hub
is shared real estate: other tools (e.g. `npx skills`) put *real
directories* there, and a spoke pointing at one of those is foreign even
though it points at the hub. Broken links get best-effort attribution
(chain heads toward the clone, or the name matches a bag skill →
"owned-dangling", removable only when pruning; otherwise "unknown" →
never removed). Everything the rule forbids touching is *reported*
instead — that is what `status` and `adopt` exist for.

## Package map

Dependencies point strictly downward; nothing below `cmd` knows about
cobra, config files, or the other packages' domains:

```
main.go            calls cmd.Execute()
internal/cmd/      cobra wiring + orchestration (the only impure layer)
  ├─ config/       ~/.config/skillx/config.toml: repo URL, clone path, hub, agents
  ├─ manifest/     skillx.toml: provenance for vendored skills
  ├─ linker/       THE CORE: pure symlink engine (plan/apply + ownership)
  ├─ gitx/         thin shell-out to the user's `git` (creds just work)
  ├─ refparse/     "owner/repo", URLs, ssh, local paths -> parsed Ref
  ├─ skillfind/    what a skill is (dir with SKILL.md) and discovery rules
  ├─ adopt/        detect skills installed by other tools (read-only)
  ├─ fsutil/       CopyDir/ReplaceDir/MoveDir with overlap guards; ~ expansion
  └─ ui/           interactive prompts behind TTY checks
```

The linker is the piece worth understanding; everything else is glue.

## The linker: declarative plan → verified apply

The linker works like a tiny terraform for symlinks:

1. **Spec** (`linker/spec.go`) — `GlobalSpec` / `LocalSpec` expand "these
   skills, these agents" into a flat list of `Desired{Path, Target}`
   links. Pure computation.
2. **Compute** (`Compute`, `RemovalsFor`, `PruneDangling`) — reads disk,
   diffs desired against actual, classifies every existing link through
   the ownership rule, and emits a `Plan`: `Actions` (create / fix /
   remove — only ever on owned links) and `Issues` (conflicts, foreign
   links, dangling links — reported, not acted on). Read-only.
3. **Apply** — executes the actions, but before every destructive step
   re-verifies the link is *exactly the link the plan decided about*
   (still a symlink, same target). A link swapped between plan and apply
   is refused. Apply deliberately does not re-run full classification:
   applying one action (removing a hub link) legitimately breaks the
   chains of later actions in the same plan.

Ownership has two modes because the two scopes have different canonical
directories. **Global** scope requires full chain resolution into the
clone — the hub is shared, so merely pointing at it proves nothing.
**Local** (project) scope is pointer-based — `<project>/.agents/skills`
contains real files *by design* and is itself canonical, so a link
pointing into it is legitimately managed there.

## Commands are compositions

Every command is the same sandwich: load config → read inventory /
manifest → build a spec → plan → print → apply → maybe git commit.

- `add` = refparse → shallow clone to temp → skillfind → copy into the
  bag → record provenance → commit → install links.
- `rm` = unlink first (while the links still resolve) → `git rm` →
  commit.
- `update` = fetch / fast-forward → prune dangling links → point out
  bag skills not installed.
- `init` = seven idempotent stages, ordered so nothing touches disk
  before the repo URL is validated, and nothing is ever deleted without
  explicit opt-in.

## Design decisions

- **Shell out to `git`, never embed it.** SSH agents, credential helpers,
  and private repos work for free; skillx holds zero credentials.
- **Vendor, don't reference.** Third-party skills are *copied* into your
  repo. Your setup survives upstream deletions; updates are explicit,
  diffed, and confirmed.
- **Escape hatches degrade gracefully.** `--copy` (real files instead of
  spokes, tagged with a `.skillx-copy` marker so only skillx's own copies
  are ever refreshed) and `--local` (committable per-project copies)
  exist for setups that can't follow symlinks, without complicating the
  core model.
- **Prompt discipline.** Every prompt has a flag equivalent; without a
  TTY, skillx fails and names the flag rather than hanging. Destructive
  defaults are always "No".
- **Destructive file operations are centralized.** `fsutil.ReplaceDir`
  stages into a temp sibling, renames into place, and refuses overlapping
  src/dst — no command can half-destroy a skill.

**The one-sentence version:** a pure planner that diffs a desired symlink
layout against disk under a strict "only touch what resolves into your
own clone" rule, wrapped in commands that treat a git repo as the single
source of truth — everything else (manifest, config, prompts, git) is
bookkeeping around those two ideas.
