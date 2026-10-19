package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// demoModel is the picker as `picker --demo` runs it, sized like a popup.
func demoModel(t *testing.T) model {
	t.Helper()
	// Any network or keychain use from the demo fails the test.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("demo made a request to %s", r.URL)
	}))
	oldG, oldT := graphqlURL, tokenURL
	graphqlURL, tokenURL = srv.URL, srv.URL
	oldR := readStore
	readStore = func(string) (*tokens, error) { t.Error("demo read the keychain"); return nil, errSignedOut }
	t.Cleanup(func() { graphqlURL, tokenURL, readStore = oldG, oldT, oldR; srv.Close() })

	m := newModel(context.Background(), config{}, "")
	m.client = newDemoSource()
	m.width, m.height = 100, 30
	return drain(m, m.Init())
}

// drain feeds a command's messages back into the model, like the runtime does,
// skipping the spinner's endless ticks.
func drain(m model, cmd tea.Cmd) model {
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if _, tick := msg.(spinner.TickMsg); tick {
			continue
		}
		if msg == nil {
			continue
		}
		next, more := m.Update(msg)
		m = next.(model)
		queue = append(queue, more)
	}
	return m
}

func press(m model, k string) model {
	next, cmd := m.Update(keyMsg(k))
	return drain(next.(model), cmd)
}

func TestDemoListsOnlyYourOpenIssues(t *testing.T) {
	m := demoModel(t)
	if m.mode != modeList || len(m.issues) != 10 {
		t.Fatalf("mode %v, %d issues", m.mode, len(m.issues))
	}
	for _, is := range m.issues {
		if is.Assignee == nil || !is.Assignee.IsMe {
			t.Errorf("%s isn't yours", is.Identifier)
		}
	}
	if m.issues[0].State.Name != "In Review" {
		t.Errorf("work closest to done should come first, got %s", m.issues[0].State.Name)
	}
	if !m.worktrees["sam/hal-231-search-index-drops-accents-in-note-titles"] {
		t.Error("demo worktrees not loaded")
	}
}

func TestDemoProjectShowsTeammatesWork(t *testing.T) {
	m := press(demoModel(t), "tab")
	if len(m.projects) != 5 || m.projects[0].Lead == nil || !m.projects[0].Lead.IsMe {
		t.Fatalf("projects: %+v", m.projects)
	}
	m = press(press(m, "enter"), "i") // Offline sync → its issues
	var others int
	for _, is := range m.proj.issues {
		if is.Assignee == nil || !is.Assignee.IsMe {
			others++
		}
	}
	if len(m.proj.issues) != 8 || others != 7 {
		t.Fatalf("%d issues, %d not yours", len(m.proj.issues), others)
	}
}

// offlineSync is the demo on Offline sync's issues: its graph,
//
//	● HAL-201───┬─ ◕ HAL-212───── ○ HAL-219─┬─ ◌ HAL-228
//	◔ HAL-245───┴─ ◔ HAL-205───── ◔ HAL-226─┘
//
// and HAL-216, linked to nothing, under it.
func offlineSync(t *testing.T) model {
	t.Helper()
	return press(press(press(demoModel(t), "tab"), "enter"), "i")
}

func selectedID(m model) string {
	if r := m.selected(); r != nil && r.issue != nil {
		return r.issue.Identifier
	}
	return ""
}

// selectIssue presses the arrows that walk the graph to id.
func selectIssue(t *testing.T, m model, id string, keys ...string) model {
	t.Helper()
	for _, k := range keys {
		m = press(m, k)
	}
	if got := selectedID(m); got != id {
		t.Fatalf("after %v on %s, want %s", keys, got, id)
	}
	return m
}

// The arrows go to the nearest issue that way: across the columns, up and
// down the lines, down out of the graph into the issues linked to nothing
// and back up to where you left it.
func TestDemoGraphArrowsGoToTheNearestIssue(t *testing.T) {
	m := offlineSync(t)
	if got := selectedID(m); got != "HAL-201" {
		t.Fatalf("starts on %s, want the first issue HAL-201", got)
	}
	m = selectIssue(t, m, "HAL-212", "right")
	m = selectIssue(t, m, "HAL-205", "down")
	m = selectIssue(t, m, "HAL-216", "down")
	m = selectIssue(t, m, "HAL-216", "down")
	m = selectIssue(t, m, "HAL-205", "up")
	m = selectIssue(t, m, "HAL-226", "right")
	m = selectIssue(t, m, "HAL-228", "right", "right")
	m = selectIssue(t, m, "HAL-226", "down")
	// ← at the first column stays put; esc is the way back.
	m = selectIssue(t, m, "HAL-245", "left", "left", "left")
	if m.drilled == nil {
		t.Fatal("← left the project")
	}
	if m = press(m, "esc"); m.drilled != nil {
		t.Fatal("esc should leave the project")
	}
}

