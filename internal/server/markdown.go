package server

import (
	"bytes"
	"fmt"
	htmlescape "html"
	"html/template"
	"regexp"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
)

var (
	mdRenderer = goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			goldmarkhtml.WithHardWraps(),
			goldmarkhtml.WithXHTML(),
		),
	)

	mdSanitizer = newMarkdownSanitizer()
)

func newMarkdownSanitizer() *bluemonday.Policy {
	// Build from scratch instead of UGCPolicy so <img> stays out — remote images
	// would let poisoned markdown beacon back to its origin, against the
	// "artifact text is data, not instruction" principle. Data-URI images can
	// be reintroduced later behind a stricter allowlist.
	policy := bluemonday.NewPolicy()
	policy.AllowStandardURLs()
	policy.AllowURLSchemes("http", "https", "mailto")
	policy.RequireNoFollowOnLinks(true)
	policy.RequireParseableURLs(true)

	policy.AllowElements("p", "br", "hr", "div", "span", "blockquote",
		"h1", "h2", "h3", "h4", "h5", "h6",
		"strong", "em", "b", "i", "u", "s", "del", "ins", "sub", "sup",
		"ul", "ol", "li",
		"code", "pre", "kbd", "samp", "var",
		"table", "thead", "tbody", "tr", "th", "td", "caption",
		"abbr", "cite", "dfn", "mark", "small", "time", "q",
	)
	policy.AllowAttrs("id").Globally()
	policy.AllowAttrs("href").OnElements("a")
	policy.AllowAttrs("title").OnElements("a", "abbr")
	policy.AllowAttrs("colspan", "rowspan").OnElements("th", "td")
	policy.AllowAttrs("class").Matching(regexp.MustCompile(`^[a-zA-Z0-9_\- ]+$`)).Globally()
	policy.AllowElements("a")
	return policy
}

const mdIframeStyles = `
:root { color-scheme: light dark; }
body {
  margin: 0;
  padding: 16px;
  background: #fff;
  color: #202124;
  font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  line-height: 1.55;
  font-size: 0.95rem;
}
@media (prefers-color-scheme: dark) {
  body { background: #1e2127; color: #e6e7ea; }
  a { color: #6dc7b7; }
  code, pre { background: #232730; }
  blockquote { color: #9aa0a8; border-color: #2c2f36; }
  th, td { border-color: #2c2f36; }
}
h1, h2, h3, h4 { line-height: 1.25; margin-top: 1.4em; }
h1 { font-size: 1.4rem; } h2 { font-size: 1.2rem; } h3 { font-size: 1.05rem; }
p, ul, ol, blockquote, pre, table { margin: 0.7em 0; }
ul, ol { padding-left: 1.4em; }
blockquote { border-left: 3px solid #d9d9d2; padding-left: 12px; color: #62656a; margin-left: 0; }
code { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 0.85em; padding: 1px 5px; background: #fafaf6; border-radius: 4px; }
pre { background: #fafaf6; padding: 12px; border-radius: 6px; overflow-x: auto; }
pre code { background: transparent; padding: 0; }
table { border-collapse: collapse; }
th, td { border: 1px solid #d9d9d2; padding: 6px 10px; text-align: left; }
img { max-width: 100%; height: auto; }
a { color: #1f6f63; }
`

func renderMarkdown(source string) template.HTML {
	if source == "" {
		return ""
	}
	var rendered bytes.Buffer
	if err := mdRenderer.Convert([]byte(source), &rendered); err != nil {
		return template.HTML(`<p class="error">Markdown failed to render.</p>`)
	}
	clean := mdSanitizer.SanitizeBytes(rendered.Bytes())
	doc := fmt.Sprintf(
		`<!doctype html><html><head><meta charset="utf-8"><style>%s</style></head><body>%s</body></html>`,
		mdIframeStyles,
		clean,
	)
	iframe := fmt.Sprintf(
		`<iframe class="preview-md" sandbox srcdoc="%s" title="Rendered markdown"></iframe>`,
		htmlescape.EscapeString(doc),
	)
	return template.HTML(iframe)
}
