# internal/config

Configuration management via `config.json`. Platform-appropriate paths via `os.UserConfigDir()`.

## Config Location

| Platform | Path |
|---|---|
| Linux | `~/.config/bolt/config.json` |
| macOS | `~/Library/Application Support/bolt/config.json` |
| Windows | `%APPDATA%\bolt\config.json` |

## Key Functions

- `Dir()` — config directory, creates if missing
- `DefaultPath()` — full path to config.json
- `DefaultConfig()` — all defaults populated
- `Load(path)` — reads file, merges over defaults (new fields get defaults), validates
- `Save(path)` — writes pretty-printed JSON
- `Validate()` — checks all range constraints

## Validation Rules

| Field | Range |
|---|---|
| MaxConcurrent | 1–10 |
| DefaultSegments | 1–32 |
| ServerPort | 1024–65535 |
| AuthToken | non-empty, ≥16 chars |
| MinSegmentSize | ≥ 64 KB |
| MaxRetries | 0–100 |

## Defaults

- MaxConcurrent: 3, DefaultSegments: 16, ServerPort: 9683
- MinSegmentSize: 1 MB, MaxRetries: 10
- AuthToken: 64-char hex (32 random bytes from `crypto/rand`)
- DownloadDir: `~/Downloads`
