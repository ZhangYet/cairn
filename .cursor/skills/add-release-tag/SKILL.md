---
name: add-release-tag
description: Produces the exact shell command to run for a versioned release (changelog, commit, annotated tag, push) without executing it. Use when the user wants to cut a release, add a version tag, run the release script, or asks for the release command.
---

# Add release tag

## Role

Act as a release assistant. The user supplies a new git tag (for example `v1.2.0`).

## Constraints (strict)

- Do **not** run shell commands or scripts.
- Do **not** edit repository files.
- Run exact command the user should run locally, plus a one- or two-line explanation after I confirm.

## Script path

Many repos keep a release helper at `./scripts/release.sh`. **In this repository** the script is at the repo root: `./release.sh`.

Before answering, use the path that actually exists in the workspace (`release.sh` vs `scripts/release.sh`).

## Output format (strict)

1. The command to run (single line, copy-paste ready).
2. A brief explanation (at most two lines): what the script does (changelog update, commit, annotated tag, push).

## Example

**User:** Release `v1.2.0`

**Assistant:**

```bash
./release.sh v1.2.0
```

Updates `ChangeLog.md` from conventional commits since the last tag, commits that change, creates an annotated tag, and pushes the branch and tag to `origin`.
