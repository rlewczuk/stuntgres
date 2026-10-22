package exec

import (
	"context"
	"stuntgres/internal/sqltype"
)

// A lazy producer preserves physical occurrence/version identity and evaluates
// only demanded rows, before any parent predicate, projection or NULL extension.
func openRowSecurityStage(ctx context.Context, state *State, child Stage) Stage {
	program := child.Source.RLS
	evaluation := state.Evaluation(ctx)
	out := ProducingStage(&child, child.Columns)
	reset := out.Reset
	out.Reset = func(ctx context.Context, out *Stage) (bool, error) {
		changed, err := reset(ctx, out)
		if err != nil {
			return false, err
		}
		if program.Volatile && !changed {
			if _, err := child.Rewind(ctx); err != nil {
				return false, err
			}
			out.Selection, out.CacheOccurrences, out.Done = out.Selection[:0], out.CacheOccurrences[:0], false
			return true, nil
		}
		return changed, nil
	}
	out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
		for {
			i, present, err := child.NextOccurrence(ctx)
			if err != nil || !present {
				return present, err
			}
			evaluation.setContext(ctx)
			evaluation.BindQueryRow(child.Source, child.Index(i))
			evaluation.PhysicalTable = child.Source.PhysicalRef(child.Index(i))
			pass, _, err := EvaluateRowSecurity(program, &evaluation, evaluation.Row)
			if err != nil {
				return false, err
			}
			if pass {
				RetainOccurrence(out, &child, i)
				return true, ctx.Err()
			}
		}
	}
	return out
}

// Imported source policies may be volatile even when the outer stored
// subquery was bound before those policies existed.
func RowSecuritySourceVolatile(source *Source) bool {
	if source == nil {
		return false
	}
	if source.Foreign != nil || source.RLS != nil && source.RLS.Volatile {
		return true
	}
	if RowSecuritySourceVolatile(source.Input) || RowSecuritySourceVolatile(source.NestedInputs) {
		return true
	}
	for _, child := range source.Sources {
		if child != source && RowSecuritySourceVolatile(child) {
			return true
		}
	}
	return false
}

// DetachRowSecurityState moves an imported clause's retained graph onto the
// producer's existing authority. It does not own/close programs, follow lock
// back-pointers, or change rows, manifests or physical occurrence IDs.
func DetachRowSecurityState(state *State, authority *Authority, detach func(*sqltype.InputEnvironment, *sqltype.OutputEnvironment)) {
	detachRowSecurityState(state, detach)
	if state != nil {
		detachRowSecuritySourceAuthority(state.Source, authority, detach)
	}
}

// Source graphs use the same owned-edge/self conventions as Close.
func detachRowSecuritySourceAuthority(source *Source, authority *Authority, detach func(*sqltype.InputEnvironment, *sqltype.OutputEnvironment)) {
	if source == nil {
		return
	}
	source.Authority, source.Host = authority, nil
	for _, child := range source.Sources {
		if child != source {
			detachRowSecuritySourceAuthority(child, authority, detach)
		}
	}
	if source.Input != source {
		detachRowSecuritySourceAuthority(source.Input, authority, detach)
	}
	if source.NestedInputs != source {
		detachRowSecuritySourceAuthority(source.NestedInputs, authority, detach)
	}
	if stage := source.NestedStage; stage != nil {
		detachRowSecurityContext(&stage.Evaluation, detach)
		detachRowSecurityState(stage.Evaluation.Query, detach)
		if stage.Source != source {
			detachRowSecuritySourceAuthority(stage.Source, authority, detach)
		}
	}
	if lineage := source.Lineage; lineage != nil {
		detachRowSecurityState(lineage.state, detach)
		if lineage.Stage != source {
			detachRowSecuritySourceAuthority(lineage.Stage, authority, detach)
		}
		if lineage.Inputs != source && lineage.Inputs != lineage.Stage {
			detachRowSecuritySourceAuthority(lineage.Inputs, authority, detach)
		}
	}
	if source.Final != nil && source.Final.Source != source {
		detachRowSecuritySourceAuthority(source.Final.Source, authority, detach)
	}
	for _, segment := range source.Inheritance {
		if segment.Prefix != source {
			detachRowSecuritySourceAuthority(segment.Prefix, authority, detach)
		}
	}
	if source.JoinLeft != source {
		detachRowSecuritySourceAuthority(source.JoinLeft, authority, detach)
	}
	if source.JoinRight != source && source.JoinRight != source.JoinLeft {
		detachRowSecuritySourceAuthority(source.JoinRight, authority, detach)
	}
	var left, right *Source
	for _, lineage := range source.invocationLineage {
		if lineage.left != left && lineage.left != source {
			detachRowSecuritySourceAuthority(lineage.left, authority, detach)
			left = lineage.left
		}
		if lineage.right != right && lineage.right != source {
			detachRowSecuritySourceAuthority(lineage.right, authority, detach)
			right = lineage.right
		}
	}
}

func detachRowSecurityContext(c *Context, detach func(*sqltype.InputEnvironment, *sqltype.OutputEnvironment)) {
	for ; c != nil; c = c.Outer {
		c.Host, c.Context, c.RoutineAuthority = nil, nil, nil
		c.PreparedChecks, c.PreparedDomainChecks, c.PreparedGenerations = nil, nil, nil
		detach(&c.Input, &c.Output)
		if c.Query != nil {
			detach(&c.Query.Input, &c.Query.Output)
			c.Query.ExecutionContext = nil
		}
	}
}

func detachRowSecurityState(state *State, detach func(*sqltype.InputEnvironment, *sqltype.OutputEnvironment)) {
	if state == nil {
		return
	}
	detach(&state.Input, &state.Output)
	state.ExecutionContext = nil
	detachRowSecurityContext(state.Invocation, detach)
	detachRowSecurityContext(state.Outer, detach)
}
