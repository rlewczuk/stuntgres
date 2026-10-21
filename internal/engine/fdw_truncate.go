package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/plan"
)

func (s *StatementScope) prepareForeignTruncate() ([]fdw.TruncateGroup, error) {
	e := s.engine()
	var groups []fdw.TruncateGroup
	for _, selected := range s.truncateOrZero().Relations {
		if selected.Relation.RelationKind != catalog.ForeignRelation {
			continue
		}
		e.mu.Lock()
		source, err := e.captureForeignSourceLocked(s, selected.Ref, selected.Relation, authorization.PrincipalRef{}, nil, plan.RowMark{})
		e.mu.Unlock()
		if err != nil {
			return nil, err
		}
		f := source.Foreign
		index := -1
		for i, g := range groups {
			if g.Source.Request.ServerOID == f.Request.ServerOID {
				index = i
				break
			}
		}
		if index < 0 {
			index = len(groups)
			groups = append(groups, fdw.TruncateGroup{Source: f, Request: catalog.ForeignTruncateRequest{Cascade: s.truncateOrZero().ForeignCascade, RestartIdentity: s.truncateOrZero().Restart}})
		}
		groups[index].Request.Tables = append(groups[index].Request.Tables, exec.CopyForeignRequest(f.Request))
	}
	// Admit every participant before any selected server is truncated.
	for i := range groups {
		g := &groups[i]
		tx, err := s.enlistForeign(s.op.Context(), g.Source.Wrapper, g.Source.Request, true)
		if err != nil {
			return nil, err
		}
		g.Request.Transaction = tx
		for j := range g.Request.Tables {
			g.Request.Tables[j].Transaction = tx
		}
	}
	return groups, nil
}
func (s *StatementScope) executeForeignTruncate(groups []fdw.TruncateGroup) error {
	s.foreignUnlocked()
	for _, g := range groups {
		if err := g.Source.Wrapper.(catalog.ForeignTruncater).ExecForeignTruncate(s.op.Context(), g.Request); err != nil {
			return exec.ForeignCallbackError(err)
		}
	}
	return nil
}
