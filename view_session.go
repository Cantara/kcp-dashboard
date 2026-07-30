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

// loadTracesFor loads the decision traces for the selected session (empty if none).
func loadTracesFor(dbPath string, sessions []SessionRow, sel int) []DecisionTraceRow {
	if sel < 0 || sel >= len(sessions) {
		return nil
	}
	traces, _ := loadSessionTraces(dbPath, sessions[sel].SessionID)
	return traces
}

// renderGateFunnel renders the 13-gate cascade for one trace: a bar per gate
// showing how many candidate units it passed vs. total — where units drop out.
func renderGateFunnel(gates []GateStat, width int) string {
	if len(gates) == 0 {
		return styleDim.Render("    (no gate summary)")
	}
	barW := width - 26
	if barW < 6 {
		barW = 6
	}
	var b strings.Builder
	for _, g := range gates {
		total := g.Passed + g.Failed
		rate := 0.0
		if total > 0 {
			rate = float64(g.Passed) / float64(total)
		}
		b.WriteString(fmt.Sprintf("    %-13s %s %s\n",
			truncate(g.Gate, 13),
			renderBar(rate, barW),
			styleDim.Render(fmt.Sprintf("%d/%d", g.Passed, total)),
		))
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderDecisions renders the right pane in decisions mode: each governance
// decision as task → gate funnel → per-unit verdict (✓ selected / ✗ skipped).
func renderDecisions(traces []DecisionTraceRow, width int) string {
	if len(traces) == 0 {
		return styleDim.Render("  no decision traces recorded")
	}
	var b strings.Builder
	for i, tr := range traces {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("  %s  %s\n",
			styleValue.Render(truncate(tr.Task, width-24)),
			styleDim.Render(fmt.Sprintf("%d selected / %d skipped", tr.SelectedCount, tr.SkippedCount)),
		))
		b.WriteString(renderGateFunnel(tr.GateSummary, width) + "\n")
		for _, u := range tr.Units {
			if u.Outcome == "selected" {
				score := ""
				if u.Score > 0 {
					score = styleDim.Render(fmt.Sprintf("  (%.2f)", u.Score))
				}
				b.WriteString("    " + styleSaved.Render("✓ ") + u.UnitID + score + "\n")
			} else {
				why := u.RejectedBy
				if why == "" {
					why = "skipped"
				}
				b.WriteString("    " + styleWarn.Render("✗ ") + styleDim.Render(u.UnitID+" — "+why) + "\n")
			}
			// RFC-0029: surface any action_scope.deny the unit carries, even when
			// it was selected — deny overrides allow, fail-closed, so the operator
			// must see the prohibition, not just the allow.
			if line := renderDeny(u.Deny); line != "" {
				b.WriteString(line + "\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderDeny renders a unit's action_scope.deny as a ⛔ prohibition line listing
// the denied tokens per dimension (§4.3a, RFC-0029). Returns "" for a nil/empty
// deny, which is a no-op and is not surfaced.
func renderDeny(d *DenyScope) string {
	if !d.nonEmpty() {
		return ""
	}
	var parts []string
	if len(d.Tools) > 0 {
		parts = append(parts, "tools "+strings.Join(d.Tools, ", "))
	}
	if len(d.Paths) > 0 {
		parts = append(parts, "paths "+strings.Join(d.Paths, ", "))
	}
	if len(d.Capabilities) > 0 {
		parts = append(parts, "capabilities "+strings.Join(d.Capabilities, ", "))
	}
	return "      " + styleWarn.Render("⛔ deny ") + styleDim.Render(strings.Join(parts, "; "))
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

	title := " Steps"
	if m.showDecisions {
		title = " Decisions"
	}
	detail := styleTitle.Render(title)
	if m.sessionSel >= 0 && m.sessionSel < len(m.sessions) {
		sel := m.sessions[m.sessionSel]
		detail += styleDim.Render(fmt.Sprintf("  %s · %s · %dt",
			shortID(sel.SessionID), truncate(sel.Model, 16), sel.Turns))
	}
	body := renderSteps(m.steps, rightW)
	if m.showDecisions {
		body = renderDecisions(m.traces, rightW)
	}
	right := detail + "\n\n" + body

	leftCol := lipgloss.NewStyle().Width(leftW).Render(left)
	rightCol := lipgloss.NewStyle().Width(rightW).Render(right)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftCol, "   ", rightCol)
}
