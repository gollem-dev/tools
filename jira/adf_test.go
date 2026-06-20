package jira_test

import (
	"testing"

	"github.com/gollem-dev/tools/jira"
	"github.com/m-mizutani/gt"
)

func TestADFToMarkdown(t *testing.T) {
	testCases := map[string]struct {
		adf  string
		want string
	}{
		"empty": {
			adf:  ``,
			want: ``,
		},
		"invalid json": {
			adf:  `{not json`,
			want: ``,
		},
		"paragraph with marks": {
			adf: `{"type":"doc","content":[{"type":"paragraph","content":[
				{"type":"text","text":"plain "},
				{"type":"text","text":"bold","marks":[{"type":"strong"}]},
				{"type":"text","text":" "},
				{"type":"text","text":"italic","marks":[{"type":"em"}]},
				{"type":"text","text":" "},
				{"type":"text","text":"code","marks":[{"type":"code"}]}
			]}]}`,
			want: "plain **bold** *italic* `code`",
		},
		"link": {
			adf: `{"type":"doc","content":[{"type":"paragraph","content":[
				{"type":"text","text":"site","marks":[{"type":"link","attrs":{"href":"https://e.com"}}]}
			]}]}`,
			want: "[site](https://e.com)",
		},
		"link wrapping emphasis": {
			adf: `{"type":"doc","content":[{"type":"paragraph","content":[
				{"type":"text","text":"x","marks":[{"type":"strong"},{"type":"link","attrs":{"href":"u"}}]}
			]}]}`,
			want: "[**x**](u)",
		},
		"headings": {
			adf: `{"type":"doc","content":[
				{"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"H1"}]},
				{"type":"heading","attrs":{"level":3},"content":[{"type":"text","text":"H3"}]}
			]}`,
			want: "# H1\n\n### H3",
		},
		"bullet list": {
			adf: `{"type":"doc","content":[{"type":"bulletList","content":[
				{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"one"}]}]},
				{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"two"}]}]}
			]}]}`,
			want: "- one\n- two",
		},
		"ordered list with start": {
			adf: `{"type":"doc","content":[{"type":"orderedList","attrs":{"order":3},"content":[
				{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]}]},
				{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"b"}]}]}
			]}]}`,
			want: "3. a\n4. b",
		},
		"nested list": {
			adf: `{"type":"doc","content":[{"type":"bulletList","content":[
				{"type":"listItem","content":[
					{"type":"paragraph","content":[{"type":"text","text":"parent"}]},
					{"type":"bulletList","content":[
						{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"child"}]}]}
					]}
				]}
			]}]}`,
			want: "- parent\n  - child",
		},
		"code block": {
			adf: `{"type":"doc","content":[{"type":"codeBlock","attrs":{"language":"go"},
				"content":[{"type":"text","text":"fmt.Println(1)"}]}]}`,
			want: "```go\nfmt.Println(1)\n```",
		},
		"blockquote": {
			adf: `{"type":"doc","content":[{"type":"blockquote","content":[
				{"type":"paragraph","content":[{"type":"text","text":"quoted"}]}
			]}]}`,
			want: "> quoted",
		},
		"rule": {
			adf: `{"type":"doc","content":[
				{"type":"paragraph","content":[{"type":"text","text":"a"}]},
				{"type":"rule"},
				{"type":"paragraph","content":[{"type":"text","text":"b"}]}
			]}`,
			want: "a\n\n---\n\nb",
		},
		"hard break": {
			adf: `{"type":"doc","content":[{"type":"paragraph","content":[
				{"type":"text","text":"a"},{"type":"hardBreak"},{"type":"text","text":"b"}
			]}]}`,
			want: "a\nb",
		},
		"mention and emoji": {
			adf: `{"type":"doc","content":[{"type":"paragraph","content":[
				{"type":"mention","attrs":{"text":"@Alice"}},
				{"type":"text","text":" "},
				{"type":"emoji","attrs":{"text":"😀","shortName":":grinning:"}}
			]}]}`,
			want: "@Alice 😀",
		},
		"unknown block falls through to text": {
			adf: `{"type":"doc","content":[{"type":"panel","content":[
				{"type":"paragraph","content":[{"type":"text","text":"inside panel"}]}
			]}]}`,
			want: "inside panel",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			gt.Equal(t, jira.ADFToMarkdown(tc.adf), tc.want)
		})
	}
}
