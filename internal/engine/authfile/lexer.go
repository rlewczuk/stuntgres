package authfile

import (
	"context"
	"strings"
)

func fields(ctx context.Context, path, text string, depth int) ([][]Token, string, error) {
	var result [][]Token
	pos := 0
	for pos < len(text) {
		var field []Token
		for {
			token, quoted, comma, ok := nextToken(text, &pos)
			if !ok {
				break
			}
			if !quoted && len(token) > 1 && token[0] == '@' {
				records, err := read(ctx, resolve(path, token[1:]), depth+1)
				if err != nil {
					if ctx.Err() != nil {
						return nil, "", ctx.Err()
					}
					return result, message(err), nil
				}
				for _, r := range records {
					if r.Diagnostic != "" {
						return result, r.Diagnostic, nil
					}
					for _, f := range r.Fields {
						field = append(field, f...)
					}
				}
			} else {
				field = append(field, Token{Text: token, InitiallyQuoted: quoted})
			}
			if !comma {
				break
			}
		}
		if len(field) > 0 {
			result = append(result, field)
		}
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
	}
	return result, "", nil
}
func nextToken(text string, pos *int) (string, bool, bool, bool) {
	for *pos < len(text) && (authBlank(text[*pos]) || text[*pos] == ',') {
		(*pos)++
	}
	var token strings.Builder
	inQuote, wasQuote, sawQuote, initial := false, false, false, false
	for *pos < len(text) {
		c := text[*pos]
		if !inQuote && authBlank(c) {
			(*pos)++
			break
		}
		if !inQuote && c == '#' {
			*pos = len(text)
			break
		}
		if !inQuote && c == ',' {
			return token.String(), initial, true, sawQuote || token.Len() > 0
		}
		if c != '"' || wasQuote {
			token.WriteByte(c)
		}
		if inQuote && c == '"' {
			wasQuote = !wasQuote
		} else {
			wasQuote = false
		}
		if c == '"' {
			inQuote = !inQuote
			sawQuote = true
			if token.Len() == 0 {
				initial = true
			}
		}
		(*pos)++
	}
	return token.String(), initial, false, sawQuote || token.Len() > 0
}

// pg_isblank in the authentication tokenizer includes CR, but not LF/VT/FF.
func authBlank(c byte) bool { return c == ' ' || c == '\t' || c == '\r' }
