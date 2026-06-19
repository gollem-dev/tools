package jira

import (
	"encoding/json"
	"strconv"
	"strings"
)

// adfNode is one node of an Atlassian Document Format (ADF) tree. ADF is a
// structured JSON document, so it is decoded into this typed shape and walked
// recursively — there is no regex-based text munging anywhere in the converter.
//
// Only the attributes actually used by the renderer are decoded; everything else
// is ignored. Unknown node types fall through to a generic "render the children"
// path so the document degrades gracefully instead of dropping text.
type adfNode struct {
	Type    string    `json:"type"`
	Text    string    `json:"text"`
	Content []adfNode `json:"content"`
	Marks   []adfMark `json:"marks"`
	Attrs   adfAttrs  `json:"attrs"`
}

type adfMark struct {
	Type  string `json:"type"`
	Attrs struct {
		Href string `json:"href"`
	} `json:"attrs"`
}

// adfAttrs collects the node attributes the renderer consults across the various
// node types (headings, code blocks, lists, links, mentions, emoji).
type adfAttrs struct {
	Level    int    `json:"level"`    // heading
	Language string `json:"language"` // codeBlock
	Order    int    `json:"order"`    // orderedList start index
	URL      string `json:"url"`      // inlineCard / blockCard
	Text     string `json:"text"`     // mention / emoji fallback
	ShortNm  string `json:"shortName"`
}

// adfToMarkdown converts a raw ADF document (the value of an issue's description
// or comment body) into Markdown. A nil or unparseable document yields an empty
// string rather than an error: a missing description is normal, not a failure.
func adfToMarkdown(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var doc adfNode
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	var b strings.Builder
	renderBlocks(&b, doc.Content, "")
	return strings.TrimRight(b.String(), "\n")
}

// renderBlocks renders a sequence of block-level nodes, separating them with a
// blank line. indent is the prefix applied to nested block content (used by
// list items so wrapped block children line up under the marker).
func renderBlocks(b *strings.Builder, nodes []adfNode, indent string) {
	for i, n := range nodes {
		if i > 0 {
			b.WriteString("\n\n")
		}
		renderBlock(b, n, indent)
	}
}

func renderBlock(b *strings.Builder, n adfNode, indent string) {
	switch n.Type {
	case "paragraph":
		b.WriteString(indent)
		b.WriteString(renderInline(n.Content))

	case "heading":
		level := min(max(n.Attrs.Level, 1), 6)
		b.WriteString(strings.Repeat("#", level))
		b.WriteString(" ")
		b.WriteString(renderInline(n.Content))

	case "bulletList":
		renderList(b, n.Content, indent, false, 0)

	case "orderedList":
		start := n.Attrs.Order
		if start <= 0 {
			start = 1
		}
		renderList(b, n.Content, indent, true, start)

	case "blockquote":
		var inner strings.Builder
		renderBlocks(&inner, n.Content, "")
		for j, line := range strings.Split(inner.String(), "\n") {
			if j > 0 {
				b.WriteString("\n")
			}
			b.WriteString(indent)
			b.WriteString("> ")
			b.WriteString(line)
		}

	case "codeBlock":
		b.WriteString(indent)
		b.WriteString("```")
		b.WriteString(n.Attrs.Language)
		b.WriteString("\n")
		b.WriteString(collectText(n.Content))
		b.WriteString("\n")
		b.WriteString(indent)
		b.WriteString("```")

	case "rule":
		b.WriteString(indent)
		b.WriteString("---")

	default:
		// Unknown block: render inline text if it has leaf content, otherwise
		// recurse into children so nested blocks are still surfaced.
		if hasTextLeaf(n.Content) {
			b.WriteString(indent)
			b.WriteString(renderInline(n.Content))
		} else if len(n.Content) > 0 {
			renderBlocks(b, n.Content, indent)
		} else if n.Text != "" {
			b.WriteString(indent)
			b.WriteString(n.Text)
		}
	}
}

// renderList renders the listItem children of a bullet/ordered list. ordered
// lists number from start; nested lists are indented by two spaces.
func renderList(b *strings.Builder, items []adfNode, indent string, ordered bool, start int) {
	n := 0
	for _, item := range items {
		if item.Type != "listItem" {
			continue
		}
		if n > 0 {
			b.WriteString("\n")
		}
		marker := "- "
		if ordered {
			marker = strconv.Itoa(start+n) + ". "
		}
		b.WriteString(indent)
		b.WriteString(marker)

		// A listItem holds block children (typically one paragraph, plus possibly
		// nested lists). The first paragraph sits on the marker line; subsequent
		// blocks are indented to align under it.
		childIndent := indent + strings.Repeat(" ", len(marker))
		for j, child := range item.Content {
			if j > 0 {
				b.WriteString("\n")
			}
			if j == 0 && child.Type == "paragraph" {
				b.WriteString(renderInline(child.Content))
			} else {
				var sub strings.Builder
				renderBlock(&sub, child, childIndent)
				b.WriteString(sub.String())
			}
		}
		n++
	}
}

// renderInline renders a sequence of inline nodes (text, hardBreak, mention,
// emoji, inlineCard) into a single line of Markdown, applying text marks.
func renderInline(nodes []adfNode) string {
	var b strings.Builder
	for _, n := range nodes {
		switch n.Type {
		case "text":
			b.WriteString(applyMarks(n.Text, n.Marks))
		case "hardBreak":
			b.WriteString("\n")
		case "mention":
			b.WriteString(n.Attrs.Text)
		case "emoji":
			if n.Attrs.Text != "" {
				b.WriteString(n.Attrs.Text)
			} else {
				b.WriteString(n.Attrs.ShortNm)
			}
		case "inlineCard", "blockCard":
			if n.Attrs.URL != "" {
				b.WriteString(n.Attrs.URL)
			}
		default:
			// Unknown inline node: surface any nested text rather than dropping it.
			b.WriteString(renderInline(n.Content))
		}
	}
	return b.String()
}

// applyMarks wraps text with the Markdown syntax for its ADF marks. link is
// applied outermost so emphasis stays inside the link label.
func applyMarks(text string, marks []adfMark) string {
	var href string
	emphasis := func(s string) string { return s }
	for _, m := range marks {
		switch m.Type {
		case "strong":
			prev := emphasis
			emphasis = func(s string) string { return "**" + prev(s) + "**" }
		case "em":
			prev := emphasis
			emphasis = func(s string) string { return "*" + prev(s) + "*" }
		case "code":
			prev := emphasis
			emphasis = func(s string) string { return "`" + prev(s) + "`" }
		case "strike":
			prev := emphasis
			emphasis = func(s string) string { return "~~" + prev(s) + "~~" }
		case "link":
			href = m.Attrs.Href
		}
	}
	out := emphasis(text)
	if href != "" {
		return "[" + out + "](" + href + ")"
	}
	return out
}

// collectText concatenates the raw text of a node subtree, used for code blocks
// where marks and structure must not leak into the rendered content.
func collectText(nodes []adfNode) string {
	var b strings.Builder
	for _, n := range nodes {
		b.WriteString(n.Text)
		b.WriteString(collectText(n.Content))
	}
	return b.String()
}

// hasTextLeaf reports whether nodes contain an immediate inline text leaf, used
// to decide how to render an unknown block.
func hasTextLeaf(nodes []adfNode) bool {
	for _, n := range nodes {
		if n.Type == "text" || n.Type == "hardBreak" {
			return true
		}
	}
	return false
}
