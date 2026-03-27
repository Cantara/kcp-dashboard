package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	colPurple = lipgloss.Color("#7C3AED")
	colTeal   = lipgloss.Color("#14B8A6")
	colGreen  = lipgloss.Color("#22C55E")
	colDim    = lipgloss.Color("#6B7280")
	colWhite  = lipgloss.Color("#F9FAFB")
	colBar    = lipgloss.Color("#8B5CF6")

	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(colWhite)
	styleValue = lipgloss.NewStyle().Bold(true).Foreground(colTeal)
	styleSaved = lipgloss.NewStyle().Bold(true).Foreground(colGreen)
	styleLabel = lipgloss.NewStyle().Foreground(colDim)
	styleDim   = lipgloss.NewStyle().Foreground(colDim)
	styleBar   = lipgloss.NewStyle().Foreground(colBar)

	stylePanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colPurple).
			Padding(0, 1)

	styleOuter = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#4C1D95")).
			Padding(0, 1)
)

// renderPanels builds the scrollable section (everything below overview).
// Called both from View() and whenever stats update.
func renderPanels(m model) string {
	innerW := m.width - 6
	s := m.stats
	var sections []string

	// ── Top Commands ──────────────────────────────────────────────────────────

	if len(s.TopCommands) > 0 {
		var maxCount int64
		for _, u := range s.TopCommands {
			if u.Count > maxCount {
				maxCount = u.Count
			}
		}
		const barCols = 22
		lines := make([]string, 0, len(s.TopCommands))
		for _, u := range s.TopCommands {
			filled := int(float64(barCols) * float64(u.Count) / float64(maxCount))
			bar := styleBar.Render(strings.Repeat("█", filled)) +
				styleDim.Render(strings.Repeat("░", barCols-filled))
			name := truncate(u.UnitID, 26)
			lines = append(lines, fmt.Sprintf("  %-26s  %s  %s",
				name, bar,
				styleValue.Render(fmt.Sprintf("%d", u.Count)),
			))
		}
		sections = append(sections,
			stylePanel.Width(innerW).Render(" Top Commands (injected)\n\n"+strings.Join(lines, "\n")),
			"",
		)
	}

	// ── Top Units ─────────────────────────────────────────────────────────────

	if len(s.TopUnits) > 0 {
		var maxCount int64
		for _, u := range s.TopUnits {
			if u.Count > maxCount {
				maxCount = u.Count
			}
		}
		const barCols = 22
		lines := make([]string, 0, len(s.TopUnits))
		for _, u := range s.TopUnits {
			filled := int(float64(barCols) * float64(u.Count) / float64(maxCount))
			bar := styleBar.Render(strings.Repeat("█", filled)) +
				styleDim.Render(strings.Repeat("░", barCols-filled))
			name := truncate(u.UnitID, 26)
			saved := ""
			if u.TokensSaved > 0 {
				saved = styleDim.Render("  " + fmtNum(u.TokensSaved) + " saved")
			}
			lines = append(lines, fmt.Sprintf("  %-26s  %s  %s%s",
				name, bar,
				styleValue.Render(fmt.Sprintf("%d", u.Count)),
				saved,
			))
		}
		sections = append(sections,
			stylePanel.Width(innerW).Render(" Top Units\n\n"+strings.Join(lines, "\n")),
			"",
		)
	}

	// ── Top Queries ───────────────────────────────────────────────────────────

	if len(s.TopQueries) > 0 {
		lines := make([]string, 0, len(s.TopQueries))
		for _, q := range s.TopQueries {
			lines = append(lines, fmt.Sprintf("  %s  %q",
				styleDim.Render(fmt.Sprintf("%4d×", q.Count)),
				truncate(q.Query, 52),
			))
		}
		sections = append(sections,
			stylePanel.Width(innerW).Render(" Top Queries\n\n"+strings.Join(lines, "\n")),
			"",
		)
	}

	return strings.Join(sections, "\n")
}

func (m model) View() string {
	if m.width == 0 || !m.vpReady {
		return "initialising…"
	}

	innerW := m.width - 6 // outer border (2) + outer padding (2) + panel border (2)

	// ── Header (pinned) ───────────────────────────────────────────────────────

	spin := ""
	if m.loading {
		spin = " " + styleDim.Render("↻")
	}
	header := styleTitle.Render("KCP Dashboard") +
		"  " + styleDim.Render("·") + "  " +
		styleDim.Render(fmt.Sprintf("last %d day%s", m.days, plural(m.days))) +
		spin

	// ── Overview (pinned) ─────────────────────────────────────────────────────

	s := m.stats
	ov := []string{
		fmt.Sprintf("  %s   %s     %s   %s     %s   %s",
			styleLabel.Render("Queries served"),
			styleValue.Render(fmtNum(s.TotalSearches)),
			styleLabel.Render("Units fetched"),
			styleValue.Render(fmtNum(s.TotalGets)),
			styleLabel.Render("Commands assisted"),
			styleValue.Render(fmtNum(s.TotalInjects)),
		),
	}
	if s.TokensSaved > 0 {
		ov = append(ov, fmt.Sprintf("  %s   %s",
			styleLabel.Render("Tokens saved  "),
			styleSaved.Render("▲ "+fmtNum(s.TokensSaved)),
		))
	} else {
		ov = append(ov, fmt.Sprintf("  %s   %s",
			styleLabel.Render("Tokens saved  "),
			styleDim.Render("n/a — add hints.token_estimate to knowledge.yaml"),
		))
	}
	if len(s.Projects) > 0 {
		ov = append(ov, fmt.Sprintf("  %s   %s",
			styleLabel.Render("Projects      "),
			styleDim.Render(strings.Join(s.Projects, ", ")),
		))
	}
	overview := stylePanel.Width(innerW).Render(" Overview\n\n" + strings.Join(ov, "\n"))

	// ── Status bar (pinned) ───────────────────────────────────────────────────

	ago := "—"
	if !m.lastUpdate.IsZero() {
		secs := int(time.Since(m.lastUpdate).Seconds())
		if secs < 2 {
			ago = "just now"
		} else {
			ago = fmt.Sprintf("%ds ago", secs)
		}
	}
	scrollPct := ""
	if m.vp.TotalLineCount() > m.vp.VisibleLineCount() {
		scrollPct = fmt.Sprintf(" · %.0f%%", m.vp.ScrollPercent()*100)
	}
	status := styleDim.Render(fmt.Sprintf(
		"q quit · d cycle days · r refresh · ↑↓ scroll%s · updated %s", scrollPct, ago,
	))

	// ── Compose: pinned top + viewport + pinned status ────────────────────────

	body := strings.Join([]string{
		header,
		"",
		overview,
		"",
		m.vp.View(),
		status,
	}, "\n")

	return styleOuter.Width(m.width - 2).Render(body)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func fmtNum(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
