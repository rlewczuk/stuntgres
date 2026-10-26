package view

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// FreezeRoutineAction shares checked DML/query presentation with rule actions,
// without rule OLD/NEW parameters or a relation-owned rewrite definition.
func FreezeRoutineAction(read catalog.Read, owner ids.SessionToken, syntax parser.Statement, binding analyze.Binding) (stored.RuleAction, error) {
	presentation, err := freezeRuleActionPresentation(read, owner, syntax, binding)
	if err != nil {
		return stored.RuleAction{}, err
	}
	modifiers, err := freezeRuleModifierPresentations(read, owner, binding)
	if err != nil {
		return stored.RuleAction{}, err
	}
	// Captures are statement-wide; presentations can share raw SELECT owners.
	// Keep complete actual-key coverage without partitioning shared captures.
	var owners map[analyze.ViewLiteralPath]bool
	if len(binding.CTEs) != 0 {
		owners = make(map[analyze.ViewLiteralPath]bool)
	}
	prepare := func(part *stored.View, statement parser.Statement) (*stored.View, error) {
		var roots map[analyze.ViewLiteralPath]bool
		if owners != nil {
			var err error
			roots, err = part.LiteralRoots()
			if err != nil {
				return nil, err
			}
		}
		return part.With(func(p *stored.ViewParts) {
			p.ActionSyntax = statement
			p.RoutineInputs = slices.Clone(binding.Parameters.Types)
			if owners == nil {
				return
			}
			p.Literals = routinePresentationOccurrences(p.Literals, roots, owners)
			p.CastUsages = routinePresentationOccurrences(p.CastUsages, roots, owners)
			p.RoutineRefs = routinePresentationOccurrences(p.RoutineRefs, roots, owners)
			p.OperatorRefs = routinePresentationOccurrences(p.OperatorRefs, roots, owners)
			p.Composites = routinePresentationOccurrences(p.Composites, roots, owners)
			p.SimilarSubstrings = routinePresentationOccurrences(p.SimilarSubstrings, roots, owners)
			p.PosixSubstrings = routinePresentationOccurrences(p.PosixSubstrings, roots, owners)
			p.Collations = routinePresentationOccurrences(p.Collations, roots, owners)
			p.Columns = routinePresentationOccurrences(p.Columns, roots, owners)
			p.Stars = routinePresentationOccurrences(p.Stars, roots, owners)
		}), nil
	}
	presentation, err = prepare(presentation, syntax)
	if err != nil {
		return stored.RuleAction{}, err
	}
	for statement, part := range modifiers {
		modifiers[statement], err = prepare(part, statement)
		if err != nil {
			return stored.RuleAction{}, err
		}
	}
	if owners != nil && !routineCaptureHasOwners(binding.Capture, owners) {
		return stored.RuleAction{}, fmt.Errorf("stuntgres: invalid view literal occurrence path")
	}
	binding = freezeRoutineSources(binding)
	if len(binding.CTEs) != 0 {
		// Actual modifier bindings own separate source lists. One existing
		// immutable mapper reconnects their declarations and every scan.
		mapper := stored.NewRuleTreeMapper(read, nil, 0, nil, stored.RuleTreeMapping{Mutation: func(definition *plan.CTEDefinition) (analyze.Binding, error) {
			mutation, _ := analyze.MutationBinding(definition)
			return freezeRoutineSources(*mutation), nil
		}})
		binding, err = stored.MapRuleBindingRecipes(binding, mapper)
		if err != nil {
			return stored.RuleAction{}, err
		}
	}
	return stored.NewRuleAction(stored.RuleActionParts{Syntax: syntax, Binding: binding, Presentation: presentation, ModifierPresentations: modifiers}), nil
}

func freezeRoutineSources(binding analyze.Binding) analyze.Binding {
	binding.Storage = nil
	binding.Resolvers = analyze.ResolverInputs{}
	binding.QuerySources = slices.Clone(binding.QuerySources)
	for i := range binding.QuerySources {
		binding.QuerySources[i].Storage = nil
		// Recipes retain creation-time slots; execution maps these stable
		// attributes to the live compacted physical row.
		binding.QuerySources[i].FrozenAttributes = slices.Clone(binding.QuerySources[i].Relation.Attributes)
	}
	return binding
}

// Root membership retains original Node/Argument identities. Strict recursive
// validation still rejects malformed or missing occurrences within those roots.
func routinePresentationOccurrences[T any](values map[analyze.ViewLiteralPath]T, roots, owners map[analyze.ViewLiteralPath]bool) map[analyze.ViewLiteralPath]T {
	belongs := func(path analyze.ViewLiteralPath) bool {
		path.Node, path.Argument = 0, 0
		return roots[path]
	}
	count := 0
	for path := range values {
		if belongs(path) {
			owners[path] = true
			count++
		}
	}
	if count == len(values) {
		return values
	}
	if count == 0 {
		return nil
	}
	projected := make(map[analyze.ViewLiteralPath]T, count)
	for path, value := range values {
		if belongs(path) {
			projected[path] = value
		}
	}
	return projected
}

func routineCaptureHasOwners(capture analyze.ViewCapture, frames map[analyze.ViewLiteralPath]bool) bool {
	return routineOccurrencesHaveOwners(capture.Literals, frames) &&
		routineOccurrencesHaveOwners(capture.CastUsages, frames) &&
		routineOccurrencesHaveOwners(capture.RoutineRefs, frames) &&
		routineOccurrencesHaveOwners(capture.OperatorRefs, frames) &&
		routineOccurrencesHaveOwners(capture.Composites, frames) &&
		routineOccurrencesHaveOwners(capture.SimilarSubstrings, frames) &&
		routineOccurrencesHaveOwners(capture.PosixSubstrings, frames) &&
		routineOccurrencesHaveOwners(capture.Collations, frames) &&
		routineOccurrencesHaveOwners(capture.Columns, frames) &&
		routineOccurrencesHaveOwners(capture.Stars, frames)
}

func routineOccurrencesHaveOwners[T any](values map[analyze.ViewLiteralPath]T, frames map[analyze.ViewLiteralPath]bool) bool {
	for path := range values {
		if !frames[path] {
			return false
		}
	}
	return true
}
