package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

// A provider is immutable; each execution/group owns just one retained datum.
// Comparison is demanded only after the first eligible candidate.
type ExtremaProvider struct {
	usage      sqltype.Usage
	comparison sqltype.Comparison
	maximum    bool
}
type extremaState struct{ value sqltype.Value }

func (p ExtremaProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p ExtremaProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.usage.Type.Concrete() || p.comparison.SessionDependent() {
		return nil, fmt.Errorf("stuntgres: invalid extrema result type")
	}
	return &extremaState{value: sqltype.Null(p.usage.Type)}, nil
}
func (p ExtremaProvider) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*extremaState)
	if !ok || s == nil || !p.usage.Type.Concrete() || s.value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid extrema state")
	}
	if value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid extrema datum")
	}
	if value.IsNull() {
		return nil
	}
	if s.value.IsNull() {
		s.value = value
		return ctx.Err()
	}
	sign, err := p.comparison.Compare(ctx, value, s.value, sqltype.InputEnvironment{})
	if err != nil {
		return err
	}
	// BPCHAR's release transition retains the prior equal representation. Other
	// strict reducers choose the incoming datum (scale, interval fields, ±0).
	if sign == 0 && p.usage.Type.ID() != sqltype.BPChar || p.maximum && sign > 0 || !p.maximum && sign < 0 {
		s.value = value
	}
	return ctx.Err()
}
func (p ExtremaProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*extremaState)
	if !ok || s == nil || !p.usage.Type.Concrete() || s.value.Type() != p.usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid extrema state")
	}
	return s.value, nil
}

func NewExtremaProvider(usage sqltype.Usage, comparison sqltype.Comparison, maximum bool) ExtremaProvider {
	return ExtremaProvider{usage, comparison, maximum}
}
func (p ExtremaProvider) Maximum() bool { return p.maximum }

func (p ExtremaProvider) Comparison() sqltype.Comparison { return p.comparison }