// Every issue of the project is listed once, done ones too: in the graph,
// or under Not linked. The blocker from another project is in it as well.
func TestDemoGraphListsEveryIssueOnce(t *testing.T) {
	m := offlineSync(t)
	seen := map[string]int{}
	for _, r := range m.rows() {
		seen[r.issue.Identifier]++
	}
	all, _ := m.client.projectIssues(context.Background(), "a1f3")
	for _, is := range all {
		if seen[is.Identifier] != 1 {
			t.Errorf("%s listed %d times", is.Identifier, seen[is.Identifier])
		}
	}
	if len(seen) != len(all) {
		t.Errorf("listed %v, want %d issues", seen, len(all))
	}
	text := screenText(m)
	for _, want := range []string{"HAL-201───┬─", "Not linked 1", "HAL-216", "Not started", "PR merged"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q on screen:\n%s", want, text)
		}
	}
}

// Above the footer, the selected issue: its title, status and PR stage,
// its worktree, and what still holds it up (a done blocker doesn't). The
// footer's ctrl+w says whether it goes to a worktree or makes one.
func TestDemoGraphSaysWhatTheSelectedIssueIs(t *testing.T) {
	m := offlineSync(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 30}) // room for every key in full
	m = selectIssue(t, next.(model), "HAL-205", "right", "down")
	status := strings.Split(screenText(m), "\n")[m.height-2]
	for _, want := range []string{"Sync queue survives an app kill", "In Progress", "PR open", "after HAL-245"} {
		if !strings.Contains(status, want) {
			t.Errorf("no %q in %q", want, status)
		}
	}
	if strings.Contains(status, "HAL-201") || strings.Contains(status, "worktree") {
		t.Errorf("status %q", status)
	}
	if footer := strings.Split(screenText(m), "\n")[m.height-1]; !strings.Contains(footer, "^w new worktree") {
		t.Errorf("footer %q", footer)
	}
	m = selectIssue(t, m, "HAL-212", "up")
	if status := strings.Split(screenText(m), "\n")[m.height-2]; !strings.Contains(status, "worktree open") || strings.Contains(status, "after") {
		t.Errorf("status %q", status)
	}
	if footer := strings.Split(screenText(m), "\n")[m.height-1]; !strings.Contains(footer, "^w go to worktree") {
		t.Errorf("footer %q", footer)
	}
}

// ctrl+w goes to the issue's worktree when it has one, and makes one when
// it hasn't, from any list.
func TestDemoCtrlWGoesToTheWorktreeOrMakesOne(t *testing.T) {
	m := selectIssue(t, offlineSync(t), "HAL-212", "right")
	if m = press(m, "ctrl+w"); !strings.HasPrefix(m.flash, "Demo: would open the worktree on sam/hal-212-") {
		t.Errorf("HAL-212: %q", m.flash)
	}
	m = selectIssue(t, m, "HAL-205", "down")
	if m = press(m, "ctrl+w"); !strings.HasPrefix(m.flash, "Demo: would create a worktree on sam/hal-205-") {
		t.Errorf("HAL-205: %q", m.flash)
	}
	mine := demoModel(t) // My issues, on HAL-212
	if mine = press(mine, "ctrl+w"); !strings.HasPrefix(mine.flash, "Demo: would open the worktree on sam/hal-212-") {
		t.Errorf("My issues: %q", mine.flash)
	}
}

// Too wide or too tall for the popup, the graph scrolls to keep the
// selection on screen.
func TestDemoGraphScrollsToTheSelection(t *testing.T) {
	m := offlineSync(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 9})
	m = selectIssue(t, next.(model), "HAL-228", "right", "right", "right")
	text := screenText(m)
	if !strings.Contains(text, "HAL-228") || strings.Contains(text, "HAL-201") {
		t.Errorf("HAL-228 not in view:\n%s", text)
	}
	m = selectIssue(t, m, "HAL-216", "down", "down")
	if text := screenText(m); !strings.Contains(text, "HAL-216") {
		t.Errorf("HAL-216 not in view:\n%s", text)
	}
	for _, line := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(line); w > m.width {
			t.Errorf("line %d cells wide: %q", w, stripANSI(line))
		}
	}
}

