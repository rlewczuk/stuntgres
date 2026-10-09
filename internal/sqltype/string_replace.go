package sqltype

import (
	"context"
	"math"
	"strings"
	"stuntgres/internal/sqlstate"
	"unicode/utf8"
)

func stringComparisonCollationError() error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly."}
}

func (p StringPlan) evaluateReplacement(ctx context.Context, texts [4]string, values [4]Value) (Value, error) {
	var text string
	var err error
	switch p.name {
	case "overlay":
		start, e := values[2].Integer()
		if e != nil {
			return Value{}, e
		}
		if start <= 0 {
			return Value{}, failure("22011", "negative substring length not allowed")
		}
		var count int64
		if p.count == 4 {
			count, err = values[3].Integer()
		} else {
			count, err = stringCharacters(ctx, texts[1])
		}
		if err == nil {
			text, err = stringOverlay(ctx, texts[0], texts[1], start, count)
		}
	case "replace":
		// PostgreSQL's empty shortcuts precede the input-collation demand.
		if texts[0] == "" || texts[1] == "" {
			return String(p.result.Type, texts[0]), ctx.Err()
		}
		if p.conflict {
			return Value{}, stringComparisonCollationError()
		}
		text, err = stringReplace(ctx, texts[0], texts[1], texts[2])
	case "translate":
		text, err = stringTranslate(ctx, texts[0], texts[1], texts[2])
	case "starts_with":
		if p.conflict {
			return Value{}, stringComparisonCollationError()
		}
		work := binarySearchWork{ctx: ctx, remaining: 4096}
		yes, e := stringPrefix(texts[0], texts[1], &work)
		if e != nil {
			return Value{}, e
		}
		return Boolean(p.result.Type, yes), nil
	}
	if err != nil {
		return Value{}, err
	}
	return String(p.result.Type, text), nil
}

// stringCharacterCut locates a codepoint boundary without copying/decoding the
// source. Inputs have already passed the type's UTF8/NUL boundary validation.
func stringCharacterCut(ctx context.Context, source string, count int64) (int, error) {
	if count <= 0 {
		return 0, ctx.Err()
	}
	for i := range len(source) {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		if source[i]&0xc0 != 0x80 {
			if count == 0 {
				return i, ctx.Err()
			}
			count--
		}
	}
	return len(source), ctx.Err()
}

func stringOutputSize(size, additional int) (int, error) {
	if additional > int(^uint(0)>>1)-size {
		return 0, failure("54000", "requested length too large")
	}
	return size + additional, nil
}

// All long emission work is bounded too, not just the search/cut work.
func stringWrite(ctx context.Context, out *strings.Builder, text string) error {
	for len(text) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(len(text), 4096)
		out.WriteString(text[:n])
		text = text[n:]
	}
	return ctx.Err()
}

func stringOverlay(ctx context.Context, source, replacement string, start, count int64) (string, error) {
	if start <= 0 {
		return "", failure("22011", "negative substring length not allowed")
	}
	end := start + count
	if end < math.MinInt32 || end > math.MaxInt32 {
		return "", failure("22003", "integer out of range")
	}
	prefix, err := stringCharacterCut(ctx, source, start-1)
	if err != nil {
		return "", err
	}
	suffix, err := stringCharacterCut(ctx, source, max(end, 1)-1)
	if err != nil {
		return "", err
	}
	if replacement == "" && prefix == suffix {
		return source, ctx.Err()
	}
	if prefix == 0 && suffix == len(source) {
		return replacement, ctx.Err()
	}
	if prefix <= suffix && suffix-prefix == len(replacement) {
		work := binarySearchWork{ctx: ctx, remaining: 4096}
		same, err := stringPrefix(source[prefix:suffix], replacement, &work)
		if err != nil {
			return "", err
		}
		if same {
			return source, ctx.Err()
		}
	}
	size, err := stringOutputSize(prefix, len(replacement))
	if err == nil {
		size, err = stringOutputSize(size, len(source)-suffix)
	}
	if err != nil {
		return "", err
	}
	var out strings.Builder
	out.Grow(size)
	for _, part := range [3]string{source[:prefix], replacement, source[suffix:]} {
		if err := stringWrite(ctx, &out, part); err != nil {
			return "", err
		}
	}
	return out.String(), ctx.Err()
}

