package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

const version = "0.3.0"

func main() {
	days    := flag.Int("days", 30, "Days to include (1, 7, 30, 90, 365)")
	project := flag.String("project", "", "Filter by project name")
	flag.Parse()

	dbPath := filepath.Join(os.Getenv("HOME"), ".kcp", "usage.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "No usage data found at", dbPath)
		fmt.Fprintln(os.Stderr, "Use kcp-mcp v0.14.3+ to start collecting statistics.")
		os.Exit(1)
	}

	m := newModel(dbPath, *days, *project)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
