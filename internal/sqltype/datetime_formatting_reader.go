package sqltype

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
)

func datetimeError(code, message, detail, hint string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail, Hint: hint}
}

func datetimeCollationError(operation string) error {
	return datetimeError("42P22", "could not determine which collation to use for "+operation+" function", "", "Use the COLLATE clause to set the collation explicitly.")
}

const datetimeFillHint = "If your source string is not fixed-width, try using the \"FM\" modifier."

func datetimeSet(destination *int, value int, node datetimeNode) error {
	if destination == nil {
		return nil
	}
	if *destination != 0 && *destination != value {
		return datetimeError("22007", fmt.Sprintf("conflicting values for %q field in formatting string", node.key.word), "This value contradicts a previous setting for the same field type.", "")
	}
	*destination = value
	return nil
}
func datetimeInteger(ctx context.Context, source string, position *int, width int, node datetimeNode) (int, int, error) {
	initial := *position
	p := initial
	for p < len(source) && datetimeSpace(source[p]) {
		if p%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
		}
		p++
	}
	fragment := source[p:min(len(source), p+width)]
	variable := node.variableWidth
	limit := len(source)
	if !variable {
		if len(source)-p < width {
			return 0, 0, datetimeError("22007", fmt.Sprintf("source string too short for %q formatting field", node.key.word), fmt.Sprintf("Field requires %d characters, but only %d remain.", width, len(source)-p), datetimeFillHint)
		}
		limit = p + width
	}
	end := p
	if end < limit && (source[end] == '+' || source[end] == '-') {
		end++
	}
	digits := end
	for end < limit && source[end] >= '0' && source[end] <= '9' {
		if end%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
		}
		end++
	}
	if end == digits {
		return 0, 0, datetimeError("22007", fmt.Sprintf("invalid value %q for %q", fragment, node.key.word), "Value must be an integer.", "")
	}
	if !variable && end-p < width {
		return 0, 0, datetimeError("22007", fmt.Sprintf("invalid value %q for %q", fragment, node.key.word), fmt.Sprintf("Field requires %d characters, but only %d could be parsed.", width, end-p), datetimeFillHint)
	}
	number, err := strconv.ParseInt(source[p:end], 10, 32)
	if err != nil {
		return 0, 0, datetimeError("22008", fmt.Sprintf("value for %q in source string is out of range", node.key.word), "Value must be in the range -2147483648 to 2147483647.", "")
	}
	*position = end
	return int(number), end - initial, nil
}
func datetimeMatchName(ctx context.Context, source string, position *int, node datetimeNode, names []string, short, reverse, localized, conflict bool) (int, error) {
	if localized {
		// PostgreSQL first searches the localized spelling exactly. Only an
		// unsuccessful exact search demands its first case conversion, upper().
		for index, name := range names {
			if short {
				name = name[:3]
			}
			if strings.HasPrefix(source[*position:], name) {
				*position += len(name)
				return index, nil
			}
		}
		if conflict {
			return 0, datetimeCollationError("upper()")
		}
	}
	for step := range len(names) {
		index := step
		if reverse {
			index = len(names) - 1 - step
		}
		name := names[index]
		if short {
			name = name[:3]
		}
		if len(source)-*position >= len(name) && strings.EqualFold(source[*position:*position+len(name)], name) {
			*position += len(name)
			return index, nil
		}
	}
	fragment := source[*position:]
	for i := range len(fragment) {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		if datetimeSpace(fragment[i]) {
			fragment = fragment[:i]
			break
		}
	}
	return 0, datetimeError("22007", fmt.Sprintf("invalid value %q for %q", fragment, node.key.word), "The given value did not match any of the allowed values for this field.", "")
}
func datetimePartialYear(y int) int {
	switch {
	case y < 70:
		return y + 2000
	case y < 100:
		return y + 1900
	case y < 520:
		return y + 2000
	case y < 1000:
		return y + 1000
	}
	return y
}
func datetimeInt32(value int64) bool { return value >= math.MinInt32 && value <= math.MaxInt32 }
