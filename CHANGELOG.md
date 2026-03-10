# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [0.2.1] - 2025-02-04

### Added

- **Dictionary** — Save looked-up words in a local SQLite DB (`~/.cairn_dict.db`). Highlight words from the DB in dictionary output (current search in green, last 3 searched in cyan); word variations (tense, plural) supported.
- **Fitbit** — Optional `scp_host` (and `scp_path`) in config: after refreshing the token on `-m`, scp the token file to the remote server. Debug messages for token refresh and scp.
- **Makefile** — Go version check: require go1.23; if not set, try `gvm use go1.23` in the same shell as the build so the build uses the correct Go.

---

## [0.2.0] - 2025-02-04

### Added

- **Dictionary lookup** (`-d` / `--dict WORD`) — Look up word meanings using the [Free Dictionary API](https://dictionaryapi.dev/) (no API key). Shows definitions, phonetics, and part-of-speech.
- **Etymology** — Word origins from [Etymonline](https://www.etymonline.com/) (primary) with [Wiktionary](https://en.wiktionary.org/) as fallback. Prefers the longer result; shows “Full entry” link when Etymonline snippet is truncated. Ignores Etymonline when it redirects to a different word (e.g. `advertise` → `advert`) so the correct word’s etymology is shown.
- **Example sentences** — Example usage from Wiktionary when available (`{{ux|en|...}}`, passage, or first `#:` line).
- **“Did you mean?”** — For unknown words, suggests the closest match via Levenshtein distance using [dwyl/english-words](https://github.com/dwyl/english-words); prefers same-length word on tie.
- **Makefile** — Targets for `build`, `build-linux`, and `clean`.
- **LICENSE** — Project licensed under GPL-3.0.

### Changed

- **Codebase layout** — Logic split from `main.go` into `config.go`, `dict.go`, `fitbit.go`, `telegram.go`, and `writer.go` for clearer structure and maintainability.
- **README** — Updated features, requirements, installation, and usage (including dictionary and writer).

### Fixed

- Etymology no longer shown for the wrong headword when Etymonline redirects (e.g. lookup “advertise” no longer displays “advert”’s etymology).

---

## [0.1.3] - (4e207d6d)

Baseline: Telegram posting, Fitbit morning summary, OpenRouter/OpenAI writer, photo posting and message updates. Single-file `main.go` with embedded config and helpers.

[0.2.1]: https://github.com/ZhangYet/cairn/compare/0.2.0...0.2.1
[0.2.0]: https://github.com/ZhangYet/cairn/compare/0.1.3...0.2.0
[0.1.3]: https://github.com/ZhangYet/cairn/releases/tag/0.1.3
