package sqltype

import (
	"context"
	"fmt"

	"stuntgres/internal/xml"
)

// XMLAggregateValue is execution-local state over one exact XML descriptor.
// Retained Values never contain this mutable accumulator.
type XMLAggregateValue struct {
	typ *Descriptor
	first Value
	seen bool
	concat xml.ConcatAccumulator
}

func NewXMLAggregateValue(d *Descriptor) (*XMLAggregateValue, error) {
	if d == nil || d.ID() != XML || !d.Concrete() { return nil, fmt.Errorf("invalid XML aggregate descriptor") }
	return &XMLAggregateValue{typ: d}, nil
}
func (a *XMLAggregateValue) Type() *Descriptor { return a.typ }
func (a *XMLAggregateValue) Advance(ctx context.Context, v Value) error {
	if err := ctx.Err(); err != nil { return err }
	if v.Type() != a.typ { return fmt.Errorf("invalid XML aggregate datum descriptor") }
	if v.IsNull() { return nil }
	r, err := v.XML()
	if err != nil { return err }
	if err := a.concat.Append(ctx, r); err != nil { return xmlError(err) }
	if !a.seen { a.first = v; a.seen = true } else { a.first = Value{} }
	return nil
}
func (a *XMLAggregateValue) Finalize(ctx context.Context) (Value, error) {
	if err := ctx.Err(); err != nil { return Value{}, err }
	if !a.seen { return Null(a.typ), nil }
	r, err := a.concat.Finish(ctx)
	if err != nil { return Value{}, xmlError(err) }
	if a.first.typ != nil { return a.first, nil }
	return Value{typ: a.typ, xml: &xmlPayload{result: r}}, nil
}
