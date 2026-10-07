package sqltype

import "strings"

// DateStyle uses PostgreSQL's identifier-list syntax, including quoted items;
// settings decode the list once, never in the row formatter.
func dateStyleWords(text string) ([]string, bool) {
	var words []string
	at := 0
	skipSpace := func() {
		for at < len(text) && space(text[at]) {
			at++
		}
	}
	skipSpace()
	if at == len(text) {
		return words, true
	}
	for {
		start := at
		word := ""
		if text[at] == '"' {
			at++
			start = at
			var decoded strings.Builder
			for {
				if at == len(text) {
					return nil, false
				}
				if text[at] != '"' {
					at++
					continue
				}
				if at+1 < len(text) && text[at+1] == '"' {
					decoded.WriteString(text[start : at+1])
					at += 2
					start = at
					continue
				}
				if decoded.Len() == 0 {
					word = text[start:at]
				} else {
					decoded.WriteString(text[start:at])
					word = decoded.String()
				}
				at++
				break
			}
		} else {
			for at < len(text) && text[at] != ',' && !space(text[at]) {
				at++
			}
			if start == at {
				return nil, false
			}
			word = text[start:at]
		}
		skipSpace()
		words = append(words, word)
		if at == len(text) {
			return words, true
		}
		if text[at] != ',' {
			return nil, false
		}
		at++
		skipSpace()
		if at == len(text) {
			return nil, false
		}
	}
}
