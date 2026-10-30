package exec

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

type foreignComposite struct {
	request      catalog.ForeignQueryRequest
	plan         catalog.ForeignQueryPlan
	width, cache int
}

// CopyForeignQueryRequest detaches all engine-owned metadata. Provider Private
// values are immutable by contract and are never traversed or serialized.
func CopyForeignQueryRequest(r catalog.ForeignQueryRequest) catalog.ForeignQueryRequest {
	r.Parameters.Values = CopyForeignCells(r.Parameters.Values)
	r.Parameters.Correlation = CopyForeignCells(r.Parameters.Correlation)
	r.Sources = slices.Clone(r.Sources)
	for i := range r.Sources {
		r.Sources[i].Request = CopyForeignRequest(r.Sources[i].Request)
	}
	r.Stage = copyForeignStage(r.Stage)
	return r
}

func copyForeignExpression(e catalog.ForeignQueryExpression) catalog.ForeignQueryExpression {
	e.Arguments = slices.Clone(e.Arguments)
	for i := range e.Arguments {
		e.Arguments[i] = copyForeignExpression(e.Arguments[i])
	}
	if b, ok := e.Literal.([]byte); ok {
		e.Literal = slices.Clone(b)
	}
	return e
}

func copyForeignStage(s catalog.ForeignQueryStage) catalog.ForeignQueryStage {
	s.Children = slices.Clone(s.Children)
	for i := range s.Children {
		s.Children[i] = copyForeignStage(s.Children[i])
	}
	s.Outputs = slices.Clone(s.Outputs)
	for i := range s.Outputs {
		s.Outputs[i] = copyForeignExpression(s.Outputs[i])
	}
	s.Slots = slices.Clone(s.Slots)
	if s.Predicate != nil {
		v := copyForeignExpression(*s.Predicate)
		s.Predicate = &v
	}
	s.Keys = slices.Clone(s.Keys)
	for i := range s.Keys {
		s.Keys[i] = copyForeignExpression(s.Keys[i])
	}
	s.Order = slices.Clone(s.Order)
	for i := range s.Order {
		s.Order[i].Expression = copyForeignExpression(s.Order[i].Expression)
	}
	s.Aggregates = slices.Clone(s.Aggregates)
	for i := range s.Aggregates {
		s.Aggregates[i].Arguments = slices.Clone(s.Aggregates[i].Arguments)
		for j := range s.Aggregates[i].Arguments {
			s.Aggregates[i].Arguments[j] = copyForeignExpression(s.Aggregates[i].Arguments[j])
		}
	}
	if s.Count != nil {
		v := *s.Count
		s.Count = &v
	}
	return s
}

func validateForeignQueryPath(request catalog.ForeignQueryRequest, path catalog.ForeignQueryPath) error {
	if !finiteForeignNumber(path.StartupCost) || !finiteForeignNumber(path.TotalCost) || path.TotalCost < path.StartupCost {
		return fmt.Errorf("stuntgres: invalid foreign query path cost")
	}
	if !slices.Equal(request.Stage.Slots, path.Plan.Slots) {
		return fmt.Errorf("stuntgres: foreign query plan does not match its declared output layout")
	}
	return nil
}

func tryOpenForeignQuery(ctx context.Context, state *State, node plan.Node) (Stage, bool, error) {
	switch node.(type) {
	case *plan.JoinNode, *plan.AggregateNode, *plan.SortNode, *plan.LimitNode, *plan.EvaluationNode:
	default:
		return Stage{}, false, nil
	}
	if state.Source == nil || state.Invocation != nil || state.Outer != nil || state.IdentityReads || state.Locking || state.Source.Locks.RowMarked() || state.inherited.Mode != 0 || state.ScanRange || state.aggregatePartials != nil || state.asyncFrontier != nil {
		return Stage{}, false, nil
	}
	t := foreignTranslator{ctx: ctx, state: state}
	translated, ok, err := t.translate(node)
	if t.err != nil {
		return Stage{}, false, t.err
	}
	if err != nil || !ok || t.first == nil {
		return Stage{}, false, err
	}
	request := catalog.ForeignQueryRequest{Stage: translated.stage, Sources: t.sources}
	if host := state.Source.LiveHost(); host != nil {
		host.ReleaseReplacementRead()
	}
	var paths []catalog.ForeignQueryPath
	if request.Stage.Kind == "join" {
		provider, ok := t.first.Wrapper.(catalog.ForeignJoinPathProvider)
		if !ok {
			return Stage{}, false, nil
		}
		paths, err = provider.GetForeignJoinPaths(ctx, CopyForeignQueryRequest(request))
	} else {
		provider, ok := t.first.Wrapper.(catalog.ForeignUpperPathProvider)
		if !ok {
			return Stage{}, false, nil
		}
		paths, err = provider.GetForeignUpperPaths(ctx, CopyForeignQueryRequest(request))
	}
	if err != nil {
		return Stage{}, false, ForeignCallbackError(err)
	}
	selected := -1
	for i, path := range paths {
		if err := validateForeignQueryPath(request, path); err != nil {
			return Stage{}, false, err
		}
		c := path.Compatibility
		if !c.Expressions || !c.Collations || !c.StatementVisibility {
			continue
		}
		if selected < 0 || path.TotalCost < paths[selected].TotalCost {
			selected = i
		}
	}
	if selected < 0 {
		return Stage{}, false, nil
	}
	chosen := paths[selected].Plan
	chosen.Slots = slices.Clone(chosen.Slots)
	f := &ForeignQuerySource{Wrapper: t.first.Wrapper, Request: CopyForeignRequest(t.first.Request), Columns: slices.Clone(translated.columns), ImportValue: t.first.ImportValue, ExportValue: t.first.ExportValue, Query: &foreignComposite{request: request, plan: chosen, width: translated.width, cache: translated.cache}}
	t.first.lifetimeOwner.Add(f)
	source := &Source{Foreign: f, Materialized: true, FlatWidth: translated.width, Anonymous: [][]sqltype.Value{}, Authority: state.Source.Authority, Host: state.Source.Host}
	for _, slot := range request.Stage.Slots {
		if slot.Presence > len(source.PresenceSlots) {
			source.PresenceSlots = append(source.PresenceSlots, make([]int, slot.Presence-len(source.PresenceSlots))...)
		}
	}
	for _, slot := range request.Stage.Slots {
		if slot.Presence != 0 {
			source.PresenceSlots[slot.Presence-1] = 1
		}
	}
	source.PresenceCount = packPresenceSlots(source.PresenceSlots)
	out, err := openForeignStage(ctx, state, source, nil, node.Schema())
	return out, true, err
}

func (q *foreignComposite) executionRequest(parameters catalog.ForeignScanParameters, transaction catalog.ForeignTransaction) catalog.ForeignQueryRequest {
	r := CopyForeignQueryRequest(q.request)
	r.Parameters = parameters
	r.Transaction = transaction
	for i := range r.Sources {
		r.Sources[i].Request.Parameters = parameters
		r.Sources[i].Request.Transaction = transaction
	}
	return r
}
