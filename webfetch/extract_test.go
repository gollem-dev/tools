package webfetch_test

import (
	"testing"

	"github.com/gollem-dev/tools/webfetch"
	"github.com/m-mizutani/gt"
)

// extractContent is exposed via export_test.go for white-box testing.

func TestExtractHTML_BasicText(t *testing.T) {
	html := `<html><body><p>Hello, World!</p></body></html>`
	text, isHTML, err := webfetch.ExtractContent("text/html", []byte(html))
	gt.NoError(t, err)
	gt.Bool(t, isHTML).True()
	gt.String(t, text).Contains("Hello, World!")
}

func TestExtractHTML_Headings(t *testing.T) {
	html := `<html><body><h1>Title</h1><h2>Subtitle</h2></body></html>`
	text, isHTML, err := webfetch.ExtractContent("text/html", []byte(html))
	gt.NoError(t, err)
	gt.Bool(t, isHTML).True()
	gt.String(t, text).Contains("# Title")
	gt.String(t, text).Contains("## Subtitle")
}

func TestExtractHTML_DropScript(t *testing.T) {
	html := `<html><body><p>Visible</p><script>alert("hidden")</script></body></html>`
	text, _, err := webfetch.ExtractContent("text/html", []byte(html))
	gt.NoError(t, err)
	gt.String(t, text).Contains("Visible")
	gt.String(t, text).NotContains("hidden")
	gt.String(t, text).NotContains("alert")
}

func TestExtractHTML_DropStyle(t *testing.T) {
	html := `<html><head><style>.x{display:none}</style></head><body><p>Content</p></body></html>`
	text, _, err := webfetch.ExtractContent("text/html", []byte(html))
	gt.NoError(t, err)
	gt.String(t, text).NotContains("display:none")
	gt.String(t, text).Contains("Content")
}

func TestExtractHTML_DropHiddenAttr(t *testing.T) {
	html := `<html><body><p hidden>Secret</p><p>Visible</p></body></html>`
	text, _, err := webfetch.ExtractContent("text/html", []byte(html))
	gt.NoError(t, err)
	gt.String(t, text).NotContains("Secret")
	gt.String(t, text).Contains("Visible")
}

func TestExtractHTML_DropDisplayNone(t *testing.T) {
	html := `<html><body><p style="display:none">Hidden</p><p>Shown</p></body></html>`
	text, _, err := webfetch.ExtractContent("text/html", []byte(html))
	gt.NoError(t, err)
	gt.String(t, text).NotContains("Hidden")
	gt.String(t, text).Contains("Shown")
}

func TestExtractHTML_Lists(t *testing.T) {
	html := `<html><body><ul><li>One</li><li>Two</li></ul></body></html>`
	text, _, err := webfetch.ExtractContent("text/html", []byte(html))
	gt.NoError(t, err)
	gt.String(t, text).Contains("- One")
	gt.String(t, text).Contains("- Two")
}

func TestExtractHTML_CodeBlock(t *testing.T) {
	html := `<html><body><pre><code>func main() {}</code></pre></body></html>`
	text, _, err := webfetch.ExtractContent("text/html", []byte(html))
	gt.NoError(t, err)
	gt.String(t, text).Contains("```")
	gt.String(t, text).Contains("func main()")
}

func TestExtractHTML_Table(t *testing.T) {
	html := `<html><body><table><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>2</td></tr></table></body></html>`
	text, _, err := webfetch.ExtractContent("text/html", []byte(html))
	gt.NoError(t, err)
	gt.String(t, text).Contains("A")
	gt.String(t, text).Contains("B")
	gt.String(t, text).Contains("|")
}

func TestExtractHTML_DropComments(t *testing.T) {
	html := `<html><body><!-- this is a comment -->Visible</body></html>`
	text, _, err := webfetch.ExtractContent("text/html", []byte(html))
	gt.NoError(t, err)
	gt.String(t, text).NotContains("this is a comment")
	gt.String(t, text).Contains("Visible")
}

func TestExtractHTML_WithCharset(t *testing.T) {
	// Content-Type with charset parameter must still be recognized as HTML.
	html := `<html><body><p>Charset test</p></body></html>`
	text, isHTML, err := webfetch.ExtractContent("text/html; charset=utf-8", []byte(html))
	gt.NoError(t, err)
	gt.Bool(t, isHTML).True()
	gt.String(t, text).Contains("Charset test")
}

func TestExtractPlainText(t *testing.T) {
	body := []byte("plain text content")
	text, isHTML, err := webfetch.ExtractContent("text/plain", body)
	gt.NoError(t, err)
	gt.Bool(t, isHTML).False()
	gt.String(t, text).Equal("plain text content")
}

func TestExtractJSON(t *testing.T) {
	body := []byte(`{"key":"value"}`)
	text, isHTML, err := webfetch.ExtractContent("application/json", body)
	gt.NoError(t, err)
	gt.Bool(t, isHTML).False()
	gt.String(t, text).Equal(`{"key":"value"}`)
}

func TestExtractUnsupportedContentType(t *testing.T) {
	body := []byte{0x00, 0x01, 0x02}
	_, _, err := webfetch.ExtractContent("application/octet-stream", body)
	gt.Error(t, err).Contains("unsupported content type")
}
