package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// gi is an unstarted issue in project p1, blocked by the issues named.
func gi(id string, blockers ...string) issue {
	is := issue{ID: id, Identifier: id, State: workflowState{Type: "unstarted"}, Project: &projectRef{ID: "p1"}}
	for _, b := range blockers {
		is.InverseRelations.Nodes = append(is.InverseRelations.Nodes,
			relation{Type: "blocks", Issue: linkedIssue{ID: b, Identifier: b, State: workflowState{Type: "unstarted"}}})
	}
	return is
}

// drawn is the graph as plain text: labels and lines unstyled.
func drawn(g projectGraph) string {
	return strings.Join(g.render(func(n *graphNode) string { return nodeText(*n.Issue) }, func(s string, _ bool) string { return s }, nil), "\n")
}

const chain = " ○ A─── ○ B─── ○ C"

func TestGraphDrawsAChainLeftToRight(t *testing.T) {
	g := buildGraph([]issue{gi("A"), gi("B", "A"), gi("C", "B")}, "p1")
	if got := drawn(g); got != chain {
		t.Fatalf("drawn\n%s\nwant\n%s", got, chain)
	}
	// Where each node is, for the keys and the mouse.
	var at []string
	for _, n := range g.Nodes {
		at = append(at, fmt.Sprintf("%s@%d,%d,%d,%d", n.Issue.Identifier, n.Level, n.Row, n.X, n.Width))
	}
	if strings.Join(at, " ") != "A@0,0,1,3 B@1,0,8,3 C@2,0,15,3" {
		t.Fatalf("nodes %v", at)
	}
}

