package main

import (
	"context"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// tabLine is the popup's first line: the tab bar.
func tabLine(m model) string { return strings.SplitN(screenText(m), "\n", 2)[0] }

// demoIn is the demo's lists in workspace w, opened as a real popup opens
// one: on w's default filter, kept in the test's state dir.
func demoIn(t *testing.T, w workspace) model {
	t.Helper()
	m := newModel(context.Background(), config{}, "/repo")
	m.width, m.height = 100, 30
	m, _ = m.useWorkspace(w, false)
	m.client = newDemoSource()
	return drain(m, tea.Batch(m.loadIssues(), m.loadProjects(), m.loadRecent()))
}

// ctrl+f lists the workspace's teams; space checks one and enter narrows
// the lists to it, asking for them again, and the tab bar says so.
func TestTheTeamFilterNarrowsTheLists(t *testing.T) {
	m := demoModel(t)
	if strings.Contains(tabLine(m), "HAL") {
		t.Fatalf("a filter shown with none set: %q", tabLine(m))
	}
	m = press(m, "ctrl+f")
	text := screenText(m)
	if m.screen != screenFilter || !strings.Contains(text, "[ ] All teams") || !strings.Contains(text, "[ ] Halcyon") || !strings.Contains(text, "[ ] Mobile") {
		t.Fatalf("screen %v:\n%s", m.screen, text)
	}
	m = press(press(press(m, "down"), "down"), " ") // Mobile
	if text := screenText(m); !strings.Contains(text, "[x] Mobile") || !strings.Contains(text, "[ ] Halcyon") || !strings.Contains(text, "[-] All teams") {
		t.Fatalf("after space:\n%s", text)
	}
	m = press(m, "enter")
	if m.screen != screenList || !strings.Contains(tabLine(m), "MOB") || strings.Contains(tabLine(m), "HAL") {
		t.Fatalf("screen %v, tabs %q", m.screen, tabLine(m))
	}
	if !m.loaded[tabMine] || len(m.issues) != 0 || len(m.projects) != 0 || len(m.recent) != 0 {
		t.Fatalf("not asked again for Mobile's: %d issues, %d projects, %d recent", len(m.issues), len(m.projects), len(m.recent))
	}
}

// The teams can be chosen before the first lists arrive: they're what the
// lists are asked for.
func TestTheTeamFilterOpensWhileTheListsLoad(t *testing.T) {
	m := newModel(context.Background(), config{}, "/repo")
	m.width, m.height = 100, 30
	m, _ = m.useWorkspace(workspace{ID: "ws-acme", Name: "Acme"}, false)
	m.client = newDemoSource()
	if m.mode != modeLoading {
		t.Fatalf("mode %v", m.mode)
	}
	if m = press(m, "ctrl+f"); m.screen != screenFilter || !strings.Contains(screenText(m), "Mobile") {
		t.Fatalf("screen %v:\n%s", m.screen, screenText(m))
	}
}

// The filter stays in the tab bar when there's no room for everything:
// where you are past the tabs gives way first.
func TestTheTeamFilterAlwaysShows(t *testing.T) {
	m := press(press(press(press(demoModel(t), "ctrl+f"), "down"), "down"), " ")
	m = press(press(press(m, "enter"), "tab"), "ctrl+f")
	m = press(press(m, " "), "enter") // every team: HAL, MOB
	m = press(m, "enter")             // a project's screen
	for _, w := range []int{100, 60, 45} {
		m.width = w
		if line := tabLine(m); m.screen != screenProject || !strings.Contains(line, "HAL, MOB") {
			t.Errorf("width %d, screen %v: %q", w, m.screen, line)
		}
	}
}

// "All teams" checks every team shown, and clears them when they all are;
// with a search typed, it's the teams found.
func TestAllTeamsChecksTheTeamsShown(t *testing.T) {
	m := press(press(demoModel(t), "ctrl+f"), " ")
	if text := screenText(m); !strings.Contains(text, "[x] All teams") || !strings.Contains(text, "[x] Halcyon") || !strings.Contains(text, "[x] Mobile") {
		t.Fatalf("all:\n%s", text)
	}
	m = press(m, " ")
	if text := screenText(m); !strings.Contains(text, "[ ] All teams") || !strings.Contains(text, "[ ] Halcyon") || !strings.Contains(text, "[ ] Mobile") {
		t.Fatalf("none:\n%s", text)
	}
	m = typeText(m, "mob")
	if text := screenText(m); strings.Contains(text, "Halcyon") || !strings.Contains(text, "Mobile") {
		t.Fatalf("searched for mob:\n%s", text)
	}
	m = press(press(press(m, "up"), " "), "enter") // All teams: the one found
	if got := m.teamFilter.Teams; len(got) != 1 || got[0] != "MOB" {
		t.Fatalf("teams %v", got)
	}
}

// esc leaves the filter as it was, and the lists with it.
func TestEscLeavesTheTeamFilterAlone(t *testing.T) {
	m := demoModel(t)
	gen := m.gen
	m = press(press(press(m, "ctrl+f"), " "), "esc")
	if m.screen != screenList || len(m.teamFilter.Teams) != 0 || m.gen != gen || len(m.issues) != 10 {
		t.Fatalf("screen %v, teams %v, gen %d → %d, %d issues", m.screen, m.teamFilter.Teams, gen, m.gen, len(m.issues))
	}
	if text := screenText(press(m, "ctrl+f")); strings.Contains(text, "[x]") {
		t.Fatalf("the cancelled checks came back:\n%s", text)
	}
}

// ctrl+s keeps the filter as the workspace's default, which the next popup
// opens on; enter is for this popup only.
func TestCtrlSKeepsTheTeamFilterForNextTime(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	acme := workspace{ID: "ws-acme", Name: "Acme"}
	m := press(press(press(press(demoIn(t, acme), "ctrl+f"), "down"), " "), "ctrl+s") // Halcyon
	if m.screen != screenList || !strings.Contains(tabLine(m), "HAL") {
		t.Fatalf("screen %v, tabs %q", m.screen, tabLine(m))
	}
	if info, err := os.Stat(savedFiltersPath()); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("filters.json: %v, %v", info, err)
	}

	m = demoIn(t, acme)
	if got := m.teamFilter.Teams; len(got) != 1 || got[0] != "HAL" || !strings.Contains(tabLine(m), "HAL") || len(m.issues) != 10 {
		t.Fatalf("next popup: teams %v, tabs %q, %d issues", got, tabLine(m), len(m.issues))
	}
	m = press(press(press(press(m, "ctrl+f"), "down"), " "), "enter") // Halcyon off: every team
	if len(m.teamFilter.Teams) != 0 || strings.Contains(tabLine(m), "HAL") {
		t.Fatalf("teams %v, tabs %q", m.teamFilter.Teams, tabLine(m))
	}
	if got := demoIn(t, acme).teamFilter.Teams; len(got) != 1 || got[0] != "HAL" {
		t.Fatalf("enter changed the default: %v", got)
	}
}
