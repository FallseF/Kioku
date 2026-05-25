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

| Flag    | Effect                                                            |
|---------|-------------------------------------------------------------------|
| `--all` | Include background sessions (e.g. claude-mem observer sessions). By default these are hidden because they tend to outnumber real sessions ~10:1. |

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

- A display title: the user's first real message, falling back to
  `aiTitle` (Claude's auto-generated summary). Leading slash commands are
  stripped (e.g. `/skill foo bar` → `foo bar`); `claude-mem`'s
  `Hello memory agent~` greeting is skipped.
- `cwd` from any entry that recorded it (or decoded from the directory name
  as a fallback).
- File mtime as last-activity timestamp.
- Total entry count as a rough message count.

No network I/O in the default build. No daemon. No external API calls. The
binary just reads files and `exec`s `claude` directly using `syscall.Exec`,
so there is no extra terminal window or AppleScript permission required.

The `sync` build adds outbound `git push` only when you explicitly press `gg`.

## Security notes

- The default build does not touch the network or any external service.
- The `sync` build's `gg` will push session contents to a Git remote you
  configure. Use a **private** repository; sessions often contain
  proprietary code, API keys mentioned in conversation, and so on.
- Kioku validates the session ID as a UUID before running `claude --resume`,
  and uses `syscall.Exec` (no shell), so injection via filenames is not
  possible.

## License

MIT