func TestGraphDrawsFanInAndFanOut(t *testing.T) {
	for _, tc := range []struct {
		name   string
		issues []issue
		want   string
	}{
		{"fan-in", []issue{gi("A"), gi("B"), gi("C", "A", "B")}, " ○ A─┬─ ○ C\n ○ B─┘"},
		{"fan-out", []issue{gi("A"), gi("B", "A"), gi("C", "A")}, " ○ A─┬─ ○ B\n     └─ ○ C"},
		// A blocks C through B too, so A → C isn't drawn.
		{"skipped step", []issue{gi("A"), gi("B", "A"), gi("C", "B", "A")}, chain},
		// D's edge to C passes B's column.
		{"long edge", []issue{gi("A"), gi("B", "A"), gi("C", "B", "D"), gi("D")}, " ○ A───── ○ B─┬─ ○ C\n ○ D──────────┘"},
		// B's line to C crosses A's to E: the crossing is drawn as A's
		// vertical, not a junction.
		{"crossing", []issue{gi("A"), gi("B"), gi("C", "B"), gi("D", "A"), gi("E", "A")},
			" ○ A─┬─── ○ D\n ○ B─│─── ○ C\n     └─── ○ E"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := drawn(buildGraph(tc.issues, "p1")); got != tc.want {
				t.Fatalf("drawn\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

// The selected issue's own lines are lit, and only they: where lines share a
// junction, a lit line says which way the selected issue's edges go.
func TestGraphLightsTheSelectedIssuesLines(t *testing.T) {
	heavy := strings.NewReplacer("─", "━", "│", "┃", "┌", "┏", "┐", "┓", "└", "┗", "┘", "┛",
		"├", "┣", "┤", "┫", "┬", "┳", "┴", "┻", "┼", "╋")
	litDrawn := func(g projectGraph, sel string) string {
		var is *issue
		for _, n := range g.Nodes {
			if n.Issue.Identifier == sel {
				is = n.Issue
			}
		}
		return strings.Join(g.render(func(n *graphNode) string { return nodeText(*n.Issue) }, func(s string, lit bool) string {
			if lit {
				return heavy.Replace(s)
			}
			return s
		}, is), "\n")
	}
	// A blocks B and C; D blocks C. D's line meets A's at one junction.
	shared := buildGraph([]issue{gi("A"), gi("D"), gi("B", "A"), gi("C", "A", "D")}, "p1")
	long := buildGraph([]issue{gi("A"), gi("B", "A"), gi("C", "B", "D"), gi("D")}, "p1")
	crossing := buildGraph([]issue{gi("A"), gi("B"), gi("C", "B"), gi("D", "A"), gi("E", "A")}, "p1")
	for _, tc := range []struct {
		name string
		g    projectGraph
		sel  string
		want string
	}{
		{"nothing selected", shared, "", " ○ A───┬─ ○ B\n ○ D───┴─ ○ C"},
		{"a fan-in source", shared, "D", " ○ A───┬─ ○ B\n ○ D━━━┻━ ○ C"},
		{"a fan-out source", shared, "A", " ○ A━━━┳━ ○ B\n ○ D───┻━ ○ C"},
		{"a target", shared, "B", " ○ A━━━┳━ ○ B\n ○ D───┴─ ○ C"},
		{"a long edge", long, "D", " ○ A───── ○ B─┳━ ○ C\n ○ D━━━━━━━━━━┛"},
		// A crossing shows A's vertical, so B's line passes under it unlit.
		{"under a crossing", crossing, "B", " ○ A─┬─── ○ D\n ○ B━│━━━ ○ C\n     └─── ○ E"},
		{"over a crossing", crossing, "E", " ○ A━┳─── ○ D\n ○ B─┃─── ○ C\n     ┗━━━ ○ E"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := litDrawn(tc.g, tc.sel); got != tc.want {
				t.Fatalf("drawn\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

func TestGraphBreaksACycleAtItsClosingEdge(t *testing.T) {
	issues := []issue{gi("A", "C"), gi("B", "A"), gi("C", "B")}
	g := buildGraph(issues, "p1")
	if got := drawn(g); got != chain {
		t.Fatalf("drawn\n%s\nwant\n%s", got, chain)
	}
	// The dropped edge is only not drawn: A still names C as its blocker.
	if b := issues[0].blockers(); len(b) != 1 || b[0].Identifier != "C" {
		t.Fatalf("A's blockers %v", b)
	}
}

// Every issue of the project shows once: in the graph when a blocks
// relation ties it to another shown issue, else in the Not linked list. A
// done blocker is a node; a canceled one ties nothing; an open blocker from
// elsewhere is a node at the left.
func TestGraphShowsEveryIssueOnce(t *testing.T) {
	done := gi("D")
	done.State.Type = "completed"
	canceledOnly := gi("B")
	canceledOnly.InverseRelations.Nodes = []relation{{Type: "blocks", Issue: linkedIssue{ID: "Z", State: workflowState{Type: "canceled"}}}}
	related := gi("R")
	related.InverseRelations.Nodes = []relation{{Type: "related", Issue: linkedIssue{ID: "D", State: workflowState{Type: "completed"}}}}
	outside := gi("X")
	outside.State.Type = "started"
	outside.Project = &projectRef{ID: "p2"}
	none := gi("N")
	none.Project = nil
	issues := []issue{gi("A", "D", "X"), done, canceledOnly, related, gi("U"), gi("E", "N"), outside, none}

	g := buildGraph(issues, "p1")
	seen := map[string]int{}
	external := map[string]bool{}
	for _, n := range g.Nodes {
		seen[n.Issue.Identifier]++
		external[n.Issue.Identifier] = n.External
		if n.External && n.Level != 0 {
			t.Errorf("%s from elsewhere at level %d, want the left", n.Issue.Identifier, n.Level)
		}
	}
	var unlinked []string
	for _, is := range g.Unlinked {
		seen[is.Identifier]++
		unlinked = append(unlinked, is.Identifier)
	}
	for _, id := range []string{"A", "D", "B", "R", "U", "E", "X", "N"} {
		if seen[id] != 1 {
			t.Errorf("%s shown %d times", id, seen[id])
		}
	}
	if strings.Join(unlinked, " ") != "B R U" {
		t.Errorf("not linked %v", unlinked)
	}
	if !external["X"] || !external["N"] || external["A"] || external["D"] {
		t.Errorf("external %v", external)
	}
}

func TestGraphNodesAreOrderedByLevelThenRow(t *testing.T) {
	g := buildGraph([]issue{gi("D", "C"), gi("C", "A", "B"), gi("B"), gi("A")}, "p1")
	var ids []string
	for _, n := range g.Nodes {
		ids = append(ids, n.Issue.Identifier)
	}
	// The drawing's first line holds the source the order puts first.
	first, second := "A", "B"
	if strings.HasPrefix(drawn(g), " ○ B") {
		first, second = "B", "A"
	}
	if want := []string{first, second, "C", "D"}; !slices.Equal(ids, want) {
		t.Fatalf("order %v, want %v, drawn\n%s", ids, want, drawn(g))
	}
}

// The legend only works when each stage has its own colour, in every theme.
func TestStagesHaveDistinctColoursInEveryTheme(t *testing.T) {
	t.Cleanup(func() { useTheme(nil) })
	for name := range herdrPalettes {
		withHerdrConfig(t, "[theme]\nname = \""+name+"\"\n")
		p := herdrTheme(true)
		useTheme(&p)
		seen := map[lipgloss.TerminalColor]string{}
		for s, stageName := range stageNames {
			c := stage(s).style().GetForeground()
			if other, ok := seen[c]; ok {
				t.Errorf("%s: %s and %s are both %v", name, other, stageName, c)
			}
			seen[c] = stageName
		}
	}
}

func TestStageIsTheLeastAdvancedLivePR(t *testing.T) {
	pr := func(status string) attachment {
		a := attachment{URL: "https://github.com/o/r/pull/1"}
		a.Metadata.Status = status
		return a
	}
	commit := attachment{URL: "https://github.com/o/r/commit/abc"}
	commit.Metadata.Status = "open"
	comment := attachment{URL: "https://github.com/o/r/pull/1#issuecomment-2"}
	for _, tc := range []struct {
		name  string
		state string
		links []attachment
		want  stage
	}{
		{"todo", "unstarted", nil, stageNotStarted},
		{"backlog", "backlog", nil, stageNotStarted},
		{"triage", "triage", nil, stageNotStarted},
		{"started", "started", nil, stageInProgress},
		{"closed PRs are ignored", "started", []attachment{pr("closed")}, stageInProgress},
		{"commits and comments aren't PRs", "started", []attachment{commit, comment}, stageInProgress},
		{"draft", "started", []attachment{pr("draft")}, stagePROpen},
		{"in review", "started", []attachment{pr("inReview")}, stagePROpen},
		{"least advanced wins", "started", []attachment{pr("merged"), pr("open")}, stagePROpen},
		{"all live merged", "started", []attachment{pr("merged"), pr("merged"), pr("closed")}, stagePRMerged},
		{"a PR beats not started", "unstarted", []attachment{pr("open")}, stagePROpen},
		{"done wins", "completed", []attachment{pr("open")}, stageDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			is := gi("A")
			is.State.Type = tc.state
			is.Attachments.Nodes = tc.links
			if got := is.stage(); got != tc.want {
				t.Fatalf("stage %v, want %v", got, tc.want)
			}
		})
	}
}
