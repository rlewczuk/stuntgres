package sqltype

import (
	"context"
	"strings"
	"stuntgres/internal/sqlstate"
	"unicode/utf8"
)

// StringIntegerArgument identifies numeric roles, excluding TEXT conversion.
func StringIntegerArgument(name string, index int) bool {
	switch name {
	case "chr":
		return index == 0
	case "overlay":
		return index >= 2
	case "split_part":
		return index == 2
	case "lpad", "rpad", "repeat", "left", "right":
		return index == 1
	case "substring", "substr":
		return index == 1 || index == 2
	default:
		return StringBaseConversion(name) && index == 0
	}
}

// PostgreSQL's varlena allocation includes a four-byte header. Padding reserves
// UTF8's maximum width, rather than the bytes actually emitted by this input.
func stringPadSize(width, count int64) error {
	const maxPayload = int64(0x3fffffff - 4)
	if width > 0 && count > maxPayload/width {
		return failure("54000", "requested length too large")
	}
	return nil
}

func (p StringPlan) evaluatePadding(ctx context.Context, texts [4]string, values [4]Value) (Value, error) {
	var text string
	var err error
	if p.name == "reverse" {
		text, err = stringReverse(ctx, texts[0], false)
	} else {
		count, e := values[1].Integer()
		if e != nil {
			return Value{}, e
		}
		if p.name == "repeat" {
			text, err = stringRepeat(ctx, texts[0], count)
		} else {
			fill := " "
			if p.count == 3 {
				fill = texts[2]
			}
			text, err = stringPad(ctx, texts[0], fill, count, p.name == "lpad")
		}
	}
	if err != nil {
		if p.count == 2 && (p.name == "lpad" || p.name == "rpad") {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "54000" {
				copy := *diagnostic
				copy.Context = "SQL function \"" + p.name + "\" statement 1"
				err = &copy
			}
		}
		return Value{}, err
	}
	return String(p.result.Type, text), nil
}

func stringPad(ctx context.Context, source, fill string, target int64, left bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	target = max(target, 0)
	if target == 0 {
		return "", nil
	}
	characters, err := stringCharacters(ctx, source)
	if err != nil {
		return "", err
	}
	if characters > target {
		cut, err := stringCharacterCut(ctx, source, target)
		if err != nil {
			return "", err
		}
		source = source[:cut]
		characters = target
	}
	effective := target
	if fill == "" {
		effective = characters
	}
	if err := stringPadSize(4, effective); err != nil {
		return "", err
	}
	missing := target - characters
	if missing == 0 || fill == "" {
		return source, ctx.Err()
	}
	fillCharacters, err := stringCharacters(ctx, fill)
	if err != nil {
		return "", err
	}
	whole, remainder := missing/fillCharacters, missing%fillCharacters
	prefix, err := stringCharacterCut(ctx, fill, remainder)
	if err != nil {
		return "", err
	}
	size := int64(len(source)) + whole*int64(len(fill)) + int64(prefix)
	var out strings.Builder
	out.Grow(int(size))
	if !left {
		if err := stringWrite(ctx, &out, source); err != nil {
			return "", err
		}
	}
	for i := int64(0); i < whole; i++ {
		if err := stringWrite(ctx, &out, fill); err != nil {
			return "", err
		}
	}
	if err := stringWrite(ctx, &out, fill[:prefix]); err != nil {
		return "", err
	}
	if left {
		if err := stringWrite(ctx, &out, source); err != nil {
			return "", err
		}
	}
	return out.String(), ctx.Err()
}

func stringRepeat(ctx context.Context, source string, count int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	count = max(count, 0)
	if err := stringPadSize(int64(len(source)), count); err != nil {
		return "", err
	}
	if count == 0 || source == "" {
		return "", ctx.Err()
	}
	if count == 1 {
		return source, ctx.Err()
	}
	var out strings.Builder
	out.Grow(len(source) * int(count))
	for i := int64(0); i < count; i++ {
		if err := stringWrite(ctx, &out, source); err != nil {
			return "", err
		}
	}
	return out.String(), ctx.Err()
}

// The binary branch reverses octets, while text retains each codepoint's exact
// original encoding. Neither branch normalizes or allocates a rune array.
func stringReverse(ctx context.Context, source string, binary bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(source) <= 1 {
		return source, nil
	}
	if !binary {
		_, size := utf8.DecodeRuneInString(source)
		if size == len(source) {
			return source, nil
		}
	}
	var out strings.Builder
	out.Grow(len(source))
	work := 0
	for end := len(source); end > 0; {
		if work&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		size := 1
		if !binary {
			_, size = utf8.DecodeLastRuneInString(source[:end])
		}
		out.WriteString(source[end-size : end])
		end -= size
		work++
	}
	return out.String(), ctx.Err()
}
