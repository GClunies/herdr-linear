package main

import (
	"cmp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// A project's issues as a graph: left (first) to right (last) by "blocks",
// each issue one column right of its latest blocker. Nodes are a status
// glyph and an identifier, coloured by stage. Only the edges no longer path
// implies are drawn; an edge that closes a cycle is dropped so the rest can
// be laid out. Either way the issue still names every blocker
// (issue.blockers), for the screen to say.

// ── stages ────────────────────────────────────────────────────────────────────

// stage is how far an issue's work has got, as the graph colours it.
type stage int

const (
	stageNotStarted stage = iota // triage, backlog or todo, with no live PR
	stageInProgress              // started, with no live PR
	stagePROpen                  // a live PR not merged yet
	stagePRMerged                // every live PR merged, the issue not done
	stageDone
)

var stageNames = [...]string{"Not started", "In progress, no PR", "PR open", "PR merged", "Done"}

// stage is done for a done issue whatever its PRs; else its least advanced
// live pull request's (a closed one is dead); else its state's. Only a
// project's issues carry their links (projectIssues).
func (is issue) stage() stage {
	if is.State.Type == "completed" {
		return stageDone
	}
	live, merged := 0, 0
	for _, a := range is.Attachments.Nodes {
		status := a.Metadata.Status
		if !strings.Contains(a.URL, "/pull/") || status == "" || status == "closed" {
			continue
		}
		live++
		if status == "merged" {
			merged++
		}
	}
	switch {
	case live > merged:
		return stagePROpen
	case live > 0:
		return stagePRMerged
	case is.State.Type == "started":
		return stageInProgress
	}
	return stageNotStarted
}

// style is the stage's colour. Done is blue, or teal where the theme's blue
// is its green (rose-pine and rose-pine-dawn), so the legend keeps five.
func (s stage) style() lipgloss.Style {
	done := look.Blue
	if done == look.Green {
		done = look.Teal
	}
	return lipgloss.NewStyle().Foreground([...]lipgloss.TerminalColor{look.Dim, look.Yellow, look.Green, look.Mauve, done}[s])
}

// stageLegend names each stage in its colour, for under the graph.
func stageLegend() string {
	parts := make([]string, len(stageNames))
	for s, name := range stageNames {
		parts[s] = stage(s).style().Render("●") + " " + styleDim.Render(name)
	}
	return strings.Join(parts, "   ")
}

// nodeLabel draws a node as render's label wants it: its nodeText in its
// stage's colour, or dim for a blocker from elsewhere, the identifier
// underlined when the issue has a worktree.
func nodeLabel(n *graphNode, worktree bool) string {
	s := n.Issue.stage().style()
	if n.External {
		s = styleDim
	}
	id := s
	if worktree {
		id = id.Underline(true)
	}
	return s.Render(stateIcon(n.Issue.State)) + " " + id.Render(n.Issue.Identifier)
}

// ── layout ────────────────────────────────────────────────────────────────────

// graphNode is one issue in the graph, where the layout put it.
type graphNode struct {
	Issue    *issue // into the issues buildGraph was given
	External bool   // an open blocker from another project, or from none
	Level    int    // its column, from 0 at the left
	Row      int    // its line of the drawing, from 0 at the top
	X        int    // the cell of its line its label starts at
	Width    int    // the cells its label takes
}

// projectGraph is a project's issues laid out by buildGraph.
type projectGraph struct {
	Nodes []graphNode // by Level, then Row
	// Unlinked are the project's issues that no "blocks" relation ties to
	// another issue shown, in the order given.
	Unlinked      []*issue
	Width, Height int        // the drawing's size, in cells and lines
	cells         [][]rune   // the drawing's lines, blank where labels go
	carries       [][][]edge // the edges each cell's line is part of
}

// edge is one drawn "blocks" relation: from blocks to.
type edge struct{ from, to *issue }

// vertex is a node of the layout: an issue, or a pass-through (issue -1)
// that carries a long edge, via, across a column.
type vertex struct {
	issue      int
	via        edge
	level, row int
	in, out    []int
}

// buildGraph lays out issues as projectIssues returns them: the project's
// own, and the open blockers from elsewhere (an issue whose project isn't
// projectID). A blocker from elsewhere is a source only, at the left: what
// blocks it is another project's business. Every issue of the project is
// either a node or Unlinked, once; a blocker from elsewhere is a node.
func buildGraph(issues []issue, projectID string) projectGraph {
	external := func(i int) bool { p := issues[i].Project; return p == nil || p.ID != projectID }
	index := make(map[string]int, len(issues))
	for i := range issues {
		index[issues[i].ID] = i
	}
	succ := make([][]int, len(issues)) // the issues each one blocks
	for v := range issues {
		if external(v) {
			continue
		}
		for _, b := range issues[v].blockers() {
			if u, ok := index[b.ID]; ok {
				succ[u] = append(succ[u], v)
			}
		}
	}
	breakCycles(succ)
	reduce(succ)
	pred := make([][]int, len(issues))
	for u, vs := range succ {
		for _, v := range vs {
			pred[v] = append(pred[v], u)
		}
	}

	var g projectGraph
	var vs []vertex
	at := make([]int, len(issues)) // an issue's vertex
	for i := range issues {
		switch {
		case len(succ[i]) > 0 || len(pred[i]) > 0:
			at[i] = len(vs)
			vs = append(vs, vertex{issue: i})
		case !external(i):
			g.Unlinked = append(g.Unlinked, &issues[i])
		}
	}
	if len(vs) == 0 {
		return g
	}

	// Each issue one column right of its latest blocker: the longest path.
	known := make([]bool, len(issues))
	var levelOf func(i int) int
	levelOf = func(i int) int {
		if !known[i] {
			for _, p := range pred[i] {
				vs[at[i]].level = max(vs[at[i]].level, levelOf(p)+1)
			}
			known[i] = true
		}
		return vs[at[i]].level
	}
	last := 0
	for k := range vs {
		last = max(last, levelOf(vs[k].issue))
	}
	link := func(a, b int) {
		vs[a].out = append(vs[a].out, b)
		vs[b].in = append(vs[b].in, a)
	}
	// An edge across several columns passes through each one between.
	for k := range len(vs) {
		u := vs[k].issue
		for _, v := range succ[u] {
			prev := k
			for l := vs[k].level + 1; l < vs[at[v]].level; l++ {
				vs = append(vs, vertex{issue: -1, via: edge{&issues[u], &issues[v]}, level: l})
				link(prev, len(vs)-1)
				prev = len(vs) - 1
			}
			link(prev, at[v])
		}
	}
	cols := make([][]int, last+1)
	for k := range vs {
		cols[vs[k].level] = append(cols[vs[k].level], k)
	}
	placeRows(vs, cols)

	// Each gap between columns has a vertical channel per source, or per
	// target where several edges meet one (fan-in).
	type channel struct {
		toTarget bool
		edges    [][2]int
		lo, hi   int
	}
	nodeW := 0
	for k := range vs {
		if i := vs[k].issue; i >= 0 {
			nodeW = max(nodeW, utf8.RuneCountInString(nodeText(issues[i])))
		}
		g.Height = max(g.Height, vs[k].row+1)
	}
	gaps := make([][]*channel, last)
	colX := []int{1}
	for l := range last {
		var edges [][2]int
		into := map[int]int{}
		for _, u := range cols[l] {
			for _, v := range vs[u].out {
				edges = append(edges, [2]int{u, v})
				into[v]++
			}
		}
		byKey := map[[2]int]*channel{}
		for _, e := range edges {
			key := [2]int{0, e[0]}
			if into[e[1]] > 1 {
				key = [2]int{1, e[1]}
			}
			ch := byKey[key]
			if ch == nil {
				ch = &channel{toTarget: key[0] == 1, lo: vs[e[0]].row, hi: vs[e[0]].row}
				byKey[key] = ch
				gaps[l] = append(gaps[l], ch)
			}
			ch.edges = append(ch.edges, e)
			for _, k := range e {
				ch.lo, ch.hi = min(ch.lo, vs[k].row), max(ch.hi, vs[k].row)
			}
		}
		slices.SortStableFunc(gaps[l], func(a, b *channel) int {
			if a.toTarget != b.toTarget {
				if a.toTarget {
					return 1
				}
				return -1
			}
			return cmp.Compare(a.lo, b.lo)
		})
		colX = append(colX, colX[l]+nodeW+2+2*max(1, len(gaps[l])))
	}
	g.Width = colX[last] + nodeW

	bits := make([][]uint8, g.Height)
	g.carries = make([][][]edge, g.Height) // the vertical lines' edges, for now
	across := make([][][]edge, g.Height)   // the horizontal lines' edges
	for r := range bits {
		bits[r] = make([]uint8, g.Width+1)
		g.carries[r] = make([][]edge, g.Width+1)
		across[r] = make([][]edge, g.Width+1)
	}
	carry := func(grid [][][]edge, e edge, r0, r1, c0, c1 int) {
		for r := r0; r <= r1; r++ {
			for c := c0; c <= c1; c++ {
				grid[r][c] = append(grid[r][c], e)
			}
		}
	}
	hline := func(e edge, r, a, b int) {
		carry(across, e, r, r, a, b)
		for c := a; c <= b; c++ {
			if c > a {
				bits[r][c] |= lineLeft
			}
			if c < b {
				bits[r][c] |= lineRight
			}
		}
	}
	vline := func(c, a, b int) {
		for r := a; r <= b; r++ {
			if r > a {
				bits[r][c] |= lineUp
			}
			if r < b {
				bits[r][c] |= lineDown
			}
		}
	}
	end := make([]int, len(vs)) // where a vertex's label ends, its lines start
	for k, x := range vs {
		c := colX[x.level]
		if x.issue < 0 {
			hline(x.via, x.row, c-1, c+nodeW)
			end[k] = c + nodeW
			continue
		}
		is := &issues[x.issue]
		n := graphNode{Issue: is, External: external(x.issue), Level: x.level, Row: x.row, X: c, Width: utf8.RuneCountInString(nodeText(*is))}
		end[k] = c + n.Width
		g.Nodes = append(g.Nodes, n)
	}
	for l, chans := range gaps {
		for n, ch := range chans {
			cx := colX[l] + nodeW + 1 + 2*n
			vline(cx, ch.lo, ch.hi)
			for r := ch.lo; r <= ch.hi; r++ {
				if !slices.ContainsFunc(ch.edges, func(e [2]int) bool { return vs[e[0]].row == r || vs[e[1]].row == r }) {
					bits[r][cx] |= lineCrossed // another edge's line crosses here: the channel stays whole
				}
			}
			for _, e := range ch.edges {
				u, v := vs[e[0]], vs[e[1]]
				var drawn edge
				switch {
				case u.issue < 0:
					drawn = u.via
				case v.issue < 0:
					drawn = v.via
				default:
					drawn = edge{&issues[u.issue], &issues[v.issue]}
				}
				hline(drawn, u.row, end[e[0]], cx)
				carry(g.carries, drawn, min(u.row, v.row), max(u.row, v.row), cx, cx)
				to := colX[l+1] - 2 // a space before the label
				if v.issue < 0 {
					to = colX[l+1]
				}
				hline(drawn, v.row, cx, to)
			}
		}
	}
	// A crossing shows only its vertical, so only the vertical's edges light it.
	for r, row := range across {
		for c, es := range row {
			if bits[r][c]&lineCrossed == 0 {
				g.carries[r][c] = append(g.carries[r][c], es...)
			}
		}
	}
	g.cells = make([][]rune, g.Height)
	for r, row := range bits {
		g.cells[r] = make([]rune, len(row))
		for c, b := range row {
			g.cells[r][c] = glyph(b)
		}
	}
	slices.SortStableFunc(g.Nodes, func(a, b graphNode) int {
		return cmp.Or(cmp.Compare(a.Level, b.Level), cmp.Compare(a.Row, b.Row))
	})
	return g
}

// nodeText is an issue's node, unstyled.
func nodeText(is issue) string { return stateIcon(is.State) + " " + is.Identifier }

// breakCycles drops each edge that closes a cycle (a back edge of a depth
// first walk), leaving a graph that can be laid out in columns.
func breakCycles(succ [][]int) {
	state := make([]int8, len(succ)) // 0 unseen, 1 on the walk, 2 done
	var walk func(u int)
	walk = func(u int) {
		state[u] = 1
		kept := succ[u][:0]
		for _, v := range succ[u] {
			if state[v] == 1 {
				continue
			}
			kept = append(kept, v)
			if state[v] == 0 {
				walk(v)
			}
		}
		succ[u] = kept
		state[u] = 2
	}
	for u := range succ {
		if state[u] == 0 {
			walk(u)
		}
	}
}

// reduce drops each edge u → v that a longer path from u to v implies.
func reduce(succ [][]int) {
	for u := range succ {
		for i := 0; i < len(succ[u]); {
			if reachesAround(succ, u, succ[u][i]) {
				succ[u] = slices.Delete(succ[u], i, i+1)
			} else {
				i++
			}
		}
	}
}

// reachesAround says whether v can be reached from u other than by u → v.
func reachesAround(succ [][]int, u, v int) bool {
	seen := make([]bool, len(succ))
	var stack []int
	for _, w := range succ[u] {
		if w != v {
			stack = append(stack, w)
		}
	}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if x == v {
			return true
		}
		if !seen[x] {
			seen[x] = true
			stack = append(stack, succ[x]...)
		}
	}
	return false
}

