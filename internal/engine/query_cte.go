package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

// Owners form an invocation chain. A correlated query gets a new local owner;
// enclosing definitions still find their existing producer through that chain.
type cteInvocation struct {
	frame     uint32
	parent    *cteInvocation
	state     queryState
	producers map[*cteDefinition]*cteProducer
	order     []*cteProducer
	closed    bool
	failure   error
}
type cteProducer struct {
	state                queryState
	project              *projectNode
	inner                indexedStage
	source               *querySource
	opened, done, closed bool
	failure              error
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
		p.inner.evaluation.setContext(ctx)
		p.inner.evaluation.Row = p.inner.source.row(p.inner.index(i))
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

func openCTEScan(ctx context.Context, state *queryState, n *derivedNode) (indexedStage, error) {
	owner := state.ctes
	for owner != nil && owner.frame != n.cte.owner {
		owner = owner.parent
	}
	if owner == nil || owner.closed {
		return indexedStage{}, fmt.Errorf("stuntgres: missing CTE invocation owner")
	}
	if owner.producers == nil {
		owner.producers = make(map[*cteDefinition]*cteProducer)
	}
	producer := owner.producers[n.cte]
	if producer == nil {
		project, ok := n.query.Root.(*projectNode)
		if !ok {
			return indexedStage{}, fmt.Errorf("stuntgres: CTE lacks projection")
		}
		innerState := owner.state
		innerState.ctes = owner
		innerState.derived = nil
		producer = &cteProducer{state: innerState, project: project, source: &querySource{flatWidth: len(n.Schema()), materialized: true, authority: state.source.authority, scope: state.source.scope, locks: state.source.locks}}
		owner.producers[n.cte] = producer
		owner.order = append(owner.order, producer)
	}
	out := indexedStage{source: producer.source, schema: n.Schema(), selection: []int{}}
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		index := len(out.selection)
		present, err := producer.ensure(ctx, index)
		if err != nil || !present {
			return present, err
		}
		out.selection = append(out.selection, index)
		return true, nil
	}
	// Reader closure does not own or erase the producer. The owner closes it
	// after all scalar readers/projection, even when an earlier reader stops.
	return out, nil
}