// Offline sync's issues make a graph with one of everything: each stage, a
// fan-out and a fan-in, a blocker from another project, an issue linked to
// nothing, and a canceled one that isn't there at all.
func TestDemoOfflineSyncIsAGraph(t *testing.T) {
	is, err := newDemoSource().projectIssues(context.Background(), "a1f3")
	if err != nil {
		t.Fatal(err)
	}
	g := buildGraph(is, "a1f3")
	want := strings.Join([]string{
		" ● HAL-201───┬─ ◕ HAL-212───── ○ HAL-219─┬─ ◌ HAL-228",
		" ◔ HAL-245───┴─ ◔ HAL-205───── ◔ HAL-226─┘",
	}, "\n")
	if got := drawn(g); got != want {
		t.Errorf("drawn\n%s\nwant\n%s", got, want)
	}
	stages := map[stage]bool{}
	for _, n := range g.Nodes {
		if n.External != (n.Issue.Identifier == "HAL-245") {
			t.Errorf("%s external %v", n.Issue.Identifier, n.External)
		}
		if !n.External {
			stages[n.Issue.stage()] = true
		}
	}
	if len(stages) != len(stageNames) {
		t.Errorf("stages shown %v, want all %d", stages, len(stageNames))
	}
	if len(g.Unlinked) != 1 || g.Unlinked[0].Identifier != "HAL-216" {
		t.Errorf("not linked %v", g.Unlinked)
	}
	for _, x := range is {
		if x.State.Type == "canceled" {
			t.Errorf("canceled %s listed", x.Identifier)
		}
	}
}

func TestDemoStatusChangeSticks(t *testing.T) {
	m := demoModel(t)
	m = press(m, "enter") // HAL-212, In Review
	m = press(press(m, "c"), "down")
	m = press(m, "enter") // → Done
	if m.cur.State.Name != "Done" || m.flash != "Moved to Done" {
		t.Fatalf("state %s flash %q", m.cur.State.Name, m.flash)
	}
	fresh, _ := m.client.myIssues(context.Background(), listFilter{})
	for _, is := range fresh {
		if is.Identifier == "HAL-212" {
			t.Fatal("a Done issue should leave My issues on the next load")
		}
	}
}

func TestDemoStartPretendsAndStaysOpen(t *testing.T) {
	m := demoModel(t)
	var todo issue
	for _, is := range m.issues {
		if is.Identifier == "HAL-240" {
			todo = is
		}
	}
	m = press(m, "enter")
	m.cur = &todo
	m = press(m, "s")
	if m.mode != modeList || m.screen != screenIssue {
		t.Fatalf("demo should stay open: mode %v screen %v", m.mode, m.screen)
	}
	if !strings.Contains(m.flash, "Demo: would move HAL-240 to In Progress, create a worktree") {
		t.Fatalf("flash %q", m.flash)
	}
	if m.cur.State.Name != "In Progress" || !m.worktrees[issueTarget(todo).Branch] {
		t.Fatalf("state %s, worktree marked %v", m.cur.State.Name, m.worktrees[issueTarget(todo).Branch])
	}
}

func TestDemoNeverOpensTheBrowser(t *testing.T) {
	old := browse
	browse = func(string) error { t.Error("demo opened a browser"); return nil }
	t.Cleanup(func() { browse = old })
	m := press(demoModel(t), "ctrl+o")
	if !strings.HasPrefix(m.flash, "Demo: would open https://linear.app/halcyon/") {
		t.Fatalf("flash %q", m.flash)
	}
}

// Screenshots come from the demo, so nothing from a real workspace may show
// up on any of its screens.
func TestDemoScreensContainNothingReal(t *testing.T) {
	banned := []string{"ACT-", "Action", "hjortur", "Hjortur", "Olafsson", "olafsson", "mrolafsson"}
	check := func(where string, m model) {
		t.Helper()
		s := screenText(m)
		for _, b := range banned {
			if strings.Contains(s, b) {
				t.Errorf("%s shows %q:\n%s", where, b, s)
			}
		}
	}
	m := demoModel(t)
	check("my issues", m)
	for i := range m.issues {
		d := m
		d.cursor = 0
		d = press(d, "enter")
		d.cur = &m.issues[i]
		d = drain(d, func() tea.Msg {
			det, _ := d.client.issueDetail(context.Background(), m.issues[i].ID)
			return issueDetailMsg{id: m.issues[i].ID, detail: det}
		})
		check("issue "+m.issues[i].Identifier, d)
	}
	p := press(m, "tab")
	check("projects", p)
	p = press(p, "enter")
	check("project page", p)
	check("project issues", press(p, "i"))
}
