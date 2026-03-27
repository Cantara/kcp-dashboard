package main

import (
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

const refreshInterval = 2 * time.Second

// Fixed UI chrome heights (lines):
//
//	header line: 1
//	blank:        1
//	overview panel border+padding+content: ~7
//	blank:        1
//	status bar:   1
//	outer border: 2
const fixedHeight = 14

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
	vp         viewport.Model
	vpReady    bool
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
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		vpHeight := msg.Height - fixedHeight
		if vpHeight < 3 {
			vpHeight = 3
		}
		if !m.vpReady {
			m.vp = viewport.New(msg.Width-6, vpHeight)
			m.vpReady = true
		} else {
			m.vp.Width = msg.Width - 6
			m.vp.Height = vpHeight
		}
		m.vp.SetContent(renderPanels(m))

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
		if m.vpReady {
			m.vp.SetContent(renderPanels(m))
		}

	case tickMsg:
		cmds = append(cmds, tea.Batch(
			fetchCmd(m.dbPath, m.days, m.project),
			tickCmd(),
		))
	}

	// Forward keypresses to viewport (↑↓ PgUp PgDn scroll)
	m.vp, cmd = m.vp.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}
