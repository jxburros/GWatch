package store

import "strings"

// sqlProtectedEnd skips a quoted value/identifier or comment starting at i.
// The result is exclusive; i means this byte is ordinary SQL.
func sqlProtectedEnd(s string, i int) (int, bool) {
	if s[i] == '\'' || s[i] == '"' || s[i] == '`' {
		quote := s[i]
		for j := i + 1; j < len(s); j++ {
			if s[j] == quote {
				if j+1 < len(s) && s[j+1] == quote {
					j++
					continue
				}
				return j + 1, false
			}
		}
		return len(s), false
	}
	if strings.HasPrefix(s[i:], "--") {
		if n := strings.IndexByte(s[i:], '\n'); n >= 0 {
			return i + n + 1, true
		}
		return len(s), true
	}
	if strings.HasPrefix(s[i:], "/*") {
		depth := 1
		for j := i + 2; j < len(s)-1; j++ {
			if s[j:j+2] == "/*" {
				depth++
				j++
			} else if s[j:j+2] == "*/" {
				depth--
				j++
				if depth == 0 {
					return j + 1, true
				}
			}
		}
		return len(s), true
	}
	if s[i] == '$' {
		j := i + 1
		for j < len(s) && (s[j] == '_' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || j > i+1 && s[j] >= '0' && s[j] <= '9') {
			j++
		}
		if j < len(s) && s[j] == '$' {
			tag := s[i : j+1]
			if end := strings.Index(s[j+1:], tag); end >= 0 {
				return j + 1 + end + len(tag), false
			}
		}
	}
	return i, false
}
