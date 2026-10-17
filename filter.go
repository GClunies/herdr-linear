package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// The team filter narrows My issues, Projects and Recent to some of the
// workspace's teams. ctrl+f on the list opens its editor: enter applies the
// teams checked there until the popup closes, ctrl+s keeps them as the
// workspace's default as well, which every popup opens on.

// listFilter narrows the lists. The zero value lists everything.
type listFilter struct {
	// Teams: only these teams' issues and projects. Empty: every team's.
	Teams []string `json:"teams"`
}

func (f listFilter) hasTeam(key string) bool {
	return len(f.Teams) == 0 || slices.Contains(f.Teams, key)
}

func (f listFilter) equal(g listFilter) bool { return slices.Equal(f.Teams, g.Teams) }

// ── the default ───────────────────────────────────────────────────────────────

// filters.json keeps each workspace's default filter, by workspace id:
// {"workspaces": {"<id>": {"teams": ["PD", "DATA"]}}}. A workspace's goes
// when you sign out of it.
type savedFilters struct {
	Workspaces map[string]listFilter `json:"workspaces"`
}

func savedFiltersPath() string { return filepath.Join(stateDir(), "filters.json") }

func readSavedFilters() savedFilters {
	var s savedFilters
	if data, err := os.ReadFile(savedFiltersPath()); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	if s.Workspaces == nil {
		s.Workspaces = map[string]listFilter{}
	}
	return s
}

// savedFilter is workspace id's default filter; the zero one if it has none.
func savedFilter(id string) listFilter { return readSavedFilters().Workspaces[id] }

// saveFilter makes f workspace id's default. The zero filter clears it, and
// the file goes with the last one.
func saveFilter(id string, f listFilter) error {
	s := readSavedFilters()
	if len(f.Teams) == 0 {
		delete(s.Workspaces, id)
	} else {
		s.Workspaces[id] = f
	}
	if len(s.Workspaces) == 0 {
		if err := os.Remove(savedFiltersPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return err
	}
	tmp := savedFiltersPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, savedFiltersPath())
}

// ── the editor ────────────────────────────────────────────────────────────────

// filterEditor is the team filter's editor, open over the list.
type filterEditor struct {
	checked map[string]bool // by team key
	search  textinput.Model
	cursor  int // 0 is "All teams"; i is shownTeams()[i-1]
}

func (t teamInfo) matches(filter string) bool {
	hay := strings.ToLower(t.Name + " " + t.Key)
	for _, word := range strings.Fields(strings.ToLower(filter)) {
		if !strings.Contains(hay, word) {
			return false
		}
	}
	return true
}

// openFilter opens the editor on the filter in use, loading the teams the
// first time.
func (m model) openFilter() (tea.Model, tea.Cmd) {
	e := filterEditor{checked: map[string]bool{}}
	for _, k := range m.teamFilter.Teams {
		e.checked[k] = true
	}
	e.search = textinput.New()
	e.search.Prompt, e.search.Placeholder = "› ", "find a team"
	e.search.PromptStyle, e.search.PlaceholderStyle = styleDim, styleDim
	e.search.Focus()
	m.editor, m.screen, m.err, m.flash = e, screenFilter, "", ""
	if m.teamList == nil {
		return m, m.loadTeams()
	}
	return m, nil
}

// shownTeams is the teams the search finds.
func (m model) shownTeams() []teamInfo {
	var out []teamInfo
	for _, t := range m.teamList {
		if t.matches(m.editor.search.Value()) {
			out = append(out, t)
		}
	}
	return out
}

// checkedShown is how many of the teams shown are checked, of how many.
func (m model) checkedShown() (int, int) {
	ts, n := m.shownTeams(), 0
	for _, t := range ts {
		if m.editor.checked[t.Key] {
			n++
		}
	}
	return n, len(ts)
}

func (m model) handleFilterKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := &m.editor
	switch k.String() {
	case "esc":
		m.screen = screenList
		return m, nil
	case "up", "ctrl+p", "ctrl+k":
		e.cursor = max(0, e.cursor-1)
		return m, nil
	case "down", "ctrl+n", "ctrl+j":
		e.cursor = min(len(m.shownTeams()), e.cursor+1)
		return m, nil
	case " ":
		ts := m.shownTeams()
		if e.cursor > 0 {
			key := ts[e.cursor-1].Key
			e.checked[key] = !e.checked[key]
			return m, nil
		}
		n, of := m.checkedShown()
		for _, t := range ts {
			e.checked[t.Key] = n < of
		}
		return m, nil
	case "enter", "ctrl+s":
		if m.teamList == nil {
			return m, nil // no teams to have checked yet
		}
		return m.applyFilter(k.String() == "ctrl+s")
	}
	prev := e.search.Value()
	var cmd tea.Cmd
	e.search, cmd = e.search.Update(k)
	if e.search.Value() != prev {
		// On the first team found, for space to check it.
		e.cursor = min(1, len(m.shownTeams()))
	}
	return m, cmd
}

// applyFilter narrows the lists to the teams checked, asking Linear for them
// again when that changes them; with keep, as the workspace's default too.
func (m model) applyFilter(keep bool) (tea.Model, tea.Cmd) {
	var f listFilter
	for _, t := range m.teamList {
		if m.editor.checked[t.Key] {
			f.Teams = append(f.Teams, t.Key)
		}
	}
	m.screen = screenList
	var cmd tea.Cmd
	if !f.equal(m.teamFilter) {
		m.teamFilter = f
		m, cmd = m.relist()
	}
	// Saved before the lists land, so that keep finds them the default's.
	switch {
	case !keep:
	case m.ws == nil: // the demo, which has no workspace to keep it for
		m.flash = "Demo: would keep these teams as the default"
	default:
		if err := saveFilter(m.ws.ID, f); err != nil {
			m.err = "Couldn't keep the teams as the default: " + err.Error()
		} else {
			m.flash = "Kept as the default for " + m.ws.Name
		}
	}
	return m, cmd
}

// ── view ──────────────────────────────────────────────────────────────────────

func (m model) viewFilter() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render(" Teams") + "  " + m.editor.search.View() + "\n")
	if m.teamList == nil {
		b.WriteString(" " + m.spin.View() + styleDim.Render(" loading teams…") + "\n")
		return b.String()
	}
	ts := m.shownTeams()
	all := "[ ]"
	switch n, of := m.checkedShown(); {
	case n > 0 && n == of:
		all = styleOK.Render("[x]")
	case n > 0:
		all = styleOK.Render("[-]")
	}
	lines := []string{all + " All teams"}
	for _, t := range ts {
		box := "[ ]"
		if m.editor.checked[t.Key] {
			box = styleOK.Render("[x]")
		}
		lines = append(lines, box+" "+shorten(t.Name, max(10, m.width-20))+styleDim.Render("  "+t.Key))
	}
	// Under the tabs, a blank and the heading; over the status line and footer.
	room := max(1, m.height-5)
	start := max(0, m.editor.cursor-room+1)
	for i := start; i < len(lines) && i-start < room; i++ {
		line := "   " + lines[i]
		if i == m.editor.cursor {
			line = highlight(" › "+lines[i], max(20, m.width-2))
		}
		b.WriteString(line + "\n")
	}
	if len(ts) == 0 {
		b.WriteString(styleDim.Render("   No team matches.") + "\n")
	}
	return b.String()
}
