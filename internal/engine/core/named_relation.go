package core

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// namedRelation lives only for one trigger invocation. Command activation does
// not replace it; ordinary function entry does. Admission serializes access.
type namedRelation struct {
	shape plan.NamedRelation
	rows  [][]sqltype.Value
}

func (h *BindingHost) LookupNamedRelation(name string) (plan.NamedRelation, bool) {
	for _, relation := range h.namedRelations {
		if relation.shape.Name == name {
			return relation.shape, true
		}
	}
	return plan.NamedRelation{}, false
}

func (h *statementHost) OpenNamedRelation(name string, schema plan.QuerySchema) (*exec.Source, error) {
	for _, relation := range h.namedRelations {
		if relation.shape.Name != name {
			continue
		}
		if !slices.Equal(relation.shape.Schema, schema) {
			return nil, ids.ErrCatalogReplaced
		}
		return &exec.Source{Anonymous: relation.rows, Materialized: true, FlatCount: len(relation.rows), FlatWidth: len(schema)}, nil
	}
	return nil, fmt.Errorf("stuntgres: named relation %q is outside its invocation", name)
}

func (s *StatementScope) installNamedRelations(relations []namedRelation) func() {
	host := s.Engine().AnalysisHost(s.Op)
	previous, previousBinding := s.namedRelations, host.namedRelations
	s.namedRelations, host.namedRelations = relations, relations
	return func() { s.namedRelations, host.namedRelations = previous, previousBinding }
}

func (s *StatementScope) installTriggerRelations(p stored.TriggerParts, segment *transitionSegment) func() {
	var relations []namedRelation
	if segment != nil && (p.OldTable != "" || p.NewTable != "") {
		relations = make([]namedRelation, 0, 2)
		if p.OldTable != "" {
			relations = append(relations, namedRelation{plan.NamedRelation{Name: p.OldTable, Schema: segment.schema}, segment.old})
		}
		if p.NewTable != "" {
			relations = append(relations, namedRelation{plan.NamedRelation{Name: p.NewTable, Schema: segment.schema}, segment.next})
		}
	}
	return s.installNamedRelations(relations)
}
