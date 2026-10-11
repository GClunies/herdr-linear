package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestTargetIssueIsAnIdentifierAndNothingElse(t *testing.T) {
	for raw, want := range map[string]string{
		"ENG-142": "ENG-142", "eng-142": "ENG-142", " HAL-7 ": "HAL-7",
		"": "", "ENG": "", "ENG-142 OPS-3": "", "me/eng-142-fix": "", "ENG-0": "", "../x-1": "",
	} {
		t.Setenv(issueEnv, raw)
		ref, ok := targetIssue()
		if got := ref.String(); (ok && got != want) || (!ok && want != "") {
			t.Errorf("%q: %q %v, want %q", raw, got, ok, want)
		}
	}
}

// Opened on an issue (as herdr-recap opens it for an agent's), the popup is
// on that issue's screen, and esc goes back to where it was opened from.
func TestThePopupOpensOnAnIssueAndEscGoesBack(t *testing.T) {
	var opened []popupRef
	old := handOver
	handOver = func(p popupRef) error { opened = append(opened, p); return nil }
	t.Cleanup(func() { handOver = old })

	m := demoModel(t)
	id := m.issues[0].Identifier
	ref := branchRefs(id)[0]
	m.target, m.back = &ref, &popupRef{PluginID: "herdr-recap", Entrypoint: "picker"}
	m = drain(m, m.findTarget(ref))
	if m.screen != screenIssue || m.cur == nil || m.cur.Identifier != id || !m.direct || m.target != nil {
		t.Fatalf("not on %s: screen %v cur %+v", id, m.screen, m.cur)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "esc back") || !strings.Contains(view, "← lists") {
		t.Fatalf("the footer doesn't say how to leave:\n%s", view)
	}
	// ← is the lists, from where esc still goes back.
	lists := press(m, "left")
	if lists.screen != screenList || lists.direct {
		t.Fatal("← didn't go to the lists")
	}
	if _, cmd := lists.Update(keyMsg("esc")); cmd == nil || cmd() != tea.Quit() || len(opened) != 1 {
		t.Fatalf("esc on the lists: opened %+v", opened)
	}
	if _, cmd := m.Update(keyMsg("esc")); cmd == nil || cmd() != tea.Quit() || len(opened) != 2 || opened[1].PluginID != "herdr-recap" {
		t.Fatalf("esc on the issue: opened %+v", opened)
	}
	// An issue opened from the lists goes back to them, as ever.
	if back := press(press(lists, "enter"), "esc"); back.screen != screenList {
		t.Fatal("esc on an issue opened from the list left the popup")
	}

	// One that isn't there says so, on the lists.
	m = demoModel(t)
	none := issueRef{"HAL", 99999}
	m.target = &none
	if m = drain(m, m.findTarget(none)); m.screen != screenList || !strings.Contains(m.err, "HAL-99999") {
		t.Fatalf("screen %v err %q", m.screen, m.err)
	}
}

func TestTheIssuesWorkspaceIsTheReposOrTheOneWithItsTeam(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	st := readLabelState()
	st.Teams["L1"], st.Teams["L2"] = teamKeys{Keys: []string{"ENG", "OPS"}}, teamKeys{Keys: []string{"ENG"}}
	if err := writeLabelState(st); err != nil {
		t.Fatal(err)
	}
	ix := workspaceIndex{Workspaces: []workspace{{ID: "L1"}, {ID: "L2"}}, Repos: map[string]string{"/given": "L2"}}
	if w := workspaceFor(ix, "/free", issueRef{"OPS", 1}); w == nil || w.ID != "L1" {
		t.Errorf("the one with the team: %+v", w)
	}
	if w := workspaceFor(ix, "/free", issueRef{"ENG", 1}); w != nil {
		t.Errorf("two have the team: %+v", w)
	}
	if w := workspaceFor(ix, "/given", issueRef{"OPS", 1}); w == nil || w.ID != "L2" {
		t.Errorf("the repo's: %+v", w)
	}
}
