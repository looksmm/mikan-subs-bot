package tgbot

import (
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"subsbot/internal/tgate"
)

// The Markdown of the admin's texts (welcome, menu, pages, notices, broadcasts), turned
// into the HTML the bot sends (parse_mode HTML): HTML escapes one way, where MarkdownV2
// would make the admin escape every dot and dash.
//
//	**bold**  *italic* _italic_  __underline__  ~~strike~~  ||spoiler||
//	`code`  ```block```  > quote  # heading (bold)  [text](https://…)  \* a literal mark
//
// Lists stay text as typed. What does not parse (a mark without its pair, a link to
// anything but https, http or tg) stays as the admin typed it, escaped: a text never makes
// a message Telegram refuses. Texts written before Markdown were plain and keep reading
// the same: a lone mark, a mark inside a word (snake_case) or with spaces around it is
// text, as in CommonMark.

// markdownHTML converts src. text turns a run of the admin's own text into HTML: it
// escapes it and fills in the {variables}, whose values never go through Markdown.
func markdownHTML(src string, text func(string) string) string {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(src, "\r\n", "\n"), "\r", "\n"), "\n")
	var out strings.Builder
	for i := 0; i < len(lines); {
		if i > 0 {
			out.WriteByte('\n')
		}
		line := lines[i]
		if lang, ok := fenceOpen(line); ok {
			// Up to the closing fence; one that never closes runs to the end.
			var code []string
			j := i + 1
			for ; j < len(lines) && !isFence(lines[j]); j++ {
				code = append(code, lines[j])
			}
			switch {
			case strings.TrimSpace(strings.Join(code, "")) == "":
				// An empty block would be an empty entity: the fences stay text.
				out.WriteString(text(strings.Join(lines[i:min(j+1, len(lines))], "\n")))
			case lang != "":
				out.WriteString(`<pre><code class="language-` + lang + `">` + text(strings.Join(code, "\n")) + "</code></pre>")
			default:
				out.WriteString("<pre>" + text(strings.Join(code, "\n")) + "</pre>")
			}
			i = j + 1
			continue
		}
		if _, ok := quoteLine(line); ok {
			var q []string
			for ; i < len(lines); i++ {
				l, ok := quoteLine(lines[i])
				if !ok {
					break
				}
				q = append(q, l)
			}
			if body := strings.Join(q, "\n"); strings.TrimSpace(body) != "" {
				out.WriteString("<blockquote>" + inline(body, text, 0) + "</blockquote>")
			} else {
				out.WriteString(text(strings.Join(lines[i-len(q):i], "\n")))
			}
			continue
		}
		if m := heading.FindStringSubmatch(line); m != nil {
			out.WriteString("<b>" + inline(headingEnd.ReplaceAllString(m[1], ""), text, markBold) + "</b>")
			i++
			continue
		}
		// A paragraph: the lines up to an empty one or a block of another kind; marks may
		// span its lines. An empty line is a paragraph of its own and stays.
		j := i + 1
		for strings.TrimSpace(line) != "" && j < len(lines) && strings.TrimSpace(lines[j]) != "" && !isBlockStart(lines[j]) {
			j++
		}
		out.WriteString(inline(strings.Join(lines[i:j], "\n"), text, 0))
		i = j
	}
	return out.String()
}

var (
	heading    = regexp.MustCompile(`^ {0,3}#{1,6}[ \t]+(.*[^ \t#].*)$`)
	headingEnd = regexp.MustCompile(`(^|[ \t]+)#*[ \t]*$`)
	fence      = regexp.MustCompile("^ {0,3}```[ \t]*([A-Za-z0-9_+#-]{0,20})[ \t]*$")
	quoteRe    = regexp.MustCompile(`^ {0,3}> ?(.*)$`)
	fenceEnd   = regexp.MustCompile("^ {0,3}```[ \t]*$")
)

