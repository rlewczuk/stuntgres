package functions

import (
	"context"
	"fmt"

	"stuntgres/internal/sqltype"
)

// AggregateContextOrdering borrows the executor's existing tuple comparator and
// cancellable index sorter. Only the selected final support requests sorting.
type AggregateContextOrdering struct {
	Compare func(context.Context, []sqltype.Value, []sqltype.Value, sqltype.InputEnvironment) (int, error)
	Sort    func(context.Context, []int, func(int, int) (int, error)) error
}

// AggregateContextSupport is an immutable recipe for selected native support
// identities, never an aggregate-name dispatch. It contains no execution state.
type AggregateContextSupport struct {
	transitionOID, finalOID uint32
	finalExtra             bool
	hypothetical           bool
	result                 sqltype.Usage
	rows, direct           []sqltype.Usage
	keys                   []AggregateSortKey
}

// orderedSupportState is private to one execution/group. Immutable Value
// payloads are retained in a flat owned tuple vector, not an SQL internal datum.
type orderedSupportState struct {
	owner  *AggregateContextSupport
	values []sqltype.Value
}

func IsAggregateContextSupport(oid uint32) bool {
	return AggregateContextSupportResult(oid) != 0
}

func AggregateContextSupportResult(oid uint32) sqltype.ID {
	switch oid {
	case 3971:
		return sqltype.ID(2281) // internal
	case 3987, 3993:
		return sqltype.Int8
	case 3989, 3991:
		return sqltype.Float8
	}
	return 0
}

func NewAggregateContextSupport(transitionOID, finalOID uint32, finalExtra, hypothetical bool, result sqltype.Usage, rows, direct []sqltype.Usage, keys []AggregateSortKey) (*AggregateContextSupport, error) {
	if transitionOID != 3971 || finalOID == 3971 || !IsAggregateContextSupport(finalOID) || result.Type == nil || result.Type.ID() != AggregateContextSupportResult(finalOID) {
		return nil, fmt.Errorf("stuntgres: invalid aggregate-context support identity")
	}
	if len(rows) == 0 || len(keys) == 0 {
		return nil, fmt.Errorf("stuntgres: aggregate-context support requires ordered inputs")
	}
	for _, usage := range rows {
		if !usage.Type.Concrete() {
			return nil, fmt.Errorf("stuntgres: aggregate-context support requires concrete row types")
		}
	}
	for _, usage := range direct {
		if !usage.Type.Concrete() {
			return nil, fmt.Errorf("stuntgres: aggregate-context support requires concrete direct types")
		}
	}
	return &AggregateContextSupport{
		transitionOID: transitionOID,
		finalOID:      finalOID,
		finalExtra:    finalExtra,
		hypothetical:  hypothetical,
		result:        result,
		rows:          append([]sqltype.Usage(nil), rows...),
		direct:        append([]sqltype.Usage(nil), direct...),
		keys:          append([]AggregateSortKey(nil), keys...),
	}, nil
}

func (p *AggregateContextSupport) TransitionOID() uint32   { return p.transitionOID }
func (p *AggregateContextSupport) FinalOID() uint32        { return p.finalOID }
func (p *AggregateContextSupport) FinalExtra() bool        { return p.finalExtra }
func (p *AggregateContextSupport) Hypothetical() bool      { return p.hypothetical }
func (p *AggregateContextSupport) ResultUsage() sqltype.Usage { return p.result }

// Rows, Direct and Keys are borrowed immutable recipe metadata.
func (p *AggregateContextSupport) Rows() []sqltype.Usage    { return p.rows }
func (p *AggregateContextSupport) Direct() []sqltype.Usage  { return p.direct }
func (p *AggregateContextSupport) Keys() []AggregateSortKey { return p.keys }
func (p *AggregateContextSupport) NewState() any           { return &orderedSupportState{owner: p} }

func (p *AggregateContextSupport) state(state any) (*orderedSupportState, error) {
	s, ok := state.(*orderedSupportState)
	if !ok || s == nil || s.owner != p {
		return nil, fmt.Errorf("stuntgres: invalid aggregate-context support state")
	}
	return s, nil
}

func (p *AggregateContextSupport) Advance(ctx context.Context, state any, rows []sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil {
		return sqlError("XX000", "ordered-set aggregate called in non-aggregate context")
	}
	s, err := p.state(state)
	if err != nil {
		return err
	}
	if len(rows) != len(p.rows) {
		return fmt.Errorf("stuntgres: invalid aggregate-context row width")
	}
	for i, value := range rows {
		if value.Type() != p.rows[i].Type {
			return fmt.Errorf("stuntgres: invalid aggregate-context row type")
		}
	}
	// Do not sort, filter NULLs, or retain the caller's borrowed slice.
	s.values = append(s.values, rows...)
	return ctx.Err()
}

func (p *AggregateContextSupport) Finalize(ctx context.Context, state any, direct []sqltype.Value, env sqltype.InputEnvironment, order AggregateContextOrdering) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, err := p.state(state)
	if err != nil {
		return sqltype.Value{}, err
	}
	if len(direct) != len(p.direct) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid aggregate-context direct width")
	}
	for i, value := range direct {
		if value.Type() != p.direct[i].Type {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid aggregate-context direct type")
		}
	}
	width := len(p.rows)
	count := len(s.values) / width
	operation := AggregateHypotheticalRank
	switch p.finalOID {
	case 3993:
		operation = AggregateHypotheticalDenseRank
	case 3989:
		operation = AggregateHypotheticalPercentRank
	case 3991:
		operation = AggregateHypotheticalCumeDist
	}
	in := OrderedInput{Values: s.values, Width: width, Target: direct, Compare: order.Compare}
	// PG's real finals return the empty defaults before argument checks.
	if count != 0 {
		nargs := len(direct)
		if p.finalExtra {
			nargs += width
		}
		if nargs%2 != 0 {
			return sqltype.Value{}, sqlError("XX000", "wrong number of arguments in hypothetical-set function")
		}
		if !p.hypothetical || nargs/2 != width || len(direct) != width {
			return sqltype.Value{}, sqlError("XX000", "type mismatch in hypothetical-set function")
		}
		for i := range p.rows {
			if p.rows[i].Type.ID() != p.direct[i].Type.ID() {
				return sqltype.Value{}, sqlError("XX000", "type mismatch in hypothetical-set function")
			}
		}
		if order.Compare == nil || order.Sort == nil {
			return sqltype.Value{}, fmt.Errorf("stuntgres: aggregate-context final requires tuple ordering")
		}
		in.Indices = make([]int, count)
		for i := range in.Indices {
			in.Indices[i] = i
		}
		if err := order.Sort(ctx, in.Indices, func(a, b int) (int, error) {
			return order.Compare(ctx, s.values[a*width:(a+1)*width], s.values[b*width:(b+1)*width], env)
		}); err != nil {
			return sqltype.Value{}, err
		}
	}
	// Genuine support final and builtin provider share only the pure algorithm.
	return finalizeHypotheticalTuple(ctx, p.result, operation, in, env)
}
