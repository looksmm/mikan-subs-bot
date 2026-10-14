// Package textutil holds the small text helpers more than one part of the panel and the node
// needs, so each does not keep its own copy with its own idea of where a string may be cut.
package textutil

import "strings"

// Clip cuts s to at most maxBytes bytes without splitting a character: a character the cut
// falls into is dropped whole, and so is any byte that is not valid UTF-8, so the result is
// always valid text for a JSON answer or a database column. Text from a remote host, a
// provider or a node goes through it before it is stored or shown.
func Clip(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) > maxBytes {
		s = s[:maxBytes]
	}
	return strings.ToValidUTF8(s, "")
}

// ClipRunes cuts s to at most maxRunes characters (not bytes) and, like Clip, never leaves
// a byte that is not valid UTF-8 in the result.
func ClipRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	n := 0
	for i := range s {
		if n == maxRunes {
			s = s[:i]
			break
		}
		n++
	}
	return strings.ToValidUTF8(s, "")
}
