package main

import (
	"context"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// cachedModel is a picker that keeps its lists, in a state dir of the
// test's own.
func cachedModel(t *testing.T, w workspace) model {
	t.Helper()
	m := newModel(context.Background(), config{}, "/repo")
	m.width, m.height, m.cache = 100, 20, true
	m, _ = m.useWorkspace(w, false)
	return m
}

// The next popup opens on the lists the last one loaded, says it's updating
// them, and takes the fresh ones when they arrive.
func TestThePopupOpensOnTheListsFromLastTime(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	acme := workspace{ID: "ws-acme", Name: "Acme", URLKey: "acme"}

	m := cachedModel(t, acme)
	if text := screenText(m); m.mode != modeLoading || !strings.Contains(text, "Loading") || strings.Contains(text, "updating") {
		t.Fatalf("first open, mode %v:\n%s", m.mode, text)
	}
	next, cmd := m.Update(issuesMsg{issues: withIDs(twoGroups()).issues, gen: m.gen})
	if cmd == nil {
		t.Fatal("a fresh list wasn't kept")
	}
	cmd()
	_, cmd = next.(model).Update(projectsMsg{gen: m.gen}) // none: still a list that loaded
	cmd()
	if info, err := os.Stat(listCachePath()); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("lists.json: %v, %v", info, err)
	}

	m = cachedModel(t, acme)
	text := screenText(m)
	if m.mode != modeList || !strings.Contains(text, "A-1 ") || !strings.Contains(text, "My issues 3  Projects 0  Recent") || !strings.Contains(text, "updating") {
		t.Fatalf("second open, mode %v:\n%s", m.mode, text)
	}
	if r := m.selected(); r == nil || r.issue.Identifier != "A-1" {
		t.Fatalf("selected %+v", r)
	}
	fresh := withIDs(listModel(mkIssue("A-9", "started", "In Review", 3, 0))).issues
	for _, msg := range []any{issuesMsg{issues: fresh, gen: m.gen}, projectsMsg{gen: m.gen}, recentMsg{gen: m.gen}} {
		next, _ := m.Update(msg)
		m = next.(model)
	}
	if text := screenText(m); !strings.Contains(text, "A-9 ") || strings.Contains(text, "A-1 ") || strings.Contains(text, "updating") {
		t.Fatalf("after the fresh lists:\n%s", text)
	}
}

// One workspace's lists are never another's, and go when you sign out.
func TestListsAreKeptByWorkspace(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	acme, globex := workspace{ID: "ws-acme", Name: "Acme"}, workspace{ID: "ws-globex", Name: "Globex"}
	saveListCache(acme.ID, tabMine, withIDs(twoGroups()).issues, nil)

	if m := cachedModel(t, globex); m.mode != modeLoading || len(m.rows()) != 0 {
		t.Fatalf("Globex opened on Acme's issues: %+v", m.rows())
	}
	if m := cachedModel(t, acme); len(m.rows()) != 3 {
		t.Fatalf("Acme's rows %+v", m.rows())
	}
	dropListCache(acme.ID)
	if m := cachedModel(t, acme); len(m.rows()) != 0 {
		t.Fatalf("still listed after signing out: %+v", m.rows())
	}
	if _, err := os.Stat(listCachePath()); !os.IsNotExist(err) {
		t.Fatalf("lists.json left behind: %v", err)
	}
}

// A list that failed to load, or was cut short, isn't kept; nor the demo's.
func TestOnlyWholeListsAreKept(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := cachedModel(t, workspace{ID: "ws-acme"})
	if _, cmd := m.Update(issuesMsg{issues: twoGroups().issues, err: errTruncated, gen: m.gen}); cmd != nil {
		t.Fatal("kept a list cut short")
	}
	m.cache = false
	if _, cmd := m.Update(issuesMsg{issues: twoGroups().issues, gen: m.gen}); cmd != nil {
		t.Fatal("kept a list with the cache off")
	}
}

// ctrl+r keeps what's listed, and usable, while it asks again.
func TestRefreshKeepsTheListWhileItUpdates(t *testing.T) {
	m := key(twoGroups(), "down")
	next, cmd := m.Update(ctrlR)
	m = next.(model)
	if cmd == nil || m.mode != modeList || !strings.Contains(screenText(m), "updating") {
		t.Fatalf("mode %v:\n%s", m.mode, screenText(m))
	}
	if r := key(m, "down").selected(); r == nil || r.issue.Identifier != "A-3" {
		t.Fatalf("the list isn't usable while updating: %+v", r)
	}
}

var ctrlR = tea.KeyMsg{Type: tea.KeyCtrlR}
