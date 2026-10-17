package logging

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	ansiPattern       = regexp.MustCompile("\x1b" + `\[[0-?]*[ -/]*[@-~]`)
	bearerPattern     = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/\-=]+`)
	headerPattern     = regexp.MustCompile(`(?i)((?:set-cookie|cookie|authorization)\s*[:=]\s*)[^\r\n]+`)
	assignmentPattern = regexp.MustCompile(`(?i)(["']?(?:password|passwd|access[_-]?token|refresh[_-]?token|session[_-]?(?:token|id)|account[_-]?(?:session|csrf)|csrf[_-]?token|api[_-]?key|secret|authorization|(?:set[_-]?)?cookie|token)["']?\s*[:=]\s*)("(?:\\.|[^"\\])*"|'[^']*'|[^\s,;&}\]]+)`)
	tokenPattern      = regexp.MustCompile(`\b(?:ahk_[A-Za-z0-9_-]{20,}|sk-[A-Za-z0-9_-]{16,}|hub_[A-Za-z0-9]{10}|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)\b`)
	userinfoPattern   = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s@]+@`)
)

func sensitiveKey(key string) bool {
	if i := strings.LastIndexByte(key, '.'); i >= 0 {
		key = key[i+1:]
	}
	key = strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(key))
	if strings.HasSuffix(key, "password") || strings.HasSuffix(key, "secret") {
		return true
	}
	switch key {
	case "password", "passwd", "accesstoken", "refreshtoken", "sessiontoken", "sessionid",
		"accountsession", "accountcsrf", "csrftoken", "apikey", "secret", "authorization",
		"cookie", "setcookie", "token":
		return true
	default:
		return false
	}
}

func redactText(value string) string {
	value = ansiPattern.ReplaceAllString(value, "")
	value = bearerPattern.ReplaceAllString(value, "$1 [REDACTED]")
	value = headerPattern.ReplaceAllString(value, "$1[REDACTED]")
	value = assignmentPattern.ReplaceAllString(value, "$1[REDACTED]")
	value = tokenPattern.ReplaceAllString(value, "[REDACTED]")
	value = userinfoPattern.ReplaceAllString(value, "$1[REDACTED]@")
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return -1
		}
		return r
	}, value)
}

func limitText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	const suffix = "… [truncated]"
	end := limit - len(suffix)
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:max(end, 0)] + suffix
}
