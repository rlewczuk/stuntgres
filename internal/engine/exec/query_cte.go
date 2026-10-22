package exec

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Owners form an invocation chain. A correlated query gets a new local owner;
// enclosing definitions still find their existing producer through that chain.
type CTEInvocation struct {
	Frame     uint32
	Parent    *CTEInvocation
	State     State
	Producers map[*plan.CTEDefinition]*CTEProducer
	Order     []*CTEProducer
	Closed    bool
	failure   error
}
type CTEProducer struct {
	state                State
	project              *plan.ProjectNode
	Inner                Stage
	Source               *Source
	Opened, Done, Closed bool
	Affected             int64 // Completed modifier count, never added to primary results.
	failure              error
	Recursion            *recursiveUnionStage
}

func (o *CTEInvocation) Close() error {
	if o.Closed {
		return o.failure
	}
	o.Closed = true
	for _, producer := range o.Order {
		if err := producer.close(); o.failure == nil {
			o.failure = err
		}
	}
	return o.failure
}
func (p *CTEProducer) close() error {
	if p.Closed {
		return p.failure
	}
	p.Closed = true
	if p.Opened {
		if err := p.Inner.Close(); p.failure == nil {
			p.failure = err
		}
	}
	return p.failure
}
func (p *CTEProducer) Fail(err error) error {
	if p.failure == nil {
		p.failure = err
	}
	_ = p.close()
	return p.failure
}

func (p *CTEProducer) Ensure(ctx context.Context, index int) (bool, error) {
	if p.failure != nil {
		return false, p.failure
	}
	if err := ctx.Err(); err != nil {
		return false, p.Fail(err)
	}
	if index < p.Source.FlatCount {
		return true, nil
	}
	if p.Done {
		return false, nil
	}
	if !p.Opened {
		inner, err := OpenQueryProjectStage(ctx, &p.state, p.project)
		if err != nil {
			return false, p.Fail(err)
		}
		p.Inner, p.Opened = inner, true
	}
	for index >= p.Source.FlatCount && !p.Done {
		i, present, err := p.Inner.NextOccurrence(ctx)
		if err != nil {
			return false, p.Fail(err)
		}
		if !present {
			p.Done = true
			if err := p.close(); err != nil {
				return false, err
			}
			break
		}
		// Recursive output and worktable generations already borrow this
		// producer's append-only tuples; do not evaluate or copy them again.
		if p.Recursion != nil {
			continue
		}
		p.Inner.Evaluation.setContext(ctx)
		p.Inner.Evaluation.BindQueryRow(p.Inner.Source, p.Inner.Index(i))
		p.Inner.Evaluation.OrderValues = p.Inner.OrderRow(i)
		start := len(p.Source.Flat)
		p.Source.Flat = append(p.Source.Flat, make([]sqltype.Value, len(p.project.Schema()))...)
		for j, expr := range p.Inner.Expressions {
			value, err := expr.Eval(&p.Inner.Evaluation)
			if err == nil {
				err = ValidateProducedValue(value, p.project.Schema()[j].Usage)
			}
			if err != nil {
				p.Source.Flat = p.Source.Flat[:start]
				return false, p.Fail(err)
			}
			p.Source.Flat[start+j] = value
		}
		if err := ctx.Err(); err != nil {
			p.Source.Flat = p.Source.Flat[:start]
			return false, p.Fail(err)
		}
		p.Source.FlatCount++
	}
	return index < p.Source.FlatCount, nil
}

func OpenCTEScan(ctx context.Context, state *State, n *plan.DerivedNode) (Stage, error) {
	owner := state.Ctes
	for owner != nil && owner.Frame != n.CTE.Owner {
		owner = owner.Parent
	}
	if owner == nil || owner.Closed {
		return Stage{}, fmt.Errorf("stuntgres: missing CTE invocation owner")
	}
	if owner.Producers == nil {
		owner.Producers = make(map[*plan.CTEDefinition]*CTEProducer)
	}
	producer := owner.Producers[n.CTE]
	if producer == nil {
		if n.CTE.Mutation != nil {
			return Stage{}, fmt.Errorf("stuntgres: modifying CTE has not completed")
		}
		project, ok := n.Query.Root.(*plan.ProjectNode)
		if !ok {
			return Stage{}, fmt.Errorf("stuntgres: CTE lacks projection")
		}
		innerState := owner.State
		innerState.SourceMode = owner.State.subquerySourceMode()
		innerState.Ctes = owner
		innerState.derived = nil
		producer = &CTEProducer{state: innerState, project: project, Source: &Source{FlatWidth: len(n.Schema()), Materialized: true, Authority: state.Source.Authority, Host: state.Source.Host, Locks: state.Source.Locks}}
		owner.Producers[n.CTE] = producer
		owner.Order = append(owner.Order, producer)
	}
	source := producer.Source
	if n.PresenceSlot != 0 {
		// Presence belongs to this reader occurrence, not the shared producer.
		// Borrow immutable admitted tuples; no row values are copied.
		source = &Source{Flat: producer.Source.Flat, FlatWidth: producer.Source.FlatWidth, FlatCount: producer.Source.FlatCount, Materialized: true, Authority: producer.Source.Authority, Host: producer.Source.Host, Locks: producer.Source.Locks}
		EstablishSourcePresence(source, n.PresenceSlot)
	}
	out := Stage{Source: source, Columns: n.Schema(), Selection: []int{}, onFailure: producer.Fail}
	out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
		index := len(out.Selection)
		present, err := producer.Ensure(ctx, index)
		if err != nil || !present {
			return present, err
		}
		if source != producer.Source {
			source.Flat, source.FlatCount = producer.Source.Flat, producer.Source.FlatCount
		}
		out.Selection = append(out.Selection, index)
		return true, nil
	}
	// Reader closure does not own or erase the producer. The owner closes it
	// after all scalar readers/projection, even when an earlier reader stops.
	return out, nil
}
