# Kioku (記憶)

A TUI session browser for [Claude Code](https://docs.claude.com/en/docs/claude-code).
Browse, search, and resume any past conversation in one keystroke.

*Kioku* means "memory" in Japanese — your Claude Code memory, navigable.

## Why

Claude Code stores every session as a `.jsonl` file under
`~/.claude/projects/<encoded-cwd>/<uuid>.jsonl`. After a while you end up with
hundreds or thousands of them, and the built-in `claude --resume` picker only
shows recent sessions in the current directory. Kioku gives you a fast TUI
across **all** sessions in **all** project directories, with fuzzy search and
one-keystroke resume.

## Install

```bash
go install github.com/FallseF/Kioku@latest
```

This installs the binary as `Kioku`. Symlink it as `kioku` if you prefer
lowercase:

```bash
ln -sf "$(go env GOPATH)/bin/Kioku" ~/bin/kioku
```

### Build from source

```bash
git clone https://github.com/FallseF/Kioku
cd Kioku
go build -o kioku .
mv kioku ~/bin/   # or anywhere on PATH
```

## Usage

```bash
kioku
```

| Key             | Action                                    |
|-----------------|-------------------------------------------|
| `j` / `k`       | Move down / up                            |
| `/`             | Filter sessions (title, message, cwd)     |
| `Enter`         | Resume selected session                   |
| `cc`            | Copy `cd '<cwd>' && claude --resume <id>` to clipboard |
| `q` / `Ctrl+C`  | Quit                                      |
| `?`             | Toggle full help                          |

`Enter` replaces the current process with `claude --resume <id>` after
`cd`-ing into the session's original `cwd`. When you exit Claude, you land
back at your shell prompt.

### Flags

| Flag            | Effect                                                    |
|-----------------|-----------------------------------------------------------|
| `--all`         | Include background sessions — claude-mem observer sessions, `agent-*` subagent transcripts, and autocomplete/suggestion sessions. Hidden by default because they outnumber real sessions ~10:1 and aren't meaningfully resumable. |
| `--warm-titles` | Pre-generate Japanese headlines (via local ollama) for every session that lacks a good one, write them to the title cache, and exit without opening the TUI. Useful for warming the cache in one shot instead of letting the TUI fill it in the background. |

## Session titles

Kioku resolves each row's headline in priority order:

1. **Your own first message**, when it's short enough to read at a glance.
2. A **locally generated Japanese title**, for sessions whose first message is
   long *and* have no aiTitle (see below).
3. Claude Code's own **`aiTitle`** (its auto-generated summary).
4. The long first message, truncated.
5. `(無題)` for empty/throwaway sessions.

The first message is cleaned of harness noise first: `<command-*>` wrappers
(content commands like `/rinko foo` are kept as `/rinko foo`; control commands
like `/clear`, `/effort` are skipped), `<local-command-caveat>` blocks,
`<system-reminder>`, compaction prompts, and `claude-mem`'s "Hello memory
agent" wrapper — from which the real request inside `<user_request>` is
recovered rather than discarded.

### Local title generation (optional, via ollama)

For step 2, Kioku asks a **local [ollama](https://ollama.com)** model for a
short Japanese title. This is the only network activity Kioku performs, it is
**localhost-only** (nothing leaves your machine), and it's entirely optional —
if ollama isn't running, Kioku just uses the other headline sources.

- Generation runs in the background while you browse and the results are
  cached to disk, so each session is titled at most once. Run
  `kioku --warm-titles` to fill the cache up front instead.
- Disable it completely with `KIOKU_TITLES=off`.

| Env var              | Default                                                    | Purpose |
|----------------------|------------------------------------------------------------|---------|
| `KIOKU_OLLAMA_MODEL` | `qwen3:8b`                                                 | Model used for titles. |
| `KIOKU_OLLAMA_URL`   | `http://localhost:11434`                                   | ollama endpoint. |
| `KIOKU_TITLE_CACHE`  | `~/Library/Application Support/kioku/titles.json`          | Generated-title cache (invalidated when a session file changes). |
| `KIOKU_TITLES`       | (unset)                                                    | Set to `off` to skip all title generation. |

## Optional: cross-machine sync (`gg`)

Kioku ships a separate `kioku-sync` build that adds a `gg` chord to push the
selected session — conversation, memory snapshots, project `CLAUDE.md` —
to a private GitHub repo you own. Another machine with `kioku-sync` (and
the same repo cloned) can then pull and resume the conversation locally.

### Setup

1. Build with the `sync` tag:
   ```bash
   git clone https://github.com/FallseF/Kioku
   cd Kioku
   go build -tags sync -o kioku-sync .
   mv kioku-sync ~/bin/
   ```
2. Create an empty private repo on GitHub (e.g. `yourname/kioku-sync`).
3. Clone it locally to the path Kioku looks at:
   ```bash
   mkdir -p "$HOME/Library/Application Support/kioku"
   git clone https://github.com/<you>/kioku-sync.git \
     "$HOME/Library/Application Support/kioku/sync"
   ```
   To put the clone elsewhere, set `KIOKU_SYNC_DIR=/your/path` instead.
4. Run `kioku-sync` and press `g` twice on a session. The first `g` previews
   what will be pushed; the second triggers `git commit && git push`.

### Bundle layout

Each pushed session lives under `sessions/<session-id>/`:

```
sessions/<session-id>/
├── meta.json       hostname, user, cwd, exported_at — handoff source info
├── session.jsonl   raw Claude Code conversation log
├── memory/         snapshot of your auto-memory directory (if any)
└── CLAUDE.md       project CLAUDE.md from the original cwd (if any)
```

`meta.json` makes it trivial to see which machine a session came from when
you resume it elsewhere.

## How it works

For each `.jsonl`, Kioku extracts:

- A display headline (see [Session titles](#session-titles) for the full
  resolution order and cleaning rules).
- `cwd` from any entry that recorded it (or decoded from the directory name
  as a fallback).
- File mtime as last-activity timestamp.
- Total entry count as a rough message count.

No daemon, no external API calls, no extra terminal window or AppleScript
permission. The binary reads files and `exec`s `claude` directly via
`syscall.Exec`. The **only** network activity is the optional, localhost-only
ollama call used to generate Japanese titles (disable with `KIOKU_TITLES=off`);
nothing is sent off the machine.

The `sync` build adds outbound `git push` only when you explicitly press `gg`.

## Security notes

- The default build's only network activity is the optional title generation,
  which talks to a **local** ollama on `localhost` and sends nothing off the
  machine. Set `KIOKU_TITLES=off` to disable it. No other external services.
- The `sync` build's `gg` will push session contents to a Git remote you
  configure. Use a **private** repository; sessions often contain
  proprietary code, API keys mentioned in conversation, and so on.
- Kioku validates the session ID as a UUID before running `claude --resume`,
  and uses `syscall.Exec` (no shell), so injection via filenames is not
  possible.

## License

MIT
