package functions

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// XMLAggregateProvider has no partial or transport capability. Its only retained
// authority is the immutable result usage; each group owns a fresh accumulator.
type XMLAggregateProvider struct { usage sqltype.Usage }

func NewXMLAggregateProvider(usage sqltype.Usage) XMLAggregateProvider {
	return XMLAggregateProvider{usage: usage}
}
func (p XMLAggregateProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p XMLAggregateProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil { return nil, err }
	return sqltype.NewXMLAggregateValue(p.usage.Type)
}
func (p XMLAggregateProvider) state(state any) (*sqltype.XMLAggregateValue, error) {
	s, ok := state.(*sqltype.XMLAggregateValue)
	if !ok || s == nil || s.Type() != p.usage.Type { return nil, fmt.Errorf("stuntgres: invalid XML aggregate state") }
	return s, nil
}
func (p XMLAggregateProvider) Advance(ctx context.Context, state any, values []sqltype.Value, _ expr.Context) error {
	if err := ctx.Err(); err != nil { return err }
	s, err := p.state(state)
	if err != nil { return err }
	if len(values) != 1 { return fmt.Errorf("stuntgres: invalid XML aggregate vector") }
	return s.Advance(ctx, values[0])
}
func (p XMLAggregateProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil { return sqltype.Value{}, err }
	s, err := p.state(state)
	if err != nil { return sqltype.Value{}, err }
	return s.Finalize(ctx)
}
func (p XMLAggregateProvider) Remap(r ProviderRemap) (AggregateProvider, error) {
	usage, err := r.Usage(p.usage)
	if err != nil { return nil, err }
	return NewXMLAggregateProvider(usage), nil
}