func stringPrefix(source, prefix string, work *binarySearchWork) (bool, error) {
	if err := work.ctx.Err(); err != nil {
		return false, err
	}
	if len(prefix) > len(source) {
		return false, nil
	}
	for i := range len(prefix) {
		if err := work.step(); err != nil {
			return false, err
		}
		if source[i] != prefix[i] {
			return false, work.ctx.Err()
		}
	}
	return true, work.ctx.Err()
}

func stringReplace(ctx context.Context, source, search, replacement string) (string, error) {
	if source == "" || search == "" {
		return source, ctx.Err()
	}
	if len(search) > len(source) {
		return source, ctx.Err()
	}
	work := binarySearchWork{ctx: ctx, remaining: 4096}
	if len(search) == len(replacement) {
		same, err := stringPrefix(search, replacement, &work)
		if err != nil {
			return "", err
		}
		if same {
			return source, nil
		}
	}
	matcher, err := prepareBinaryMatcher(search, &work)
	if err != nil {
		return "", err
	}
	size, matches, offset := 0, 0, 0
	for offset < len(source) {
		index, err := matcher.position(source[offset:], &work)
		if err != nil {
			return "", err
		}
		if index < 0 {
			break
		}
		size, err = stringOutputSize(size, index)
		if err == nil {
			size, err = stringOutputSize(size, len(replacement))
		}
		if err != nil {
			return "", err
		}
		matches++
		offset += index + len(search)
	}
	if matches == 0 {
		return source, ctx.Err()
	}
	size, err = stringOutputSize(size, len(source)-offset)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	out.Grow(size)
	offset = 0
	for range matches {
		index, err := matcher.position(source[offset:], &work)
		if err != nil {
			return "", err
		}
		if err = stringWrite(ctx, &out, source[offset:offset+index]); err != nil {
			return "", err
		}
		if err = stringWrite(ctx, &out, replacement); err != nil {
			return "", err
		}
		offset += index + len(search)
	}
	if err = stringWrite(ctx, &out, source[offset:]); err != nil {
		return "", err
	}
	return out.String(), ctx.Err()
}

// The first from-codepoint wins. Walk to in lockstep; missing positions delete.
// Returned slices belong to immutable source/to strings, not execution scratch.
func stringTranslateCharacter(original, from, to string, work *binarySearchWork) (string, error) {
	want, _ := utf8.DecodeRuneInString(original)
	toOffset := 0
	for fromOffset := 0; fromOffset < len(from); {
		if err := work.step(); err != nil {
			return "", err
		}
		r, n := utf8.DecodeRuneInString(from[fromOffset:])
		toEnd := toOffset
		if toOffset < len(to) {
			_, width := utf8.DecodeRuneInString(to[toOffset:])
			toEnd += width
		}
		if r == want {
			return to[toOffset:toEnd], nil
		}
		fromOffset += n
		toOffset = toEnd
	}
	return original, nil
}

func stringTranslate(ctx context.Context, source, from, to string) (string, error) {
	if source == "" || from == "" {
		return source, ctx.Err()
	}
	work := binarySearchWork{ctx: ctx, remaining: 4096}
	size, changed := 0, false
	for offset := 0; offset < len(source); {
		if err := work.step(); err != nil {
			return "", err
		}
		_, width := utf8.DecodeRuneInString(source[offset:])
		original := source[offset : offset+width]
		mapped, err := stringTranslateCharacter(original, from, to, &work)
		if err != nil {
			return "", err
		}
		size, err = stringOutputSize(size, len(mapped))
		if err != nil {
			return "", err
		}
		changed = changed || mapped != original
		offset += width
	}
	if !changed {
		return source, ctx.Err()
	}
	var out strings.Builder
	out.Grow(size)
	for offset := 0; offset < len(source); {
		if err := work.step(); err != nil {
			return "", err
		}
		_, width := utf8.DecodeRuneInString(source[offset:])
		mapped, err := stringTranslateCharacter(source[offset:offset+width], from, to, &work)
		if err != nil {
			return "", err
		}
		out.WriteString(mapped) // At most one UTF8 codepoint.
		offset += width
	}
	return out.String(), ctx.Err()
}
