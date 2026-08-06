package tyler

import (
	"html"
	"regexp"
	"strings"
)

// toHTML is a small, dependency-free markdown renderer scoped to exactly
// the subset TYLER episode scripts actually use: #/##/### headers, **bold**,
// *italic*, `inline code`, --- rules, ``` fences (used for UI-overlay/scene-
// header blocks), | pipe | tables |, and paragraphs. Not a general-purpose
// markdown library on purpose — see blog/render.go's own "poor man's
// markdown" comment for the precedent; this one just covers more ground
// because TYLER's script format actually needs headers/tables/fences to
// read correctly, where blog posts so far haven't.
func toHTML(body string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var out strings.Builder
	var para []string

	flushPara := func() {
		if len(para) == 0 {
			return
		}
		out.WriteString("<p>")
		out.WriteString(strings.Join(para, "<br>\n"))
		out.WriteString("</p>\n")
		para = nil
	}

	isRule := regexp.MustCompile(`^-{3,}\s*$`)
	isTableRow := regexp.MustCompile(`^\s*\|.*\|\s*$`)
	isTableSep := regexp.MustCompile(`^\s*\|?[\s:|-]+\|?\s*$`)
	isListItem := regexp.MustCompile(`^-\s+(.*)$`)
	isChecklist := regexp.MustCompile(`^\[([ xX])\]\s*(.*)$`)

	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		switch {
		case trimmed == "":
			flushPara()
			i++

		case strings.HasPrefix(trimmed, "```"):
			flushPara()
			var code []string
			i++
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				code = append(code, lines[i])
				i++
			}
			i++ // skip closing fence
			out.WriteString("<pre>")
			out.WriteString(html.EscapeString(strings.Join(code, "\n")))
			out.WriteString("</pre>\n")

		case isRule.MatchString(trimmed):
			flushPara()
			out.WriteString("<hr>\n")
			i++

		case strings.HasPrefix(trimmed, "### "):
			flushPara()
			out.WriteString("<h3>" + inline(trimmed[4:]) + "</h3>\n")
			i++
		case strings.HasPrefix(trimmed, "## "):
			flushPara()
			out.WriteString("<h2>" + inline(trimmed[3:]) + "</h2>\n")
			i++
		case strings.HasPrefix(trimmed, "# "):
			flushPara()
			out.WriteString("<h1>" + inline(trimmed[2:]) + "</h1>\n")
			i++

		case isListItem.MatchString(trimmed):
			flushPara()
			var items []string
			for i < len(lines) {
				t := strings.TrimSpace(lines[i])
				if m := isListItem.FindStringSubmatch(t); m != nil {
					items = append(items, m[1])
					i++
					continue
				}
				// Wrapped continuation line: non-empty, indented in the
				// source, and not itself a new list item.
				if t != "" && len(items) > 0 && strings.HasPrefix(lines[i], "  ") {
					items[len(items)-1] += " " + t
					i++
					continue
				}
				break
			}
			out.WriteString("<ul class=\"checklist\">\n")
			for _, item := range items {
				if cm := isChecklist.FindStringSubmatch(item); cm != nil {
					checked := ""
					if strings.ToLower(cm[1]) == "x" {
						checked = " checked"
					}
					out.WriteString("<li><input type=\"checkbox\" disabled" + checked + "> " + inline(cm[2]) + "</li>\n")
				} else {
					out.WriteString("<li>" + inline(item) + "</li>\n")
				}
			}
			out.WriteString("</ul>\n")

		case isTableRow.MatchString(trimmed) && i+1 < len(lines) && isTableSep.MatchString(lines[i+1]) && strings.Contains(lines[i+1], "-"):
			flushPara()
			header := splitRow(trimmed)
			i += 2 // header + separator
			var bodyRows [][]string
			for i < len(lines) && isTableRow.MatchString(strings.TrimSpace(lines[i])) {
				bodyRows = append(bodyRows, splitRow(strings.TrimSpace(lines[i])))
				i++
			}
			out.WriteString("<table>\n<thead><tr>")
			for _, c := range header {
				out.WriteString("<th>" + inline(c) + "</th>")
			}
			out.WriteString("</tr></thead>\n<tbody>\n")
			for _, row := range bodyRows {
				out.WriteString("<tr>")
				for _, c := range row {
					out.WriteString("<td>" + inline(c) + "</td>")
				}
				out.WriteString("</tr>\n")
			}
			out.WriteString("</tbody>\n</table>\n")

		default:
			para = append(para, inline(trimmed))
			i++
		}
	}
	flushPara()
	return out.String()
}

func splitRow(row string) []string {
	row = strings.Trim(row, "|")
	parts := strings.Split(row, "|")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

var (
	reBold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reItalic = regexp.MustCompile(`\*([^*]+)\*`)
	reCode   = regexp.MustCompile("`([^`]+)`")
)

// inline escapes a line of text as HTML, then applies bold/italic/code —
// safe because escaping happens first, so the markdown markers themselves
// (plain asterisks/backticks) survive escaping untouched and only get
// turned into tags afterward.
func inline(text string) string {
	escaped := html.EscapeString(text)
	escaped = reBold.ReplaceAllString(escaped, "<strong>$1</strong>")
	escaped = reItalic.ReplaceAllString(escaped, "<em>$1</em>")
	escaped = reCode.ReplaceAllString(escaped, "<code>$1</code>")
	return escaped
}
