package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Owners form an invocation chain. A correlated query gets a new local owner;
// enclosing definitions still find their existing producer through that chain.
type cteInvocation struct {
	frame     uint32
	parent    *cteInvocation
	state     queryState
	producers map[*plan.CTEDefinition]*cteProducer
	order     []*cteProducer
	closed    bool
	failure   error
}
type cteProducer struct {
	state                queryState
	project              *plan.ProjectNode
	inner                indexedStage
	source               *querySource
	opened, done, closed bool
	affected             int64 // Completed modifier count, never added to primary results.
	failure              error
	recursion            *recursiveUnionStage
}

func (o *cteInvocation) close() error {
	if o.closed {
		return o.failure
	}
	o.closed = true
	for _, producer := range o.order {
		if err := producer.close(); o.failure == nil {
			o.failure = err
		}
	}
	return o.failure
}
func (p *cteProducer) close() error {
	if p.closed {
		return p.failure
	}
	p.closed = true
	if p.opened {
		if err := p.inner.Close(); p.failure == nil {
			p.failure = err
		}
	}
	return p.failure
}
func (p *cteProducer) fail(err error) error {
	if p.failure == nil {
		p.failure = err
	}
	_ = p.close()
	return p.failure
}

func (p *cteProducer) ensure(ctx context.Context, index int) (bool, error) {
	if p.failure != nil {
		return false, p.failure
	}
	if err := ctx.Err(); err != nil {
		return false, p.fail(err)
	}
	if index < p.source.flatCount {
		return true, nil
	}
	if p.done {
		return false, nil
	}
	if !p.opened {
		inner, err := openQueryProjectStage(ctx, &p.state, p.project)
		if err != nil {
			return false, p.fail(err)
		}
		p.inner, p.opened = inner, true
	}
	for index >= p.source.flatCount && !p.done {
		i, present, err := p.inner.nextOccurrence(ctx)
		if err != nil {
			return false, p.fail(err)
		}
		if !present {
			p.done = true
			if err := p.close(); err != nil {
				return false, err
			}
			break
		}
		// Recursive output and worktable generations already borrow this
		// producer's append-only tuples; do not evaluate or copy them again.
		if p.recursion != nil {
			continue
		}
		p.inner.evaluation.setContext(ctx)
		p.inner.evaluation.bindQueryRow(p.inner.source, p.inner.index(i))
		p.inner.evaluation.orderValues = p.inner.orderRow(i)
		start := len(p.source.flat)
		p.source.flat = append(p.source.flat, make([]sqltype.Value, len(p.project.Schema()))...)
		for j, expr := range p.inner.expressions {
			value, err := expr.Eval(&p.inner.evaluation)
			if err == nil {
				err = validateProducedValue(value, p.project.Schema()[j].Usage)
			}
			if err != nil {
				p.source.flat = p.source.flat[:start]
				return false, p.fail(err)
			}
			p.source.flat[start+j] = value
		}
		if err := ctx.Err(); err != nil {
			p.source.flat = p.source.flat[:start]
			return false, p.fail(err)
		}
		p.source.flatCount++
	}
	return index < p.source.flatCount, nil
}

func openCTEScan(ctx context.Context, state *queryState, n *plan.DerivedNode) (indexedStage, error) {
	owner := state.ctes
	for owner != nil && owner.frame != n.CTE.Owner {
		owner = owner.parent
	}
	if owner == nil || owner.closed {
		return indexedStage{}, fmt.Errorf("stuntgres: missing CTE invocation owner")
	}
	if owner.producers == nil {
		owner.producers = make(map[*plan.CTEDefinition]*cteProducer)
	}
	producer := owner.producers[n.CTE]
	if producer == nil {
		if n.CTE.Mutation != nil {
			return indexedStage{}, fmt.Errorf("stuntgres: modifying CTE has not completed")
		}
		project, ok := n.Query.Root.(*plan.ProjectNode)
		if !ok {
			return indexedStage{}, fmt.Errorf("stuntgres: CTE lacks projection")
		}
		innerState := owner.state
		innerState.ctes = owner
		innerState.derived = nil
		producer = &cteProducer{state: innerState, project: project, source: &querySource{flatWidth: len(n.Schema()), materialized: true, authority: state.source.authority, scope: state.source.scope, locks: state.source.locks}}
		owner.producers[n.CTE] = producer
		owner.order = append(owner.order, producer)
	}
	source := producer.source
	if n.PresenceSlot != 0 {
		// Presence belongs to this reader occurrence, not the shared producer.
		// Borrow immutable admitted tuples; no row values are copied.
		source = &querySource{flat: producer.source.flat, flatWidth: producer.source.flatWidth, flatCount: producer.source.flatCount, materialized: true, authority: producer.source.authority, scope: producer.source.scope, locks: producer.source.locks}
		establishSourcePresence(source, n.PresenceSlot)
	}
	out := indexedStage{source: source, schema: n.Schema(), selection: []int{}, onFailure: producer.fail}
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		index := len(out.selection)
		present, err := producer.ensure(ctx, index)
		if err != nil || !present {
			return present, err
		}
		if source != producer.source {
			source.flat, source.flatCount = producer.source.flat, producer.source.flatCount
		}
		out.selection = append(out.selection, index)
		return true, nil
	}
	// Reader closure does not own or erase the producer. The owner closes it
	// after all scalar readers/projection, even when an earlier reader stops.
	return out, nil
}