func fenceOpen(line string) (lang string, ok bool) {
	m := fence.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func isFence(line string) bool { return fenceEnd.MatchString(line) }

func quoteLine(line string) (string, bool) {
	m := quoteRe.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func isBlockStart(line string) bool {
	_, f := fenceOpen(line)
	_, q := quoteLine(line)
	return f || q || heading.MatchString(line)
}

// The inline marks, as bits of what is open: a mark inside the same mark adds no tag.
type mark uint8

const (
	markBold mark = 1 << iota
	markItalic
	markUnderline
	markStrike
	markSpoiler
	markLink
)

type delim struct {
	s    string
	m    mark
	open string
}

// Longer delimiters first: ** before *.
var delims = []delim{
	{"**", markBold, "b"}, {"__", markUnderline, "u"}, {"~~", markStrike, "s"}, {"||", markSpoiler, "tg-spoiler"},
	{"*", markItalic, "i"}, {"_", markItalic, "i"},
}

// inline converts the marks of s; open are the marks around it.
func inline(s string, text func(string) string, open mark) string {
	var out, plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			out.WriteString(text(plain.String()))
			plain.Reset()
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && isASCIIPunct(s[i+1]):
			plain.WriteByte(s[i+1])
			i += 2
			continue
		case c == '`':
			if end, inner, ok := codeSpan(s, i); ok {
				flush()
				out.WriteString("<code>" + text(inner) + "</code>")
				i = end
				continue
			}
			// An unclosed run of backticks is text, all of it.
			n := runLen(s, i, '`')
			plain.WriteString(s[i : i+n])
			i += n
			continue
		case c == '[' && open&markLink == 0:
			if end, label, href, ok := link(s, i); ok {
				flush()
				out.WriteString(`<a href="` + html.EscapeString(href) + `">` + inline(label, text, open|markLink) + "</a>")
				i = end
				continue
			}
		case c == '*' || c == '_' || c == '~' || c == '|':
			if end, d, inner, ok := emphasis(s, i); ok {
				flush()
				if open&d.m != 0 {
					out.WriteString(inline(inner, text, open))
				} else {
					out.WriteString("<" + d.open + ">" + inline(inner, text, open|d.m) + "</" + d.open + ">")
				}
				i = end
				continue
			}
			// A run that opens nothing is text, all of it: "***" is not three tries.
			n := runLen(s, i, c)
			plain.WriteString(s[i : i+n])
			i += n
			continue
		}
		plain.WriteByte(c)
		i++
	}
	flush()
	return out.String()
}

func isASCIIPunct(b byte) bool {
	return b < utf8.RuneSelf && unicode.IsPunct(rune(b)) || strings.IndexByte("$+<=>^`|~", b) >= 0
}

// runLen is how many c start at s[i].
func runLen(s string, i int, c byte) int {
	n := 0
	for i+n < len(s) && s[i+n] == c {
		n++
	}
	return n
}

// codeSpan: a run of backticks up to the next run of the same length; the inside is
// literal, with one space trimmed on each side when both have one.
func codeSpan(s string, i int) (end int, inner string, ok bool) {
	n := runLen(s, i, '`')
	for j := i + n; j < len(s); {
		if s[j] != '`' {
			j++
			continue
		}
		m := runLen(s, j, '`')
		if m == n {
			inner = s[i+n : j]
			if len(inner) > 2 && inner[0] == ' ' && inner[len(inner)-1] == ' ' && strings.TrimSpace(inner) != "" {
				inner = inner[1 : len(inner)-1]
			}
			if inner == "" {
				return 0, "", false
			}
			return j + m, inner, true
		}
		j += m
	}
	return 0, "", false
}

