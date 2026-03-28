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
	colAmber  = lipgloss.Color("#F59E0B")

	styleTitle  = lipgloss.NewStyle().Bold(true).Foreground(colWhite)
	styleValue  = lipgloss.NewStyle().Bold(true).Foreground(colTeal)
	styleSaved  = lipgloss.NewStyle().Bold(true).Foreground(colGreen)
	styleLabel  = lipgloss.NewStyle().Foreground(colDim)
	styleDim    = lipgloss.NewStyle().Foreground(colDim)
	styleBar    = lipgloss.NewStyle().Foreground(colBar)
	styleWarn   = lipgloss.NewStyle().Foreground(colAmber)

	stylePanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colPurple).
			Padding(0, 1)

	styleOuter = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#4C1D95")).
			Padding(0, 1)
)

// renderBar renders a progress bar of the given width with filled/empty blocks.
func renderBar(rate float64, width int) string {
	filled := int(rate * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return styleBar.Render(strings.Repeat("█", filled)) +
		styleDim.Render(strings.Repeat("░", width-filled))
}

// renderPanels builds the scrollable section below the overview.
func renderPanels(m model) string {
	innerW := m.width - 6
	s := m.stats
	var sections []string

	// ── Guidance Effects ───────────────────────────────────────────────────

	if s.TotalBashCalls > 0 {
		const barW = 20
		lines := []string{
			fmt.Sprintf("  %-22s %s  %s",
				styleLabel.Render("Manifest coverage"),
				renderBar(s.ManifestHitRate, barW),
				styleValue.Render(fmt.Sprintf("%d%% of Bash calls are guided", int(s.ManifestHitRate*100))),
			),
			fmt.Sprintf("  %-22s %s  %s",
				styleLabel.Render("Retry rate"),
				renderBar(s.FilteredRetryRate, barW),
				styleValue.Render(fmt.Sprintf("%d%% (action cmds, excl. iter.)", int(s.FilteredRetryRate*100))),
			),
			fmt.Sprintf("  %-22s %s  %s",
				styleLabel.Render("Help followup"),
				renderBar(s.HelpFollowupRate, barW),
				styleValue.Render(fmt.Sprintf("%.1f%% after inject", s.HelpFollowupRate*100)),
			),
		}

		if len(s.QualityAlerts) > 0 {
			lines = append(lines, "")
			lines = append(lines, "  "+styleLabel.Render("Quality alerts:"))
			for _, a := range s.QualityAlerts {
				sym := styleSaved.Render("  *")
				if a.Score > 0.2 {
					sym = styleWarn.Render("  !")
				}
				lines = append(lines, fmt.Sprintf("  %s %-12s %s  retry=%d%%  help=%d%%",
					sym,
					truncate(a.ManifestKey, 12),
					styleDim.Render(fmt.Sprintf("%d calls", a.TotalCalls)),
					int(a.RetryRate*100),
					int(a.HelpRate*100),
				))
			}
		}

		sections = append(sections,
			stylePanel.Width(innerW).Render(" Guidance Effects\n\n"+strings.Join(lines, "\n")),
			"",
		)
	}

	// ── Session Profile ───────────────────────────────────────────────────

	if s.MemSessions > 0 {
		// Count sessions in the current period
		var periodSessions int64
		for _, c := range s.SessionSizeDist {
			periodSessions += c
		}

		if periodSessions > 0 {
			headerLine := fmt.Sprintf("  %s  %s  %s",
				styleValue.Render(fmt.Sprintf("%s sessions", fmtNum(periodSessions))),
				styleDim.Render(fmt.Sprintf("avg %.0f turns", s.AvgTurns)),
				styleDim.Render(fmt.Sprintf("avg %.0f tool calls", s.AvgToolCalls)),
			)

			bucketLabels := [5]string{"1-5 turns", "6-20 turns", "21-50 turns", "51-100 turns", "100+ turns"}
			var maxBucket int64
			for _, c := range s.SessionSizeDist {
				if c > maxBucket {
					maxBucket = c
				}
			}

			const barW = 20
			var distLines []string
			for i, c := range s.SessionSizeDist {
				pct := int(float64(c) / float64(periodSessions) * 100)
				rate := float64(0)
				if maxBucket > 0 {
					rate = float64(c) / float64(maxBucket)
				}
				distLines = append(distLines, fmt.Sprintf("  %-14s %s  %s",
					styleLabel.Render(bucketLabels[i]),
					renderBar(rate, barW),
					styleDim.Render(fmt.Sprintf("%s (%d%%)", fmtNum(c), pct)),
				))
			}

			lines := append([]string{headerLine, ""}, distLines...)
			sections = append(sections,
				stylePanel.Width(innerW).Render(" Session Profile\n\n"+strings.Join(lines, "\n")),
				"",
			)
		}
	}

	// ── Commands Guided ────────────────────────────────────────────────────

	if len(s.TopCommands) > 0 {
		var maxCount int64
		for _, u := range s.TopCommands {
			if u.Count > maxCount {
				maxCount = u.Count
			}
		}
		const barCols = 20
		lines := make([]string, 0, len(s.TopCommands))
		for _, u := range s.TopCommands {
			filled := int(float64(barCols) * float64(u.Count) / float64(maxCount))
			bar := styleBar.Render(strings.Repeat("█", filled)) +
				styleDim.Render(strings.Repeat("░", barCols-filled))
			name := truncate(u.UnitID, 22)
			lines = append(lines, fmt.Sprintf("  %-22s  %s  %s",
				name, bar,
				styleValue.Render(fmt.Sprintf("%d", u.Count)),
			))
		}
		sections = append(sections,
			stylePanel.Width(innerW).Render(" Commands Guided\n\n"+strings.Join(lines, "\n")),
			"",
		)
	}

	// ── Memory Searches ────────────────────────────────────────────────────

	if len(s.RecentSearches) > 0 {
		lines := make([]string, 0, len(s.RecentSearches))
		for _, r := range s.RecentSearches {
			ts := ""
			if len(r.Timestamp) >= 19 {
				// "2026-03-27T20:51:24..." → "20:51"
				ts = r.Timestamp[11:16]
			}
			q := truncate(r.Query, 40)
			var resultStr string
			if r.ResultCount == 0 {
				resultStr = styleDim.Render("0 results")
			} else if r.ResultCount == 1 {
				resultStr = styleSaved.Render("1 session recalled")
			} else {
				resultStr = styleSaved.Render(fmt.Sprintf("%d sessions recalled", r.ResultCount))
			}
			lines = append(lines, fmt.Sprintf("  %s  %-42s  %s",
				styleDim.Render(ts),
				styleValue.Render(fmt.Sprintf("%q", q)),
				resultStr,
			))
		}
		sections = append(sections,
			stylePanel.Width(innerW).Render(" Memory Searches\n\n"+strings.Join(lines, "\n")),
			"",
		)
	}

	return strings.Join(sections, "\n")
}

func (m model) View() string {
	if m.width == 0 || !m.vpReady {
		return "initialising…"
	}

	innerW := m.width - 6

	// ── Header (pinned) ───────────────────────────────────────────────────

	spin := ""
	if m.loading {
		spin = " " + styleDim.Render("↻")
	}
	header := styleTitle.Render("KCP Dashboard") +
		styleDim.Render(" v"+version) +
		"  " + styleDim.Render("·") + "  " +
		styleDim.Render(fmt.Sprintf("last %d day%s", m.days, plural(m.days))) +
		spin

	// ── Overview (pinned) ─────────────────────────────────────────────────

	s := m.stats

	// kcp-commands row
	cmdLine1 := fmt.Sprintf("  %s   %s %s",
		styleLabel.Render("kcp-commands"),
		styleValue.Render(fmtNum(s.TotalInjects)),
		styleDim.Render("commands guided"),
	)
	if s.UniqueTools > 0 {
		cmdLine1 += styleDim.Render(fmt.Sprintf("   %d unique tools", s.UniqueTools))
	}
	cmdLine2 := ""
	if s.InjectTokens > 0 {
		coveragePart := ""
		if s.ManifestHitRate > 0 {
			coveragePart = fmt.Sprintf("  %s manifest coverage",
				styleValue.Render(fmt.Sprintf("%d%%", int(s.ManifestHitRate*100))))
		} else {
			coveragePart = styleDim.Render(fmt.Sprintf("%d manifests available", s.ManifestCount))
		}
		cmdLine2 = fmt.Sprintf("  %s   %s  %s  %s",
			styleDim.Render("            "),
			styleValue.Render(fmt.Sprintf("~%s tokens of context delivered", fmtNum(s.InjectTokens))),
			styleDim.Render("·"),
			coveragePart,
		)
	} else if s.ManifestCount > 0 && s.TotalInjects == 0 {
		cmdLine2 = fmt.Sprintf("  %s   %s",
			styleDim.Render("            "),
			styleDim.Render(fmt.Sprintf("%d manifests available, no commands intercepted yet", s.ManifestCount)),
		)
	}

	// kcp-memory row
	var memLine1, memLine2 string
	if s.MemSessions > 0 {
		memLine1 = fmt.Sprintf("  %s   %s %s",
			styleLabel.Render("kcp-memory  "),
			styleValue.Render(fmtNum(s.MemSessions)),
			styleDim.Render("sessions indexed"),
		)
		if s.MemProjects > 0 {
			memLine1 += styleDim.Render(fmt.Sprintf("   %d projects", s.MemProjects))
		}
	} else {
		memLine1 = fmt.Sprintf("  %s   %s",
			styleLabel.Render("kcp-memory  "),
			styleDim.Render("no sessions indexed yet"),
		)
	}
	if s.TotalSearches == 0 {
		memLine2 = fmt.Sprintf("  %s   %s",
			styleDim.Render("            "),
			styleDim.Render("no searches yet — ready when Claude needs it"),
		)
	} else if s.SuccessSearches == 0 {
		hint := "← memory building"
		if s.MemSessions > 0 {
			hint = "← no matches yet"
		}
		memLine2 = fmt.Sprintf("  %s   %s   %s",
			styleDim.Render("            "),
			styleWarn.Render(fmt.Sprintf("0 of %d searches found results", s.TotalSearches)),
			styleDim.Render(hint),
		)
	} else {
		pct := int(float64(s.SuccessSearches) / float64(s.TotalSearches) * 100)
		memLine2 = fmt.Sprintf("  %s   %s",
			styleDim.Render("            "),
			styleSaved.Render(fmt.Sprintf("%d of %d searches recalled prior work (%d%%)",
				s.SuccessSearches, s.TotalSearches, pct)),
		)
	}

	// Projects row
	ovLines := []string{cmdLine1}
	if cmdLine2 != "" {
		ovLines = append(ovLines, cmdLine2)
	}
	ovLines = append(ovLines, memLine1, memLine2)

	if len(s.Projects) > 0 {
		proj := strings.Join(s.Projects, ", ")
		if len(proj) > 60 {
			proj = proj[:57] + "…"
		}
		ovLines = append(ovLines, fmt.Sprintf("  %s   %s",
			styleLabel.Render("Projects    "),
			styleDim.Render(proj),
		))
	}

	overview := stylePanel.Width(innerW).Render(" Overview\n\n" + strings.Join(ovLines, "\n"))

	// ── Status bar (pinned) ───────────────────────────────────────────────

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

	// ── Compose ───────────────────────────────────────────────────────────

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
