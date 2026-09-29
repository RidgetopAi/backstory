package pi

import "sort"

// node is one treeLine plus its children, keyed by id during buildTree and
// discarded once walkOrder has produced its linear order.
type node struct {
	line     treeLine
	children []*node
}

// walkOrder reconstructs Pi's id/parentId tree from lines (in whatever
// order the file happened to hand them to us — never trusted) and returns
// every reachable line in a single deterministic pre-order walk from the
// type=="session" root: at each node, children are sorted by
// (Timestamp, ID) before recursing, so a branch (two children of one
// assistant message — an edit-and-resend or a regenerated reply) always
// visits in the same order no matter how the source file's lines were
// shuffled. unreachable counts lines whose parentId names an id this file
// never declared (or, for everything but the root, an empty parentId) —
// never silently included in order, since a caller cannot place them
// anywhere it would be safe to say happened before or after a real node.
//
// order is nil when lines contains no type=="session" line: there is
// nothing to root a walk on, and this importer must never guess a root by
// falling back to file order or the directory slug.
func walkOrder(lines []treeLine) (order []treeLine, unreachable int) {
	byID := make(map[string]*node, len(lines))
	for i := range lines {
		byID[lines[i].ID] = &node{line: lines[i]}
	}

	var root *node
	for i := range lines {
		l := lines[i]
		n := byID[l.ID]
		if l.Type == "session" && root == nil {
			root = n
			continue
		}
		p, ok := byID[l.ParentID]
		if l.ParentID == "" || !ok {
			unreachable++
			continue
		}
		p.children = append(p.children, n)
	}
	if root == nil {
		return nil, unreachable
	}

	for _, n := range byID {
		children := n.children
		sort.Slice(children, func(i, j int) bool {
			a, b := children[i].line, children[j].line
			if !a.Timestamp.Equal(b.Timestamp) {
				return a.Timestamp.Before(b.Timestamp)
			}
			return a.ID < b.ID
		})
	}

	visited := make(map[string]bool, len(byID))
	var visit func(*node)
	visit = func(n *node) {
		if visited[n.line.ID] {
			return
		}
		visited[n.line.ID] = true
		order = append(order, n.line)
		for _, c := range n.children {
			visit(c)
		}
	}
	visit(root)
	return order, unreachable
}