// placeRows gives each vertex a row, column by column: as near its
// blockers' median row as it can get. Side by side on a row reads as
// connected, so a vertex only takes a row whose left neighbour, if any,
// leads into it.
func placeRows(vs []vertex, cols [][]int) {
	taken := map[[2]int]int{} // level, row → vertex
	for l, col := range cols {
		want := make(map[int]int, len(col))
		for _, k := range col {
			rows := make([]int, len(vs[k].in))
			for i, p := range vs[k].in {
				rows[i] = vs[p].row
			}
			slices.Sort(rows)
			if len(rows) > 0 {
				want[k] = rows[(len(rows)-1)/2]
			}
		}
		slices.SortStableFunc(col, func(a, b int) int { return cmp.Compare(want[a], want[b]) })
		for _, k := range col {
			free := func(r int) bool {
				if _, ok := taken[[2]int{l, r}]; r < 0 || ok {
					return false
				}
				west, ok := taken[[2]int{l - 1, r}]
				return !ok || slices.Contains(vs[k].in, west)
			}
			w := want[k]
			r := w
			for d := 1; !free(r); d++ {
				if r = w + d; !free(r) {
					r = w - d
				}
			}
			vs[k].row = r
			taken[[2]int{l, r}] = k
		}
	}
}

// ── drawing ───────────────────────────────────────────────────────────────────