// link reads [label](href) at s[i]: one line, an href without spaces that safeLink takes.
// An optional "title" after the address is dropped.
func link(s string, i int) (end int, label, href string, ok bool) {
	depth := 0
	j := i
	for ; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
			continue
		case '\n':
			return 0, "", "", false
		case '[':
			depth++
		case ']':
			depth--
		}
		if depth == 0 {
			break
		}
	}
	if j >= len(s) || j+1 >= len(s) || s[j+1] != '(' {
		return 0, "", "", false
	}
	label = s[i+1 : j]
	k := strings.IndexByte(s[j+2:], ')')
	if k < 0 {
		return 0, "", "", false
	}
	dest := strings.TrimSpace(s[j+2 : j+2+k])
	if sp := strings.IndexAny(dest, " \t"); sp >= 0 {
		if title := strings.TrimSpace(dest[sp:]); len(title) < 2 || title[0] != '"' || title[len(title)-1] != '"' {
			return 0, "", "", false
		}
		dest = dest[:sp]
	}
	if strings.TrimSpace(label) == "" || !safeLink(dest) {
		return 0, "", "", false
	}
	return j + 2 + k + 1, label, dest, true
}

// safeLink: what a link of the admin's text may open — web pages and Telegram links, as
// the menu's buttons, and plain http.
func safeLink(s string) bool {
	if strings.HasPrefix(strings.ToLower(s), "http://") {
		s = "https://" + s[len("http://"):]
	}
	return len(s) <= 2048 && tgate.SafeURL(s)
}

// emphasis reads a mark at s[i] with its closing pair, CommonMark's way: the opening one
// leans on the text after it, the closing one on the text before; "_" does not work
// inside a word.
func emphasis(s string, i int) (end int, d delim, inner string, ok bool) {
	c := s[i]
	n := runLen(s, i, c)
	for _, d := range delims {
		if d.s[0] != c || n < len(d.s) || (len(d.s) == 1 && n == 2) {
			continue
		}
		if !canOpen(s, i, i+len(d.s), c) {
			continue
		}
		if j, ok := closer(s, i+len(d.s), d); ok {
			return j + len(d.s), d, s[i+len(d.s) : j], true
		}
	}
	return 0, delim{}, "", false
}

// closer finds where d closes after from: not inside a code span, not escaped, and for a
// one-letter mark not part of a double one.
func closer(s string, from int, d delim) (int, bool) {
	c := d.s[0]
	for j := from; j < len(s); {
		switch {
		case s[j] == '\\':
			j += 2
			continue
		case s[j] == '`':
			if end, _, ok := codeSpan(s, j); ok {
				j = end
				continue
			}
			j += runLen(s, j, '`')
			continue
		case s[j] != c:
			j++
			continue
		}
		n := runLen(s, j, c)
		if j > from && n >= len(d.s) && !(len(d.s) == 1 && n == 2) && canClose(s, j, j+len(d.s), c) {
			return j, true
		}
		j += n
	}
	return 0, false
}

// before and after are the runes around a mark; a line's edge reads as a space.
func around(s string, start, end int) (before, after rune) {
	before, after = ' ', ' '
	if start > 0 {
		before, _ = utf8.DecodeLastRuneInString(s[:start])
	}
	if end < len(s) {
		after, _ = utf8.DecodeRuneInString(s[end:])
	}
	return before, after
}

func punct(r rune) bool { return unicode.IsPunct(r) || unicode.IsSymbol(r) }

func canOpen(s string, start, end int, c byte) bool {
	before, after := around(s, start, end)
	left := !unicode.IsSpace(after) && (!punct(after) || unicode.IsSpace(before) || punct(before))
	if c != '_' {
		return left
	}
	right := !unicode.IsSpace(before) && (!punct(before) || unicode.IsSpace(after) || punct(after))
	return left && (!right || punct(before))
}

func canClose(s string, start, end int, c byte) bool {
	before, after := around(s, start, end)
	right := !unicode.IsSpace(before) && (!punct(before) || unicode.IsSpace(after) || punct(after))
	if c != '_' {
		return right
	}
	left := !unicode.IsSpace(after) && (!punct(after) || unicode.IsSpace(before) || punct(before))
	return right && (!left || punct(after))
}
