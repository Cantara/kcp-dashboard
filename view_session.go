package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// clampIndex keeps a selection index within [0, n-1]; returns 0 for an empty set.
func clampIndex(i, n int) int {
	if n <= 0 || i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// loadStepsFor loads the tool_events for the selected session (empty if none).
func loadStepsFor(dbPath string, sessions []SessionRow, sel int) []SessionStep {
	if sel < 0 || sel >= len(sessions) {
		return nil
	}
	steps, _ := loadSessionSteps(dbPath, sessions[sel].SessionID)
	return steps
}

// renderSessionList renders the left column: one session per entry, the
// selected row marked with ▸.
func renderSessionList(sessions []SessionRow, sel, width int) string {
	if len(sessions) == 0 {
		return styleDim.Render("  no sessions in range")
	}
	var b strings.Builder
	for i, s := range sessions {
		id := shortID(s.SessionID)
		marker := "  "
		idStr := styleLabel.Render(id)
		if i == sel {
			marker = styleValue.Render("▸ ")
			idStr = styleValue.Render(id)
		}
		b.WriteString(fmt.Sprintf("%s%s  %s  %s\n",
			marker, idStr,
			styleDim.Render(truncate(s.Model, 18)),
			styleDim.Render(fmt.Sprintf("%dt", s.Turns)),
		))
		if s.Task != "" {
			b.WriteString("    " + styleDim.Render(truncate(s.Task, width-4)) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderSteps renders the right column: the ordered step timeline for one
// session. A ✓ marks steps that received KCP guidance, · those that did not.
func renderSteps(steps []SessionStep, width int) string {
	if len(steps) == 0 {
		return styleDim.Render("  no steps recorded")
	}
	var b strings.Builder
	for _, s := range steps {
		mark := styleDim.Render("·")
		if s.Guided {
			mark = styleSaved.Render("✓")
		}
		b.WriteString(fmt.Sprintf("  %s %-5s %s\n",
			mark, s.Tool, truncate(s.Command, width-14),
		))
		if s.OutputPreview != "" {
			b.WriteString("       " + styleDim.Render(truncate(s.OutputPreview, width-9)) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderSessionBrowser composes the split list + detail view shown in the
// viewport while in session mode.
func renderSessionBrowser(m model) string {
	innerW := m.width - 6
	if innerW < 24 {
		innerW = 24
	}
	leftW := 34
	if leftW > innerW/2 {
		leftW = innerW / 2
	}
	rightW := innerW - leftW - 3
	if rightW < 12 {
		rightW = 12
	}

	left := styleTitle.Render(" Sessions") + "\n\n" +
		renderSessionList(m.sessions, m.sessionSel, leftW)

	detail := styleTitle.Render(" Steps")
	if m.sessionSel >= 0 && m.sessionSel < len(m.sessions) {
		sel := m.sessions[m.sessionSel]
		detail += styleDim.Render(fmt.Sprintf("  %s · %s · %dt",
			shortID(sel.SessionID), truncate(sel.Model, 16), sel.Turns))
	}
	right := detail + "\n\n" + renderSteps(m.steps, rightW)

	leftCol := lipgloss.NewStyle().Width(leftW).Render(left)
	rightCol := lipgloss.NewStyle().Width(rightW).Render(right)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftCol, "   ", rightCol)
}
