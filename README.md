# cairn

A command-line tool for posting to Telegram channels, syncing Fitbit sleep data, and generating text with LLM APIs (OpenAI or OpenRouter).

**Version:** 0.1.3

## Features

1. **Telegram channel posting** — Post text or photos to a channel. Edit or replace messages by ID. All posts get a `#cairn` tag.
2. **Fitbit morning summary** — Fetch today’s sleep data from Fitbit and post a formatted summary (and optional extra text) to your channel. Uses OAuth2 with PKCE; tokens are stored locally.
3. **LLM writer** — Read a prompt from a file, call OpenAI or OpenRouter (streaming), and optionally save the result to a file. Configure one provider in config; OpenAI is used if both are set.
4. **Dictionary** — Look up word meanings via the [Free Dictionary API](https://dictionaryapi.dev/) (no API key). Use `-d`/`--dict` with a word.

## Requirements

- Go 1.18+
- A Telegram bot and channel (with the bot added as admin)
- For Fitbit: a [Fitbit app](https://dev.fitbit.com/apps) with OAuth2 callback `http://127.0.0.1:8765/callback`
- For writer: an [OpenAI](https://platform.openai.com/) and/or [OpenRouter](https://openrouter.ai/) API key

## Installation

```bash
# Build for current OS/arch
make build
# or
go build -o cairn .

# Build for Linux x86_64
make build-linux
```

Binary is produced in the current directory (`cairn` or `cairn-linux-amd64`).

## Configuration

Create `~/.cairn.toml` (or pass `-c path/to/config.toml`):

```toml
[telegram]
bot_token = "YOUR_BOT_TOKEN"
channel_id = "@your_channel"

# Optional: for -m/--morning (Fitbit sleep → channel)
[fitbit]
client_id = "YOUR_FITBIT_CLIENT_ID"
client_secret = "YOUR_FITBIT_CLIENT_SECRET"

# Optional: for -W/--writer (LLM). Use one or both; OpenAI takes precedence if both set.
[openrouter]
api_key = "YOUR_OPENROUTER_API_KEY"
model = "google/gemma-2-9b-it:free"

[openai]
api_key = "YOUR_OPENAI_API_KEY"
model = "gpt-4o-mini"
```

- **Telegram** is required for all posting and editing.
- **Fitbit** is required only for `--morning`.
- **OpenRouter** or **OpenAI** (at least one with both `api_key` and `model`) is required for `--writer`.

## Usage

### Post to channel

```bash
# Text (inline or from file)
cairn -p "Hello world #tag1"
cairn -f message.txt

# One or more photos (caption from -p or -f)
cairn -P image.jpg -p "Caption"
cairn -P a.jpg b.jpg -p "Caption"
cairn -P a.jpg,b.jpg -f caption.txt
```

After a post, the CLI prints the `message_id` so you can edit later.

### Update a message

```bash
# Change text or caption
cairn -u 123 -p "Corrected text"
cairn -u 456 -p "New caption"   # photo message: edits caption

# Replace the photo and set new caption
cairn -u 456 -P new.jpg -p "New caption"
```

### Morning (Fitbit sleep → channel)

```bash
# Post today’s sleep summary only
cairn --morning

# With extra line of text
cairn --morning -p "Feeling good today"
cairn --morning -f notes.txt
```

First run will open the browser for Fitbit authorization; tokens are saved under `~/.cairn_fitbit_tokens.json`.

### Writer (LLM)

```bash
# Prompt from file; result only printed to stderr (no file, no Telegram)
cairn -W prompt.txt

# Save result to file
cairn -W prompt.txt -o result.txt
```

Prompt file content is sent as the user message; the model reply is streamed. Configure either `[openai]` or `[openrouter]` (or both; OpenAI wins) in config.

### Dictionary

```bash
# Look up a word (uses Free Dictionary API; no config needed)
cairn -d hello
cairn --dict serendipity
```

Prints definitions, phonetics, part of speech, examples, and synonyms/antonyms when available.

## Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--config` | `-c` | Config file (default: `~/.cairn.toml`) |
| `--post` | `-p` | Text to post or use as caption |
| `--file` | `-f` | Read content from file |
| `--photo` | `-P` | Photo path(s), comma or space separated |
| `--morning` | `-m` | Fitbit sleep → channel |
| `--writer` | `-W` | Prompt file for LLM (OpenAI/OpenRouter) |
| `--output` | `-o` | Output file for writer result (use with `-W`) |
| `--dict` | `-d` | Look up word meaning (Free Dictionary API) |
| `--update` | `-u` | Message ID to edit (text/caption or replace photo with `-P`) |
| `--help` | `-h` | Show help |

## License

This project is licensed under the GNU General Public License v3.0 (GPL-3.0).  
See [LICENSE](LICENSE) for the full text.
