# kcp-dashboard

Live terminal dashboard for [KCP (Knowledge Context Protocol)](https://github.com/Cantara/knowledge-context-protocol) usage statistics.

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) + [Lip Gloss](https://github.com/charmbracelet/lipgloss). Auto-refreshes every 2 seconds.

## Panels

### Overview (pinned)

- **kcp-commands**: commands guided, unique tools, tokens of context delivered, manifest coverage percentage
- **kcp-memory**: sessions indexed, projects tracked, search recall rate (N of M searches found results)
- **Projects**: list of active project directories

### Guidance Effects (scrollable)

- **Manifest coverage** -- bar chart showing what percentage of Bash calls received guidance
- **Filtered retry rate** -- same-command-within-90s retries, excluding iterative commands (ls, grep, cat, etc.)
- **Help-followup rate** -- how often `--help` was run within 5 minutes after an inject
- **Quality alerts** -- top 5 worst manifests ranked by composite score (retry + help rates), with call counts

### Session Profile (scrollable)

- **Session count** for the selected time window, with average turns and tool calls
- **Size histogram** -- 5-bucket distribution: 1-5, 6-20, 21-50, 51-100, 100+ turns

### Commands Guided (scrollable)

- Bar chart of the top 10 most-guided commands with inject counts

### Memory Searches (scrollable)

- Recent kcp-memory searches with timestamps, queries, and result counts
- Shows recall rate (sessions recalled per search)

## Install

**macOS (Apple Silicon)**
```bash
curl -fsSL https://github.com/Cantara/kcp-dashboard/releases/latest/download/kcp-dashboard-darwin-arm64 -o ~/.local/bin/kcp-dashboard
chmod +x ~/.local/bin/kcp-dashboard
```

**macOS (Intel)**
```bash
curl -fsSL https://github.com/Cantara/kcp-dashboard/releases/latest/download/kcp-dashboard-darwin-amd64 -o ~/.local/bin/kcp-dashboard
chmod +x ~/.local/bin/kcp-dashboard
```

**Linux (amd64)**
```bash
curl -fsSL https://github.com/Cantara/kcp-dashboard/releases/latest/download/kcp-dashboard-linux-amd64 -o ~/.local/bin/kcp-dashboard
chmod +x ~/.local/bin/kcp-dashboard
```

**Linux (arm64)**
```bash
curl -fsSL https://github.com/Cantara/kcp-dashboard/releases/latest/download/kcp-dashboard-linux-arm64 -o ~/.local/bin/kcp-dashboard
chmod +x ~/.local/bin/kcp-dashboard
```

**Windows**
Download [`kcp-dashboard-windows-amd64.exe`](https://github.com/Cantara/kcp-dashboard/releases/latest/download/kcp-dashboard-windows-amd64.exe) and run it from Windows Terminal or PowerShell.

## Usage

```
kcp-dashboard [--days N] [--project name]
```

| Key | Action |
|-----|--------|
| `q` | Quit |
| `d` | Cycle day range (1 -> 7 -> 30 -> 90 -> 365) |
| `r` | Force refresh |
| Up/Down | Scroll panels |

## Data sources

| Database | Written by | Contains |
|----------|-----------|----------|
| `~/.kcp/usage.db` | kcp-commands v0.20.0+ (RFC-0017) | inject events, search logs, token estimates |
| `~/.kcp/memory.db` | kcp-memory v0.20.0+ | sessions, tool_events, manifest quality data |

Both databases must exist for the dashboard to show all panels. The dashboard opens them in read-only mode.

## Requirements

- [kcp-commands](https://github.com/Cantara/kcp-commands) v0.20.0+ to populate `~/.kcp/usage.db` with inject events
- [kcp-memory](https://github.com/Cantara/kcp-memory) v0.20.0+ to populate `~/.kcp/memory.db` with session and tool-event data

## Releases

| Version | Notes |
|---------|-------|
| v0.5.0 | Removed fabricated "tokens saved vs --help" metric. Replaced with honest "tokens of context delivered". Fixed state message. |
| v0.5.1 | Patch: fixed "memory building" state message when sessions exist but no searches match. |
| v0.6.0 | Added Guidance Effects panel (manifest coverage bar, filtered retry rate, help-followup rate, quality alerts). Added Session Profile panel (session size histogram + averages). Removed empty Top Units panel. Data sourced from memory.db tool_events. |
| v0.22.0 | README rewrite — accurate panel descriptions, removed stale kcp-mcp bridge references. Version aligned with kcp-commands v0.22.0 and kcp-memory v0.22.0. |

## Part of the KCP ecosystem

| Tool | Role |
|------|------|
| [kcp-commands](https://github.com/Cantara/kcp-commands) | Claude Code hook -- injects CLI knowledge before Bash tool calls |
| [kcp-memory](https://github.com/Cantara/kcp-memory) | Episodic memory -- indexes Claude Code session history |
| [knowledge-context-protocol](https://github.com/Cantara/knowledge-context-protocol) | Spec -- the KCP specification |
| **kcp-dashboard** | Live stats -- visualises KCP guidance and memory activity |

## License

Apache 2.0 -- [Cantara](https://github.com/Cantara)
