package sqltype

import (
	"context"
	"fmt"
	"stuntgres/internal/sqlstate"
)

// trimStringPadding returns an immutable view and removes only U+0020.
func trimStringPadding(ctx context.Context, text string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	end := len(text)
	for end > 0 && text[end-1] == ' ' {
		end--
		if (len(text)-end)&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
	}
	return text[:end], ctx.Err()
}

// Input validation owns UTF8 validity. Counting leading bytes is sufficient
// here and requires neither decoded rune storage nor a copied prefix.
func stringCharacters(ctx context.Context, text string) (int64, error) {
	var count int64
	for i := range len(text) {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		if text[i]&0xc0 != 0x80 {
			count++
		}
	}
	return count, ctx.Err()
}

func stringPosition(ctx context.Context, source, needle string) (int64, error) {
	index, err := binaryPosition(ctx, source, needle)
	if err != nil {
		return 0, err
	}
	if index < 0 {
		return 0, ctx.Err()
	}
	prefix, err := stringCharacters(ctx, source[:index])
	return prefix + 1, err
}

func (p StringPlan) evaluateLength(ctx context.Context, texts [4]string) (Value, error) {
	if p.name == "position" || p.name == "strpos" {
		if p.conflict {
			return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly."}
		}
		index, err := stringPosition(ctx, texts[0], texts[1])
		if err != nil {
			return Value{}, err
		}
		return Integer(p.result.Type, index)
	}
	text := texts[0]
	switch p.name {
	case "length", "char_length", "character_length":
		var err error
		if p.formal[0].Type.ID() == BPChar {
			text, err = trimStringPadding(ctx, text)
			if err != nil {
				return Value{}, err
			}
		}
		count, err := stringCharacters(ctx, text)
		if err != nil {
			return Value{}, err
		}
		return Integer(p.result.Type, count)
	case "octet_length", "bit_length":
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		return integerLength(p.result.Type, int64(len(text)), p.name == "bit_length")
	default:
		return Value{}, fmt.Errorf("invalid string measure operation")
	}
}
