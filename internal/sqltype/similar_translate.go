package sqltype

import (
	"context"
	"strings"
	"stuntgres/internal/sqlstate"
	"unicode/utf8"
)

// TranslateSimilar implements the SQL frontend, not a public POSIX interface.
// The caller supplies backslash for an omitted ESCAPE; empty disables escaping.
func TranslateSimilar(ctx context.Context, pattern, escape string) (string, error) {
	return translateSimilarBudget(ctx, pattern, escape, pgRegexMemoryLimit)
}

func translateSimilarBudget(ctx context.Context, pattern, escape string, limit int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if escape != "" {
		_, n := utf8.DecodeRuneInString(escape)
		if n != len(escape) {
			return "", &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."}
		}
	}
	// Count first: the checked exact capacity precedes the only allocation.
	size, err := similarTranslatePass(ctx, pattern, escape, nil, limit)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	out.Grow(size)
	_, err = similarTranslatePass(ctx, pattern, escape, &out, limit)
	if err != nil {
		return "", err
	}
	return out.String(), nil
}

func similarTranslatePass(ctx context.Context, pattern, escape string, out *strings.Builder, limit int) (int, error) {
	size, depth, classpos, quotes := 0, 0, 0, 0
	after := false
	work := pgRegexWork{ctx: ctx}
	emit := func(s string) error {
		if len(s) > limit-size {
			return pgRegexCompileError("regular expression is too complex")
		}
		size += len(s)
		if out != nil {
			out.WriteString(s)
		}
		return nil
	}
	if err := emit("^(?:"); err != nil {
		return 0, err
	}
	for i := 0; i < len(pattern); {
		if err := work.step(); err != nil {
			return 0, err
		}
		_, n := utf8.DecodeRuneInString(pattern[i:])
		s := pattern[i : i+n]
		i += n
		var e string
		slash := false
		// Match release's special multibyte-escape path: it does not alter
		// bracket position when copying a multibyte character.
		if len(escape) > 1 && n > 1 {
			if after {
				e = s
				slash = true
				after = false
			} else if s == escape {
				after = true
			} else {
				e = s
			}
		} else if after {
			if s == "\"" && depth == 0 {
				switch quotes {
				case 0:
					e = "){1,1}?("
				case 1:
					e = "){1,1}(?:"
				default:
					return 0, failure("2200C", "SQL regular expression may not contain more than two escape-double-quote separators")
				}
				quotes++
			} else {
				e = s
				slash = true
				classpos = 3
			}
			after = false
		} else if escape != "" && s == escape {
			after = true
		} else if depth > 0 {
			e = s
			if s == "\\" {
				e = "\\\\"
			}
			switch {
			case s == "]" && classpos > 2:
				depth--
			case s == "[":
				depth++
				classpos = 3
			case s == "^":
				classpos++
			default:
				classpos = 3
			}
		} else {
			switch s {
			case "[":
				depth = 1
				classpos = 1
				e = s
			case "%":
				e = ".*"
			case "_":
				e = "."
			case "(":
				e = "(?:"
			case "\\", ".", "^", "$":
				e = s
				slash = true
			default:
				e = s
			}
		}
		if slash {
			if err := emit("\\"); err != nil {
				return 0, err
			}
		}
		if err := emit(e); err != nil {
			return 0, err
		}
	}
	if err := emit(")$"); err != nil {
		return 0, err
	}
	return size, ctx.Err()
}
