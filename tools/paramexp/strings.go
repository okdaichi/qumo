// Package main — string helpers (avoiding strings/stdlib to stay minimal).
package main

// split splits on a separator (single char for MVP).
func split(s, sep string) []string {
	if sep == "" {
		return []string{s}
	}
	var out []string
	for {
		i := indexOfStr(s, sep)
		if i < 0 {
			out = append(out, s)
			return out
		}
		out = append(out, s[:i])
		s = s[i+len(sep):]
	}
}

func splitNewlines(s string) []string {
	return split(s, "\n")
}

func indexOfStr(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

func trim(s, cutset string) string {
	if len(cutset) == 0 {
		return s
	}
	for len(s) > 0 && containsChar(cutset, s[0]) {
		s = s[1:]
	}
	for len(s) > 0 && containsChar(cutset, s[len(s)-1]) {
		s = s[:len(s)-1]
	}
	return s
}

func containsChar(s string, c byte) bool {
	for i := range s {
		if s[i] == c {
			return true
		}
	}
	return false
}
