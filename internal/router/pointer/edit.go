package pointer

import (
	"bytes"
	"encoding/json"
	"errors"
)

// A settings file is a person's own, with their own layout. These helpers
// change one member of one JSON object in its text and leave every other
// byte as it was: the file is never decoded and written out again.

var errJSON = errors.New("not a JSON object")

// member is one "key": value pair of an object, as positions in the text.
type member struct {
	key              string
	keyStart         int // the key's opening quote
	valStart, valEnd int // the value, end exclusive
}

func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// skipString returns the position after the string that starts at i.
func skipString(b []byte, i int) (int, error) {
	for j := i + 1; j < len(b); j++ {
		switch b[j] {
		case '\\':
			j++
		case '"':
			return j + 1, nil
		}
	}
	return 0, errJSON
}

// skipValue returns the position after the JSON value that starts at i.
func skipValue(b []byte, i int) (int, error) {
	if i >= len(b) {
		return 0, errJSON
	}
	switch b[i] {
	case '"':
		return skipString(b, i)
	case '{', '[':
		depth := 0
		for j := i; j < len(b); j++ {
			switch b[j] {
			case '"':
				end, err := skipString(b, j)
				if err != nil {
					return 0, err
				}
				j = end - 1
			case '{', '[':
				depth++
			case '}', ']':
				if depth--; depth == 0 {
					return j + 1, nil
				}
			}
		}
		return 0, errJSON
	}
	j := i
	for j < len(b) && !bytes.ContainsRune([]byte(",}] \t\r\n"), rune(b[j])) {
		j++
	}
	if j == i {
		return 0, errJSON
	}
	return j, nil
}

// members lists the members of the object that starts at b[start] and
// returns the position of its closing brace.
func members(b []byte, start int) (list []member, end int, err error) {
	if start >= len(b) || b[start] != '{' {
		return nil, 0, errJSON
	}
	i := skipSpace(b, start+1)
	if i < len(b) && b[i] == '}' {
		return nil, i, nil
	}
	for {
		if i >= len(b) || b[i] != '"' {
			return nil, 0, errJSON
		}
		keyEnd, err := skipString(b, i)
		if err != nil {
			return nil, 0, err
		}
		var key string
		if json.Unmarshal(b[i:keyEnd], &key) != nil {
			return nil, 0, errJSON
		}
		m := member{key: key, keyStart: i}
		j := skipSpace(b, keyEnd)
		if j >= len(b) || b[j] != ':' {
			return nil, 0, errJSON
		}
		m.valStart = skipSpace(b, j+1)
		if m.valEnd, err = skipValue(b, m.valStart); err != nil {
			return nil, 0, err
		}
		list = append(list, m)
		i = skipSpace(b, m.valEnd)
		if i < len(b) && b[i] == '}' {
			return list, i, nil
		}
		if i >= len(b) || b[i] != ',' {
			return nil, 0, errJSON
		}
		i = skipSpace(b, i+1)
	}
}

// find returns the member named key of the object at start; ok is false
// when there is none. A key given twice is an error: which one a reader
// takes is not ours to guess.
func find(b []byte, start int, key string) (m member, ok bool, err error) {
	list, _, err := members(b, start)
	if err != nil {
		return member{}, false, err
	}
	for _, x := range list {
		if x.key == key {
			if ok {
				return member{}, false, errJSON
			}
			m, ok = x, true
		}
	}
	return m, ok, nil
}

// splice returns b with b[from:to] replaced by with.
func splice(b []byte, from, to int, with []byte) []byte {
	out := make([]byte, 0, len(b)-(to-from)+len(with))
	out = append(out, b[:from]...)
	out = append(out, with...)
	return append(out, b[to:]...)
}

// lineStart returns the white space that begins the line holding position i.
func indentOf(b []byte, i int) []byte {
	start := bytes.LastIndexByte(b[:i], '\n') + 1
	end := start
	for end < i && (b[end] == ' ' || b[end] == '\t') {
		end++
	}
	return b[start:end]
}

func newline(b []byte) []byte {
	if bytes.Contains(b, []byte("\r\n")) {
		return []byte("\r\n")
	}
	return []byte("\n")
}

// set gives the object at start a member key with the JSON text value,
// replacing the value when the member exists and adding it after the last
// member otherwise, laid out as the object's other members are.
func set(b []byte, start int, key string, value []byte) ([]byte, error) {
	m, ok, err := find(b, start, key)
	if err != nil {
		return nil, err
	}
	if ok {
		return splice(b, m.valStart, m.valEnd, value), nil
	}
	list, end, err := members(b, start)
	if err != nil {
		return nil, err
	}
	name, _ := json.Marshal(key)
	pair := append(append(name, ':', ' '), value...)
	if len(list) > 0 {
		last := list[len(list)-1]
		// The same separation as between the members already there: what
		// stands between the object's start, or the comma before the last
		// member, and that member's key.
		from := start + 1
		if len(list) > 1 {
			from = bytes.IndexByte(b[list[len(list)-2].valEnd:], ',') + list[len(list)-2].valEnd + 1
		}
		sep := b[from:last.keyStart]
		return splice(b, last.valEnd, last.valEnd, append(append([]byte{','}, sep...), pair...)), nil
	}
	// An empty object. On one line when the text has no line breaks at all;
	// otherwise one member a level in from the object's own line.
	if !bytes.ContainsAny(b, "\n") {
		return splice(b, start+1, end, pair), nil
	}
	nl, outer := newline(b), indentOf(b, start)
	step := []byte("  ")
	if top, _, err := members(b, bytes.IndexByte(b, '{')); err == nil && len(top) > 0 {
		if in := indentOf(b, top[0].keyStart); len(in) > 0 {
			step = in
		}
	}
	var body []byte
	body = append(body, nl...)
	body = append(append(body, outer...), step...)
	body = append(body, pair...)
	body = append(append(body, nl...), outer...)
	return splice(b, start+1, end, body), nil
}

// remove takes the member key out of the object at start, with the comma
// and white space that went with it. Nothing changes when there is none.
func remove(b []byte, start int, key string) ([]byte, error) {
	list, end, err := members(b, start)
	if err != nil {
		return nil, err
	}
	for i, m := range list {
		if m.key != key {
			continue
		}
		switch {
		case len(list) == 1:
			return splice(b, start+1, end, nil), nil
		case i == 0:
			return splice(b, m.keyStart, list[1].keyStart, nil), nil
		default:
			return splice(b, list[i-1].valEnd, m.valEnd, nil), nil
		}
	}
	return b, nil
}
