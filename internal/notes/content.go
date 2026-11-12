package notes

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
	nethtml "golang.org/x/net/html"
)

var managedImage = regexp.MustCompile(`^/media/img-[a-f0-9]{32}\.(jpg|png|gif|webp|avif|heic|bmp)$`)

var contentPolicy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "div", "br", "strong", "b", "em", "i", "u", "s", "strike", "del", "blockquote", "pre", "code", "ul", "ol", "li", "h1", "h2", "h3", "h4", "h5", "h6", "hr", "a", "img", "table", "thead", "tbody", "tr", "th", "td", "input")
	p.AllowAttrs("href", "title").OnElements("a")
	p.AllowAttrs("src", "alt", "title").OnElements("img")
	p.AllowAttrs("start").Matching(regexp.MustCompile(`^-?\d{1,9}$`)).OnElements("ol")
	p.AllowAttrs("align").Matching(regexp.MustCompile(`^(left|center|right|justify)$`)).OnElements("p", "div", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "li")
	p.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	p.AllowAttrs("checked", "disabled").OnElements("input")
	p.AllowURLSchemes("https", "http", "mailto")
	p.AllowRelativeURLs(true)
	p.RequireNoFollowOnLinks(true)
	return p
}()

var markdownParser = goldmark.New(goldmark.WithExtensions(extension.GFM), goldmark.WithRendererOptions(html.WithUnsafe(), html.WithHardWraps()))

// HTML and Markdown are untrusted input even when sent by our own editor.
// Keep a canonical, safe HTML representation and derive plain text for summaries.
func normalizeContent(n Note) Note {
	original := n
	if n.BodyFormat == "markdown" {
		var rendered bytes.Buffer
		if err := markdownParser.Convert([]byte(n.BodyMarkdown), &rendered); err != nil {
			return original
		}
		n.BodyHTML = rendered.String()
	} else {
		n.BodyFormat = ""
		n.BodyMarkdown = ""
	}
	if n.BodyHTML == "" && n.BodyFormat != "markdown" {
		return n // Legacy plain text is deliberately not interpreted as Markdown.
	}
	n.BodyHTML = contentPolicy.Sanitize(n.BodyHTML)
	doc, err := nethtml.Parse(strings.NewReader(n.BodyHTML))
	if err != nil {
		return original
	}
	seen := make(map[string]bool)
	for _, url := range n.Images {
		seen[url] = true
	}
	var body *nethtml.Node
	var walk func(*nethtml.Node)
	walk = func(node *nethtml.Node) {
		if node.Type == nethtml.ElementNode {
			if node.Data == "body" {
				body = node
			}
			if node.Data == "img" {
				for _, attr := range node.Attr {
					if attr.Key == "src" && managedImage.MatchString(attr.Val) && !seen[attr.Val] {
						n.Images = append(n.Images, attr.Val)
						seen[attr.Val] = true
					}
				}
			}
			if node.Data == "input" {
				// Stored HTML is inert; the editor activates only task-list controls.
				attrs := node.Attr[:0]
				for _, attr := range node.Attr {
					if attr.Key != "disabled" && attr.Key != "type" {
						attrs = append(attrs, attr)
					}
				}
				node.Attr = append(attrs, nethtml.Attribute{Key: "disabled", Val: ""}, nethtml.Attribute{Key: "type", Val: "checkbox"})
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if body != nil {
		var safe bytes.Buffer
		for child := body.FirstChild; child != nil; child = child.NextSibling {
			_ = nethtml.Render(&safe, child)
		}
		n.BodyHTML = safe.String()
		n.Body = strings.TrimSpace(contentText(body))
	}
	return n
}

func contentText(node *nethtml.Node) string {
	if node.Type == nethtml.TextNode {
		return node.Data
	}
	if node.Type == nethtml.ElementNode {
		if node.Data == "br" {
			return "\n"
		}
		if node.Data == "img" {
			var src, alt string
			for _, attr := range node.Attr {
				if attr.Key == "src" {
					src = attr.Val
				}
				if attr.Key == "alt" {
					alt = attr.Val
				}
			}
			if managedImage.MatchString(src) {
				return "[[image:" + src + "]]"
			}
			if src != "" {
				return "[图片" + strings.TrimSpace(" "+alt) + "]"
			}
		}
	}
	var text strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		text.WriteString(contentText(child))
	}
	if node.Type == nethtml.ElementNode {
		switch node.Data {
		case "p", "div", "blockquote", "pre", "ul", "ol", "li", "h1", "h2", "h3", "h4", "h5", "h6", "tr", "hr":
			text.WriteString("\n\n")
		case "th", "td":
			text.WriteString("\t")
		}
	}
	return text.String()
}
