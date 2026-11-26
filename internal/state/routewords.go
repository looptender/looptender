package state

import (
	"regexp"
	"strings"
	"unicode"
)

// providerWords name a provider, a model or a plan: a trailing qualifier
// made of route words is dropped from a title-derived display name only when
// it has one of these (or one of the loop's own route labels).
var providerWords = map[string]bool{
	"claude": true, "anthropic": true, "opus": true, "sonnet": true, "haiku": true, "fable": true,
	"openai": true, "gpt": true, "codex": true, "o1": true, "o3": true, "o4": true,
	"gemini": true, "antigravity": true, "neon": true, "cursor": true, "grok": true, "xai": true,
	"llama": true, "mistral": true, "deepseek": true, "qwen": true, "ultra": true,
}

// effortWords are ordinary words that count only inside such a qualifier,
// beside a provider or model word: never on their own and never in running
// text ("Write code", "Make the CLI fast" are names).
var effortWords = map[string]bool{
	"low": true, "medium": true, "high": true, "xhigh": true, "max": true, "fast": true, "thinking": true,
	"code": true, "model": true, "effort": true, "reasoning": true, "mini": true, "pro": true,
	"v": true, "gen": true, "generation": true, "conversation": true, "thread": true,
}

var (
	// countWord is a version, a generation or a plan multiplier: "4",
	// "4.5", "v2", "gen3", "4th", "5x".
	countWord = regexp.MustCompile(`^(v|gen)?\d+(\.\d+)*(st|nd|rd|th|x)?$`)
	ordinal   = regexp.MustCompile(`^\d+(st|nd|rd|th)$`)
	// qualifierSep starts a trailing qualifier segment.
	qualifierSeps = []string{" · ", " - ", " – ", " — ", " | "}
)

// DisplayNameFromTitle makes a task-only inheritance fallback from a BB
// title, before the successor has a title of its own. Current observed titles
// are shown as written. Only whole trailing qualifiers are dropped: a
// bracketed group at the end, or the part after the last " · ", " - ",
// " – ", " — " or " | ", when it consists entirely of route words and names
// a provider, model or plan (or is a generation count such as "4th
// conversation"): " · Sonnet medium", " (Claude 4)", " · codex high",
// " (Antigravity, Neon high)", " · Ultra 5x", or one of labels, the loop's
// own route labels. Running text is never cut and brackets are kept whole.
// When nothing usable would be left, the title is kept as it is. trimmed
// says whether a qualifier was dropped. A name a person sets is never
// changed.
func DisplayNameFromTitle(title string, labels []string) (name string, trimmed bool) {
	title = CleanLaneTitle(title)
	extra := map[string]bool{}
	for _, l := range labels {
		for _, w := range routeTokens(l) {
			extra[w] = true
		}
	}
	name = title
	for {
		rest, q, ok := trailingQualifier(name)
		if !ok || !routeQualifier(q, extra) {
			break
		}
		name = strings.TrimSpace(rest)
	}
	if name == "" {
		return title, false
	}
	return name, name != title
}

// trailingQualifier splits off the last qualifier of s: a balanced bracketed
// group that ends s and follows a space, or else the part after the last
// qualifier separator.
func trailingQualifier(s string) (rest, q string, ok bool) {
	if end := len(s) - 1; end > 0 && (s[end] == ')' || s[end] == ']') {
		open, close := byte('('), s[end]
		if close == ']' {
			open = '['
		}
		depth := 0
		for i := end; i >= 0; i-- {
			switch s[i] {
			case close:
				depth++
			case open:
				depth--
			}
			if depth == 0 {
				if i > 0 && s[i-1] == ' ' {
					return s[:i], s[i+1 : end], true
				}
				return "", "", false // glued to a word, or the whole title
			}
		}
		return "", "", false // unbalanced: kept as written
	}
	at, sep := -1, ""
	for _, x := range qualifierSeps {
		if i := strings.LastIndex(s, x); i > at {
			at, sep = i, x
		}
	}
	if at < 0 {
		return "", "", false
	}
	return s[:at], s[at+len(sep):], true
}

// routeQualifier reports whether q consists only of route words and names a
// provider, model or plan, or is a generation count.
func routeQualifier(q string, extra map[string]bool) bool {
	toks := routeTokens(q)
	if len(toks) == 0 {
		return false
	}
	named, counted := false, false
	for i, w := range toks {
		w = strings.Trim(w, ".")
		switch {
		case providerWords[w] || extra[w]:
			named = true
		case countWord.MatchString(w) || effortWords[w]:
			if (w == "conversation" || w == "thread" || w == "generation") && i > 0 && ordinal.MatchString(toks[i-1]) {
				counted = true
			}
		default:
			return false
		}
	}
	return named || counted
}

// routeTokens splits a label or text into lower-case tokens at anything that
// is not a letter or digit, keeping dotted versions whole ("4.5").
func routeTokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.'
	})
}
