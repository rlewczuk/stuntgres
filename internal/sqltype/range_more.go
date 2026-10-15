package sqltype

import (
	"context"
	"fmt"
)

var emptyConstructedMultirange multirangePayload

func constructMultirange(result *Descriptor, member rangeDatum) Value {
	if member.flags&rangeEmpty != 0 {
		return Value{typ: result, multirange: &emptyConstructedMultirange}
	}
	// The payload and its one canonical member share a single publication.
	publication := &struct {
		payload multirangePayload
		member [1]rangeDatum
	}{member: [1]rangeDatum{member}}
	publication.payload.members = publication.member[:]
	return Value{typ: result, multirange: &publication.payload}
}

func MultirangeUnnestResult(input *Descriptor) *Descriptor {
	if input == nil || input.IsDomain() || !input.Concrete() || !input.validMultirangeAuthority() {
		return nil
	}
	return input.multirangeRange
}

// MultirangeCursor borrows immutable published members. Position is private to
// one invocation; neither a prepared expression nor its datum owns progress.
type MultirangeCursor struct {
	result *Descriptor
	payload *multirangePayload
	position int
}

func OpenMultirange(ctx context.Context, input Usage, result Usage, value Value) (MultirangeCursor, error) {
	if ctx == nil { return MultirangeCursor{}, fmt.Errorf("missing multirange cursor context") }
	if err := ctx.Err(); err != nil { return MultirangeCursor{}, err }
	if expected := MultirangeUnnestResult(input.Type); expected == nil || result != expected.Usage() || input != input.Type.Usage() || value.typ != input.Type {
		return MultirangeCursor{}, fmt.Errorf("invalid multirange cursor authority")
	}
	cursor := MultirangeCursor{result: result.Type}
	if !value.null {
		if value.multirange == nil { return MultirangeCursor{}, fmt.Errorf("missing multirange cursor payload") }
		cursor.payload = value.multirange
	}
	return cursor, nil
}

func (c *MultirangeCursor) Next(ctx context.Context) (Value, bool, error) {
	if ctx == nil { c.Close(); return Value{}, false, fmt.Errorf("missing multirange cursor context") }
	if err := ctx.Err(); err != nil { c.Close(); return Value{}, false, err }
	if c.result == nil { return Value{}, false, fmt.Errorf("unbound multirange cursor") }
	if c.payload == nil || c.position == len(c.payload.members) {
		c.Close()
		return Null(c.result), false, nil
	}
	value := Value{typ: c.result, rangeDatum: c.payload.members[c.position]}
	c.position++
	return value, true, nil
}

func (c *MultirangeCursor) Close() { c.payload = nil; c.position = 0 }
