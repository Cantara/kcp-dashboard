# kcp-dashboard

Live terminal dashboard for [KCP (Knowledge Context Protocol)](https://github.com/Cantara/knowledge-context-protocol) usage statistics.

Shows queries served, units fetched, tokens saved, top units, and recent queries — updated every 2 seconds from `~/.kcp/usage.db`.

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) + [Lip Gloss](https://github.com/charmbracelet/lipgloss).

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
| `d` | Cycle day range (1 → 7 → 30 → 90 → 365) |
| `r` | Force refresh |

## Requirements

- `~/.kcp/usage.db` — populated by [kcp-mcp bridge](https://github.com/Cantara/knowledge-context-protocol) v0.14.3+ (RFC-0017)
- Install [kcp-commands](https://github.com/Cantara/kcp-commands) to start collecting data

## Part of the KCP ecosystem

| Tool | Role |
|------|------|
| [kcp-commands](https://github.com/Cantara/kcp-commands) | Claude Code hook — injects CLI knowledge before Bash tool calls |
| [kcp-memory](https://github.com/Cantara/kcp-memory) | Episodic memory — indexes Claude Code session history |
| [knowledge-context-protocol](https://github.com/Cantara/knowledge-context-protocol) | Spec + bridge — serves `knowledge.yaml` manifests via MCP |
| **kcp-dashboard** | Live stats — visualises what KCP has saved you |
