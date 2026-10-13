package sqltype

import (
	"context"
	"fmt"
)

// jsonArrayLength scans validated retained JSON without decoding strings or
// constructing a tree. Only starts of elements at the root array count.
func jsonArrayLength(ctx context.Context, source string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	l := jsonLexer{ctx: ctx, text: source, line: 1}
	if err := l.next(); err != nil {
		return 0, err
	}
	switch l.kind {
	case '[':
	case '{':
		return 0, failure("22023", "cannot get array length of a non-array")
	case 's', 'n', 't', 'f', 'z':
		return 0, failure("22023", "cannot get array length of a scalar")
	default:
		return 0, fmt.Errorf("invalid retained JSON root")
	}
	depth := 1
	var count int64
	for depth > 0 {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if err := l.next(); err != nil {
			return 0, err
		}
		switch l.kind {
		case 0:
			return 0, fmt.Errorf("incomplete retained JSON array")
		case ']', '}':
			if depth == 1 && l.kind != ']' {
				return 0, fmt.Errorf("invalid retained JSON array end")
			}
			depth--
		case ',', ':':
		default:
			if depth == 1 {
				count++
			}
			if l.kind == '[' || l.kind == '{' {
				depth++
			}
		}
	}
	if err := l.next(); err != nil {
		return 0, err
	}
	if l.kind != 0 {
		return 0, fmt.Errorf("trailing retained JSON data")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return count, nil
}
