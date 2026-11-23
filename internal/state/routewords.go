package state

import (
	"regexp"
	"strings"
	"unicode"
)

// routeWords are the words of a provider, a model, an effort level or a
// generation, which a display name never carries: they change with routing
// and migration. The list is maintained here; a loop's own route labels
// (its providers, models, groups and accounts) are added by the caller.
var routeWords = map[string]bool{
	"claude": true, "anthropic": true, "opus": true, "sonnet": true, "haiku": true, "fable": true,
	"openai": true, "gpt": true, "codex": true, "o1": true, "o3": true, "o4": true, "mini": true,
	"gemini": true, "google": true, "antigravity": true, "cursor": true, "grok": true, "xai": true,
	"llama": true, "mistral": true, "deepseek": true, "qwen": true,
	"low": true, "medium": true, "high": true, "xhigh": true, "max": true, "fast": true, "thinking": true,
	"code": true, "model": true, "effort": true, "reasoning": true,
	"gen": true, "generation": true, "v": true,
}

var (
	// versionWord is an explicit version or generation: "v2", "gen3".
	versionWord = regexp.MustCompile(`^(v|gen)\d+(\.\d+)*$`)
	// numberWord is a bare number, a generation only after a route word
	// ("Claude 4", "Opus 4.5"), never alone ("PR 478").
	numberWord = regexp.MustCompile(`^\d+(\.\d+)*$`)
	// ordinalWord is a generation count: "4th", in "4th conversation".
	ordinalWord = regexp.MustCompile(`^\d+(st|nd|rd|th)$`)
	// countedWords follow an ordinal in a generation count.
	countedWords = map[string]bool{"conversation": true, "thread": true, "generation": true, "gen": true}
)

// titleSegment splits a title where a qualifier usually starts: " · ",
// " - ", " – ", " — ", " | ", ":", and brackets.
var titleSegment = regexp.MustCompile(`\s+[·|\-–—]\s+|[()\[\]:]`)

// DisplayNameFromTitle is the default display name made from a BB thread's
// title: the title without the words of a provider, model, effort level or
// generation (routeWords, a version or generation number, or a word of one
// of labels, the loop's own route labels). A qualifier made only of such
// words is dropped with the separator before it ("… · Sonnet medium",
// "… (Claude 4)"), and so are such words at the end of a part ("coordinator
// claude 4"); the rest of the title is kept as written, and trailing
// separators are trimmed. trimmed says whether anything was dropped; ""
// means nothing usable is left. A name a person sets is never changed.
func DisplayNameFromTitle(title string, labels []string) (name string, trimmed bool) {
	title = CleanLaneTitle(title)
	extra := map[string]bool{}
	for _, l := range labels {
		for _, w := range routeTokens(l) {
			extra[w] = true
		}
	}
	isRoute := func(w string) bool {
		return routeWords[w] || extra[w] || versionWord.MatchString(w) || ordinalWord.MatchString(w)
	}
	// dropLast says whether the last of words is a route word in context.
	dropLast := func(words []string) bool {
		toks := routeTokens(words[len(words)-1])
		if len(toks) == 0 {
			return true // a separator or punctuation alone
		}
		var prev []string
		if len(words) > 1 {
			prev = routeTokens(words[len(words)-2])
		}
		for i, w := range toks {
			w = strings.Trim(w, ".")
			before := prev
			if i > 0 {
				before = toks[:i]
			}
			last := ""
			if len(before) > 0 {
				last = strings.Trim(before[len(before)-1], ".")
			}
			switch {
			case isRoute(w):
			case numberWord.MatchString(w) && (routeWords[last] || extra[last]):
			case countedWords[w] && ordinalWord.MatchString(last):
			default:
				return false
			}
		}
		return true
	}
	// Alternating parts and the separators between them.
	seps := titleSegment.FindAllStringIndex(title, -1)
	var b strings.Builder
	start, pendingSep := 0, ""
	for i := 0; i <= len(seps); i++ {
		end := len(title)
		if i < len(seps) {
			end = seps[i][0]
		}
		part := title[start:end]
		words := strings.Fields(part)
		n := len(words)
		for n > 0 && dropLast(words[:n]) {
			n--
		}
		if n > 0 {
			kept := part
			if n < len(words) {
				// Cut the part just after its last kept word.
				idx := 0
				for _, w := range words[:n] {
					idx = strings.Index(part[idx:], w) + idx + len(w)
				}
				kept = part[:idx]
			}
			if b.Len() > 0 {
				b.WriteString(pendingSep)
			}
			b.WriteString(kept)
		}
		if i < len(seps) {
			pendingSep = title[seps[i][0]:seps[i][1]]
			start = seps[i][1]
		}
	}
	name = strings.TrimRight(strings.TrimSpace(b.String()), " -–—·|:,.;([")
	// An opening bracket left without its close is a fragment: close it.
	if strings.Count(name, "(") > strings.Count(name, ")") {
		name += ")"
	}
	return name, name != title
}

// routeTokens splits a label or word into lower-case tokens at anything that
// is not a letter or digit, keeping dotted versions whole ("4.5").
func routeTokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.'
	})
}
