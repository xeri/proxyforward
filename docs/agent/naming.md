# Naming & git hygiene — name the change, never the plan

Plans are session-scoped; names are permanent. A branch, PR, commit, or spec file
named after a plan's internal structure ("phase 9", "pillar 8", "batch A") means
nothing the moment that session ends. Cleaning them out of this repo on 2026-07-17
cost a 56-commit history rewrite, two branch renames, and edits to three merged
PRs — names that describe **what the change is** never need that.

Enforced by the `naming` job in `.github/workflows/ci.yml`: every PR's head branch
name, title, commit messages, and changed file paths are rejected if they carry
plan jargon or attribution trailers.

## The durability test

*Would this name make sense to someone who never saw the plan that produced it?*
If decoding a name requires the plan document, the name is wrong.

## Rules

- **Branches** are `type/feature-slug` — `feat/`, `fix/`, `security/`, `docs/`,
  `ci/`, `deps/`. The slug names the capability or fix (`feat/agent-management-gui`,
  `security/agent-identity-hardening`), never a plan label, counter, or date.
- **PR titles** read like a commit subject: what the change is. **PR bodies**
  describe what changed and how it was verified — plan narrative stays in the plan.
  Cross-reference other work by PR number (#13) or branch name, never by plan label.
- **Commit messages, body included.** "Lands in Phase 3" rots the moment the plan
  dies; point at the artifact instead ("lands with the multi-agent GUI"). Avoid bare
  commit SHAs in messages and PR bodies where a PR number or filename works — SHAs
  don't survive history rewrites.
- **Spec files** under docs/superpowers/specs/ are named
  YYYY-MM-DD-topic-design.md — the topic, not the plan's name for the work.
- **No attribution trailers.** No Claude session URLs, no Co-Authored-By: Claude.
  A commit message carries what changed and why — nothing else.
  `.claude/settings.json` sets attribution empty at the source; the CI gate
  catches strays.
- **Merge commits take the PR title** — a repo setting (merge_commit_title:
  PR_TITLE), so never hand-write "Merge pull request #N from …" subjects. Head
  branches auto-delete on merge; don't recreate long-lived ones.

## Banned in names and messages (the CI regex, case-insensitive)

phase/pillar/sprint/milestone/week followed by a number, "batch" followed by a
letter or number, `Claude-Session:` and `Co-Authored-By: …Claude…` trailer lines.

## Before → after (from the 2026-07-17 cleanup)

```
feat/identity-phase9-finalize          → feat/identity-enrollment-finalize
feat/pillar8-agent-mgmt                → feat/agent-management-gui
identity phase 9: … finalize + docs    → identity: enrollment, pairing, and revocation — finalize + docs
docs: Pillar 8 … Batch A design spec   → docs: agent-management GUI surfacing design spec
"until Phase 3's Status.Agents"        → "until the multi-agent GUI's Status.Agents lands"
2026-07-16-pillar8-agent-management-gui-design.md
                                       → 2026-07-16-agent-management-gui-surfacing-design.md
```
