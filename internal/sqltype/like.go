package sqltype

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

// LikePlan retains descriptors and operation identity, never execution values.
type LikePlan struct {
	args                           [2]Usage
	result                         Usage
	binary, fold, negate, conflict bool
}

func BindLike(operator string, left, right, result Usage, conflict bool) (LikePlan, error) {
	p := LikePlan{args: [2]Usage{left, right}, result: result, conflict: conflict}
	switch operator {
	case "~~":
	case "!~~":
		p.negate = true
	case "~~*":
		p.fold = true
	case "!~~*":
		p.fold, p.negate = true, true
	default:
		return p, fmt.Errorf("invalid LIKE operation")
	}
	if !left.Valid() || !right.Valid() || !result.Valid() || result.Type.ID() != Bool {
		return p, fmt.Errorf("invalid LIKE usage")
	}
	p.binary = left.Type.ID() == Bytea
	if p.binary {
		if right.Type != left.Type || p.fold {
			return p, fmt.Errorf("invalid binary LIKE signature")
		}
	} else if (left.Type.ID() != Text && left.Type.ID() != Name && left.Type.ID() != BPChar) || right.Type.ID() != Text {
		return p, fmt.Errorf("invalid text LIKE signature")
	}
	return p, nil
}

func (p LikePlan) Evaluate(ctx context.Context, a, b Value) (Value, error) {
	if p.result.Type == nil || a.typ != p.args[0].Type || b.typ != p.args[1].Type {
		return Value{}, fmt.Errorf("invalid LIKE values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null || b.null {
		return Null(p.result.Type), nil
	}
	if p.conflict {
		name := "LIKE"
		if p.fold {
			name = "ILIKE"
		}
		return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for " + name, Hint: "Use the COLLATE clause to set the collation explicitly."}
	}
	matched, err := matchLike(ctx, a.text, b.text, p.binary, p.fold)
	if err != nil {
		return Value{}, err
	}
	return Boolean(p.result.Type, matched != p.negate), nil
}

func likeAdvance(s string, i int, binary bool) int {
	if binary || s[i] < utf8.RuneSelf {
		return i + 1
	}
	_, n := utf8.DecodeRuneInString(s[i:])
	return i + n
}

func likeFold(b byte, fold bool) byte {
	if fold && b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// The most recent percent supplies the sole retry continuation. Retrying
// advances a character boundary, not a byte inside a text code point. After a
// percent folds adjacent wildcards, inspect its next escaped token before
// searching the remaining input, matching the release's lazy error ordering.
func matchLike(ctx context.Context, input, pattern string, binary, fold bool) (bool, error) {
	i, j, retryInput, retryPattern, work := 0, 0, -1, -1, 0
	for i < len(input) {
		work++
		if work&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if j < len(pattern) && pattern[j] == '%' {
			j++
			for j < len(pattern) && (pattern[j] == '%' || pattern[j] == '_') {
				work++
				if work&4095 == 0 {
					if err := ctx.Err(); err != nil {
						return false, err
					}
				}
				if pattern[j] == '_' {
					if i == len(input) {
						return false, nil
					}
					i = likeAdvance(input, i, binary)
				}
				j++
			}
			if j == len(pattern) {
				return true, nil
			}
			if pattern[j] == '\\' && j+1 == len(pattern) {
				return false, failure("22025", "LIKE pattern must not end with escape character")
			}
			retryInput, retryPattern = i, j
			if i == len(input) {
				return false, nil
			}
			continue
		}
		if j < len(pattern) {
			if pattern[j] == '_' {
				i, j = likeAdvance(input, i, binary), j+1
				continue
			}
			literal := j
			if pattern[j] == '\\' {
				literal++
				if literal == len(pattern) {
					return false, failure("22025", "LIKE pattern must not end with escape character")
				}
			}
			if likeFold(input[i], fold) == likeFold(pattern[literal], fold) {
				i, j = i+1, literal+1
				continue
			}
		}
		if retryInput < 0 || retryInput == len(input) {
			return false, nil
		}
		retryInput = likeAdvance(input, retryInput, binary)
		i, j = retryInput, retryPattern
	}
	for j < len(pattern) && pattern[j] == '%' {
		work++
		if work&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		j++
	}
	return j == len(pattern), nil
}

type LikeEscapePlan struct {
	args   [2]Usage
	result Usage
	binary bool
}

func BindLikeEscape(pattern, escape, result Usage) (LikeEscapePlan, error) {
	p := LikeEscapePlan{args: [2]Usage{pattern, escape}, result: result}
	if !pattern.Valid() || !escape.Valid() || !result.Valid() || pattern.Type != escape.Type || pattern.Type != result.Type || (pattern.Type.ID() != Text && pattern.Type.ID() != Bytea) {
		return p, fmt.Errorf("invalid LIKE escape signature")
	}
	p.binary = pattern.Type.ID() == Bytea
	return p, nil
}

func (p LikeEscapePlan) Evaluate(ctx context.Context, pattern, escape Value) (Value, error) {
	if p.result.Type == nil || pattern.typ != p.args[0].Type || escape.typ != p.args[1].Type {
		return Value{}, fmt.Errorf("invalid LIKE escape values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if pattern.null || escape.null {
		return Null(p.result.Type), nil
	}
	e := escape.text
	if len(e) != 0 && likeAdvance(e, 0, p.binary) != len(e) {
		return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."}
	}
	if e == "\\" {
		return pattern, nil
	}
	// Determine exact capacity first. The second pass allocates only when the
	// canonical representation differs, and never copies unchanged storage.
	s := pattern.text
	size, changed := len(s), false
	for i, work := 0, 0; i < len(s); work++ {
		if work&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		next := likeAdvance(s, i, p.binary)
		if e != "" && s[i:next] == e {
			size += 1 - len(e)
			changed = true
			if next < len(s) {
				next = likeAdvance(s, next, p.binary)
			}
		} else if s[i] == '\\' {
			size++
			changed = true
		}
		i = next
	}
	if !changed {
		return pattern, nil
	}
	var out strings.Builder
	out.Grow(size)
	for i, work := 0, 0; i < len(s); work++ {
		if work&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		next := likeAdvance(s, i, p.binary)
		if e != "" && s[i:next] == e {
			out.WriteByte('\\')
			if next < len(s) {
				end := likeAdvance(s, next, p.binary)
				out.WriteString(s[next:end])
				next = end
			}
		} else {
			if s[i] == '\\' {
				out.WriteByte('\\')
			}
			out.WriteString(s[i:next])
		}
		i = next
	}
	return Value{typ: p.result.Type, text: out.String()}, nil
}
