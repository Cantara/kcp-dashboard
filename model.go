package main

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const refreshInterval = 2 * time.Second

var dayOptions = []int{1, 7, 30, 90, 365}

type tickMsg time.Time

type model struct {
	dbPath     string
	stats      Stats
	lastUpdate time.Time
	days       int
	dayIndex   int
	project    string
	width      int
	height     int
	loading    bool
}

func newModel(dbPath string, days int, project string) model {
	idx := 2 // default 30d
	for i, d := range dayOptions {
		if d == days {
			idx = i
			break
		}
	}
	return model{
		dbPath:   dbPath,
		days:     days,
		dayIndex: idx,
		project:  project,
		loading:  true,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		fetchCmd(m.dbPath, m.days, m.project),
		tickCmd(),
	)
}

func fetchCmd(dbPath string, days int, project string) tea.Cmd {
	return func() tea.Msg {
		return loadStats(dbPath, days, project)
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "Q", "ctrl+c":
			return m, tea.Quit
		case "d", "D":
			m.dayIndex = (m.dayIndex + 1) % len(dayOptions)
			m.days = dayOptions[m.dayIndex]
			m.loading = true
			return m, fetchCmd(m.dbPath, m.days, m.project)
		case "r", "R":
			m.loading = true
			return m, fetchCmd(m.dbPath, m.days, m.project)
		}

	case Stats:
		m.stats = msg
		m.lastUpdate = time.Now()
		m.loading = false

	case tickMsg:
		return m, tea.Batch(
			fetchCmd(m.dbPath, m.days, m.project),
			tickCmd(),
		)
	}

	return m, nil
}
