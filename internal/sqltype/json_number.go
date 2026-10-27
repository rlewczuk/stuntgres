package sqltype

import "context"

// scanJSONNumber recognizes a numeric prefix without converting its magnitude.
// The caller owns trailing-token rules and diagnostic endpoints.
func scanJSONNumber(ctx context.Context, text string, start int) (int, bool, error) {
	i := start
	if i == len(text) || text[i] != '-' && !jsonDigit(text[i]) {
		return i, false, nil
	}
	if text[i] == '-' {
		i++
	}
	valid := true
	if i == len(text) || !jsonDigit(text[i]) {
		valid = false
	} else if text[i] == '0' {
		i++
	} else {
		for i < len(text) && jsonDigit(text[i]) {
			i++
			if i&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return i, false, err
				}
			}
		}
	}
	if i < len(text) && text[i] == '.' {
		i++
		first := i
		for i < len(text) && jsonDigit(text[i]) {
			i++
			if i&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return i, false, err
				}
			}
		}
		valid = valid && i > first
	}
	if i < len(text) && (text[i] == 'e' || text[i] == 'E') {
		i++
		if i < len(text) && (text[i] == '+' || text[i] == '-') {
			i++
		}
		first := i
		for i < len(text) && jsonDigit(text[i]) {
			i++
			if i&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return i, false, err
				}
			}
		}
		valid = valid && i > first
	}
	return i, valid, nil
}