// A cell's line runs up, down, left and right from its middle; lineCrossed
// draws it as the vertical of a crossing.
const (
	lineUp uint8 = 1 << iota
	lineDown
	lineLeft
	lineRight
	lineCrossed
)

func glyph(b uint8) rune {
	if b&lineCrossed != 0 {
		return '│'
	}
	switch b {
	case lineUp, lineDown, lineUp | lineDown:
		return '│'
	case lineLeft, lineRight, lineLeft | lineRight:
		return '─'
	case lineUp | lineLeft:
		return '┘'
	case lineUp | lineRight:
		return '└'
	case lineDown | lineLeft:
		return '┐'
	case lineDown | lineRight:
		return '┌'
	case lineUp | lineDown | lineLeft:
		return '┤'
	case lineUp | lineDown | lineRight:
		return '├'
	case lineDown | lineLeft | lineRight:
		return '┬'
	case lineUp | lineLeft | lineRight:
		return '┴'
	case lineUp | lineDown | lineLeft | lineRight:
		return '┼'
	}
	return ' '
}

// render draws the graph, a string per line, Height of them, each at most
// Width cells and trimmed at the right. label draws a node; it must take
// the node's Width cells (nodeLabel does). line styles the connectors, lit
// where they carry an edge from or to sel (nil lights none), so that one
// issue's edges stand out where lines share a junction. Cropping to a view
// is the caller's, with ansi.Cut: a node's X and Width say where it lands.
func (g projectGraph) render(label func(*graphNode) string, line func(seg string, lit bool) string, sel *issue) []string {
	byRow := make([][]*graphNode, g.Height)
	for i := range g.Nodes {
		n := &g.Nodes[i]
		byRow[n.Row] = append(byRow[n.Row], n) // left to right: Nodes go by level
	}
	out := make([]string, g.Height)
	for r, cells := range g.cells {
		// kind is a cell's look: 0 blank, 1 a line, 2 a lit line.
		kind := func(c int) int {
			switch {
			case cells[c] == ' ':
				return 0
			case sel != nil && slices.ContainsFunc(g.carries[r][c], func(e edge) bool { return e.from == sel || e.to == sel }):
				return 2
			}
			return 1
		}
		var b strings.Builder
		lines := func(from, to int) {
			for i := from; i < to; {
				k, j := kind(i), i+1
				for j < to && kind(j) == k {
					j++
				}
				if seg := string(cells[i:j]); k == 0 {
					b.WriteString(seg)
				} else {
					b.WriteString(line(seg, k == 2))
				}
				i = j
			}
		}
		c := 0
		for _, n := range byRow[r] {
			lines(c, n.X)
			b.WriteString(label(n))
			c = n.X + n.Width
		}
		lines(c, len(cells))
		out[r] = strings.TrimRight(b.String(), " ")
	}
	return out
}
