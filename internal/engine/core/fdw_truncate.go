package core

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/plan"
)

func (s *StatementScope) prepareForeignTruncate() ([]fdw.TruncateGroup, error) {
	e := s.Engine()
	var sources []*exec.ForeignQuerySource
	for _, selected := range s.truncateOrZero().Relations {
		if selected.Relation.RelationKind != catalog.ForeignRelation {
			continue
		}
		e.Mu.Lock()
		source, err := e.captureForeignSourceLocked(s, selected.Ref, selected.Relation, authorization.PrincipalRef{}, nil, plan.RowMark{})
		e.Mu.Unlock()
		if err != nil {
			return nil, err
		}
		sources = append(sources, source.Foreign)
	}
	groups := fdw.GroupTruncate(sources, s.truncateOrZero().ForeignCascade, s.truncateOrZero().Restart)
	// Admit every participant before any selected server is truncated.
	for i := range groups {
		g := &groups[i]
		tx, err := s.enlistForeign(s.Op.Context(), g.Source.Wrapper, g.Source.Request, true)
		if err != nil {
			return nil, err
		}
		g.SetTransaction(tx)
		characteristics, err := s.foreignCharacteristics()
		if err != nil {
			return nil, err
		}
		for j := range g.Request.Tables {
			g.Request.Tables[j].Characteristics = characteristics
		}
	}
	return groups, nil
}
func (s *StatementScope) executeForeignTruncate(groups []fdw.TruncateGroup) error {
	s.foreignUnlocked()
	for _, g := range groups {
		if err := g.Source.Wrapper.(catalog.ForeignTruncater).ExecForeignTruncate(s.Op.Context(), g.Request); err != nil {
			return exec.ForeignCallbackError(err)
		}
	}
	return nil
}
