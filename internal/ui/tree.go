package ui

import (
	"os"
	"sort"
	"strings"
)

// Sync markers, per concept.md "Sync output". They appear in sync output
// only.
const (
	MarkerSynced          = "✓"
	MarkerFailed          = "✗"
	MarkerRemovedUpstream = "⊘"
)

// Tree guides.
const (
	guideBranch = "├── "
	guideLast   = "└── "
	guidePipe   = "│   "
	guideBlank  = "    "
)

// treeMarkerColor is the tone of each marker in a tree label; a marker
// without an entry stays plain.
var treeMarkerColor = map[string]string{
	MarkerProblem: errorTone,
}

// TreeNode is one top-level node of a tree; it never collapses. Paths are
// slash paths below it, each rendered as a leaf carrying LeafMarker.
type TreeNode struct {
	Name       string
	Marker     string
	Dim        bool
	Paths      []string
	LeafMarker string
}

// trieNode is one line below a top-level node.
type trieNode struct {
	name     string
	children []*trieNode
}

// Tree renders nodes under a result line, per concept.md "Holding a
// namespace": below each node, a folder with a single child collapses onto
// one line with it. Guides are dim, folders bold blue.
func Tree(nodes []TreeNode) string {
	f := os.Stdout
	var b strings.Builder
	for i, n := range nodes {
		last := i == len(nodes)-1
		label := markedLabel(n.Marker, n.Name, f)
		switch {
		case n.Dim:
			label = dimTone(label, f)
		case n.Marker == "" && len(n.Paths) > 0:
			label = folderTone(label, f)
		}
		writeTreeLine(&b, "", last, label, f)
		renderTrie(&b, buildTrie(n.Paths), nextPrefix("", last), n.LeafMarker, f)
	}
	return b.String()
}

// SyncLine formats a repository's result line, per concept.md "Sync
// output": ✓ or ✗, the name, and the step that failed in parentheses.
func SyncLine(ok bool, name, step string) string {
	marker, tone := MarkerSynced, green
	if !ok {
		marker, tone = MarkerFailed, errorTone
	}
	if colorEnabled(os.Stdout) {
		marker = tone + marker + reset
	}
	line := marker + " " + name
	if step != "" {
		line += " (" + step + ")"
	}
	return line + "\n"
}

// Note formats secondary text under a result line, each line indented and
// dim.
func Note(text string) string {
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		b.WriteString(subIndent + dimTone(line, os.Stdout) + "\n")
	}
	return b.String()
}

func buildTrie(paths []string) []*trieNode {
	root := &trieNode{}
	for _, p := range paths {
		at := root
		for _, part := range strings.Split(p, "/") {
			var next *trieNode
			for _, c := range at.children {
				if c.name == part {
					next = c
					break
				}
			}
			if next == nil {
				next = &trieNode{name: part}
				at.children = append(at.children, next)
			}
			at = next
		}
	}
	return collapse(root.children)
}

// collapse merges every folder holding a single child into that child.
func collapse(nodes []*trieNode) []*trieNode {
	for _, n := range nodes {
		for len(n.children) == 1 {
			c := n.children[0]
			n.name += "/" + c.name
			n.children = c.children
		}
		n.children = collapse(n.children)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].name < nodes[j].name })
	return nodes
}

func renderTrie(b *strings.Builder, nodes []*trieNode, prefix, leafMarker string, f *os.File) {
	for i, n := range nodes {
		last := i == len(nodes)-1
		label := folderTone(n.name, f)
		if len(n.children) == 0 {
			label = markedLabel(leafMarker, n.name, f)
		}
		writeTreeLine(b, prefix, last, label, f)
		renderTrie(b, n.children, nextPrefix(prefix, last), leafMarker, f)
	}
}

func writeTreeLine(b *strings.Builder, prefix string, last bool, label string, f *os.File) {
	guide := guideBranch
	if last {
		guide = guideLast
	}
	b.WriteString(subIndent + dimTone(prefix+guide, f) + label + "\n")
}

func nextPrefix(prefix string, last bool) string {
	if last {
		return prefix + guideBlank
	}
	return prefix + guidePipe
}

// markedLabel joins marker and name, in the marker's tone from treeMarkerColor.
// A marker without a tone stays plain.
func markedLabel(marker, name string, f *os.File) string {
	if marker == "" {
		return name
	}
	label := marker + " " + name
	if tone, ok := treeMarkerColor[marker]; ok && colorEnabled(f) {
		return tone + label + reset
	}
	return label
}

// folderTone wraps s in bold blue.
func folderTone(s string, f *os.File) string {
	if !colorEnabled(f) {
		return s
	}
	return bold + blue + s + reset
}
