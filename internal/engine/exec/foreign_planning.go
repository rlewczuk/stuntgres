package exec

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// ForeignPlanningOccurrence is a metadata-only occurrence, not a live scan.
// Owner identifies the common statement authority; a nil owner is ineligible.
type ForeignPlanningOccurrence struct {
	Source   *ForeignQuerySource
	Owner    any
	Eligible bool
}

// ForeignPlanningInput borrows translation facts and the actual handler owner
// only for PlanForeignQuery. Selections never retain this input or its owner.
// Excluded preserves execution's runtime-context barriers without opening them.
type ForeignPlanningInput struct {
	Frame        uint32
	Input        sqltype.InputEnvironment
	Parameters   []sqltype.Value
	Output       sqltype.OutputEnvironment
	ExportValue  ForeignValueExporter
	Occurrence   func(int) ForeignPlanningOccurrence
	HandlerOwner ForeignHandlerOwner // Borrowed for this planning call only.
	Excluded     bool
}

// ForeignQuerySelection retains the complete provider-selected representation.
// Private is immutable provider authority, never interpreted by the engine.
type ForeignQuerySelection struct {
	Request      catalog.ForeignQueryRequest
	Path         catalog.ForeignQueryPath
	Source       *ForeignQuerySource
	Columns      []catalog.Column
	Width, Cache int
}

func foreignPlanningInput(state *State) ForeignPlanningInput {
	input := ForeignPlanningInput{Frame: state.Frame, Input: state.Input, Output: state.Output, Parameters: state.Parameters,
		Excluded: state.Source == nil || state.Invocation != nil || state.Outer != nil || state.IdentityReads || state.Locking || state.inherited.Mode != 0 || state.ScanRange || state.aggregatePartials != nil || state.asyncFrontier != nil}
	if state.Source == nil {
		return input
	}
	input.Excluded = input.Excluded || state.Source.Locks.RowMarked()
	input.HandlerOwner = state.Source.LiveHost()
	input.ExportValue = firstForeignExporter(state)
	bundle := state.Source
	input.Occurrence = func(occurrence int) ForeignPlanningOccurrence {
		s := QuerySourceAt(bundle, occurrence)
		if s == nil || s.Foreign == nil {
			return ForeignPlanningOccurrence{}
		}
		return ForeignPlanningOccurrence{Source: s.Foreign, Owner: s.Foreign.lifetimeOwner,
			Eligible: s.RLS == nil && !s.RlsDenied && s.Input == nil && s.Final == nil && s.NestedRoot == nil && s.Inheritance == nil && s.StoredRef.Address.ObjectID == 0 && s.Foreign.Request.Identity == nil && s.Foreign.lifetimeOwner != nil}
	}
	return input
}
