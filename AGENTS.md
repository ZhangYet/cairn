# AGENTS.md — cairn

## Project

Single-binary Go CLI tool (`cairn`): Telegram posting, Fitbit sleep summaries, LLM writer, dictionary lookup, geocoding + TSP routing.

- **All `.go` files are `package main`** — flat structure, no internal packages.
- Entrypoint: `main.go:59` (`func main`). Version string: `main.go:12`.

## Build

```bash
make build        # go build -o cairn . (current OS/arch)
make build-linux  # linux/amd64 cross-build
make build-linux-386
make clean
```

**Go version**: `go.mod` says 1.20, but the Makefile **requires Go 1.23** and uses `gvm` to switch. If gvm is absent and Go != 1.23, the build errors.

For a quick compile without `make`: `go build -o cairn .` (if Go 1.23+ is active).

## Testing & Linting

- **No test files exist** (`*_test.go` returns zero results).
- **No lint/typecheck/formatter config** — no CI, no pre-commit hooks.

## Release

```bash
./release.sh <tag>   # e.g. ./release.sh v0.3.0
```
The script collects conventional commits since the last tag, prepends a section to `ChangeLog.md`, commits it as `chore(release): <tag>`, creates an annotated tag, and pushes both branch and tag to origin. Only warns if not on `master`.

See `.cursor/skills/add-release-tag/SKILL.md` — instructs agents to output the release command without executing it.

## Configuration (runtime)

- Config file: `~/.cairn.toml` (TOML, overridable via `-c`).
- Sections: `[telegram]`, `[fitbit]`, `[openrouter]`, `[openai]`, `[google]`.
- Fitbit OAuth2 tokens: `~/.cairn_fitbit_tokens.json` (auto-managed, callback `http://127.0.0.1:8765/callback`).
- Dictionary DB: `~/.cairn_dict.db` (SQLite, auto-created on first `-d` lookup via `dict.go`).

## Key dependencies

- `spf13/pflag` — CLI flags (POSIX/GNU-style)
- `pelletier/go-toml/v2` — config parsing
- `modernc.org/sqlite` — pure-Go SQLite for dict history
- `mattn/go-runewidth` — CJK-aware column alignment in geocode output
