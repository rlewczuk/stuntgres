package sqltype

import (
	"context"
	"fmt"
	"unicode/utf8"
)

// StringFieldCursor owns invocation state only. Returned values reference immutable
// validated TEXT strings; present NULL and exhausted are separate results.
type StringFieldCursor struct {
	source, delimiter, marker          string
	element                            *Descriptor
	matcher                            binaryMatcher
	work                               binarySearchWork
	offset                             int
	characters, replaceNull, exhausted bool
}

func newStringFieldCursor(ctx context.Context, source, delimiter string, characters bool) (StringFieldCursor, error) {
	c := StringFieldCursor{source: source, delimiter: delimiter, characters: characters, exhausted: source == "", work: binarySearchWork{ctx: ctx, remaining: 4096}}
	if err := ctx.Err(); err != nil {
		return c, err
	}
	if !characters && delimiter != "" && source != "" {
		var err error
		c.matcher, err = prepareBinaryMatcher(delimiter, &c.work)
		if err != nil {
			return c, err
		}
	}
	return c, nil
}
func (c *StringFieldCursor) nextField(ctx context.Context) (string, bool, error) {
	c.work.ctx = ctx
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if c.exhausted {
		return "", false, nil
	}
	if err := c.work.step(); err != nil {
		return "", false, err
	}
	start := c.offset
	if c.characters {
		_, n := utf8.DecodeRuneInString(c.source[start:])
		c.offset += n
		c.exhausted = c.offset == len(c.source)
		return c.source[start:c.offset], true, nil
	}
	if c.delimiter == "" {
		c.exhausted = true
		return c.source, true, nil
	}
	index, err := c.matcher.position(c.source[start:], &c.work)
	if err != nil {
		return "", false, err
	}
	if index < 0 {
		c.exhausted = true
		return c.source[start:], true, nil
	}
	c.offset = start + index + len(c.delimiter)
	return c.source[start : start+index], true, nil
}
func (c *StringFieldCursor) Next(ctx context.Context) (Value, bool, error) {
	field, present, err := c.nextField(ctx)
	if err != nil || !present {
		return Value{}, present, err
	}
	if c.replaceNull && len(field) == len(c.marker) {
		equal, err := stringPrefix(field, c.marker, &c.work)
		if err != nil {
			return Value{}, false, err
		}
		if equal {
			return Null(c.element), true, nil
		}
	}
	return String(c.element, field), true, nil
}
func (c *StringFieldCursor) Close() {
	if c != nil {
		*c = StringFieldCursor{exhausted: true}
	}
}

func splitPart(ctx context.Context, source, delimiter string, index int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if index == 0 {
		return "", failure("22023", "field position must not be zero")
	}
	if source == "" {
		return "", nil
	}
	cursor, err := newStringFieldCursor(ctx, source, delimiter, false)
	if err != nil {
		return "", err
	}
	if index == -1 {
		last := ""
		for {
			field, present, err := cursor.nextField(ctx)
			if err != nil {
				return "", err
			}
			if !present {
				return last, nil
			}
			last = field
		}
	}
	if index < 0 {
		var count int64
		for {
			_, present, err := cursor.nextField(ctx)
			if err != nil {
				return "", err
			}
			if !present {
				break
			}
			count++
		}
		index = count + index + 1
		if index <= 0 {
			return "", nil
		}
		cursor.offset, cursor.exhausted = 0, false
	}
	for fieldNumber := int64(1); ; fieldNumber++ {
		field, present, err := cursor.nextField(ctx)
		if err != nil {
			return "", err
		}
		if !present {
			return "", nil
		}
		if fieldNumber == index {
			return field, nil
		}
	}
}

// Conversion is shared by scalar and table producers, with typed NULLs preserved.
func (p StringPlan) stringInputs(ctx context.Context, values [4]Value, input InputEnvironment, output OutputEnvironment) (texts [4]string, err error) {
	for i := range int(p.count) {
		v := values[i]
		if v.Type() != p.args[i].Type {
			return texts, fmt.Errorf("foreign string input value")
		}
		if v.IsNull() || p.formal[i].Type.ID() == Int4 || p.formal[i].Type.ID() == Int8 || p.name == "parse_ident" && !p.textual[i] {
			continue
		}
		if p.name == "to_ascii" && i == 1 {
			texts[i], err = v.Text()
			if err != nil {
				return texts, err
			}
			continue
		}
		if v.Type().BaseType().ID() == BPChar && p.formal[i].Type.ID() == Text {
			v.text, err = trimStringPadding(ctx, v.text)
			if err != nil {
				return texts, err
			}
		}
		if p.textual[i] && v.Type().ID() != Char {
			v, err = p.conversions[i].Apply(ctx, v, input, output)
			if err == nil {
				texts[i], err = v.Text()
			}
		} else {
			texts[i], err = v.Type().ConcatOutput(ctx, v, output)
		}
		if err != nil {
			return texts, err
		}
	}
	return texts, ctx.Err()
}
func (p StringPlan) splitCursor(ctx context.Context, texts [4]string, values [4]Value) (StringFieldCursor, error) {
	if p.conflict && !values[0].IsNull() && texts[0] != "" && (!values[1].IsNull() && texts[1] != "" || p.count == 3 && !values[2].IsNull()) {
		return StringFieldCursor{}, stringComparisonCollationError()
	}
	c, err := newStringFieldCursor(ctx, texts[0], texts[1], values[1].IsNull())
	if err != nil {
		return c, err
	}
	c.element = p.text.Type
	c.marker = texts[2]
	c.replaceNull = p.count == 3 && !values[2].IsNull()
	if values[0].IsNull() {
		c.exhausted = true
	}
	return c, nil
}
func (p StringPlan) OpenStringTable(ctx context.Context, values [4]Value, input InputEnvironment, output OutputEnvironment) (StringFieldCursor, error) {
	if p.name != "string_to_table" {
		return StringFieldCursor{}, fmt.Errorf("not a string table plan")
	}
	texts, err := p.stringInputs(ctx, values, input, output)
	if err != nil {
		return StringFieldCursor{}, err
	}
	return p.splitCursor(ctx, texts, values)
}
func splitArray(ctx context.Context, p StringPlan, texts [4]string, values [4]Value) (Value, error) {
	cursor, err := p.splitCursor(ctx, texts, values)
	if err != nil {
		return Value{}, err
	}
	// Count without buffering fields; allocate the owned element payload once.
	count := 0
	for {
		_, present, err := cursor.nextField(ctx)
		if err != nil {
			return Value{}, err
		}
		if !present {
			break
		}
		if count == maxArrayItems {
			return Value{}, arraySizeFailure()
		}
		count++
	}
	if count == 0 {
		return OwnedArrayValue(p.result.Type, nil, nil)
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	cursor.offset, cursor.exhausted = 0, false
	elements := make([]Value, count)
	for i := range elements {
		v, present, err := cursor.Next(ctx)
		if err != nil {
			return Value{}, err
		}
		if !present {
			return Value{}, fmt.Errorf("string split cardinality changed")
		}
		elements[i] = v
	}
	return OwnedArrayValue(p.result.Type, []ArrayDimension{{Lower: 1, Length: int32(count)}}, elements)
}
