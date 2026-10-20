package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
)

type foreignTruncateGroup struct {
	source  *foreignQuerySource
	request catalog.ForeignTruncateRequest
}

func (s *StatementScope) prepareForeignTruncate() ([]foreignTruncateGroup, error) {
	e := s.op.session.Engine()
	var groups []foreignTruncateGroup
	for _, selected := range s.truncateOrZero().relations {
		if selected.relation.RelationKind != catalog.ForeignRelation {
			continue
		}
		e.mu.Lock()
		source, err := e.captureForeignSourceLocked(s, selected.ref, selected.relation, authorization.PrincipalRef{}, nil, plan.RowMark{})
		e.mu.Unlock()
		if err != nil {
			return nil, err
		}
		f := source.Foreign
		index := -1
		for i, g := range groups {
			if g.source.Request.ServerOID == f.Request.ServerOID {
				index = i
				break
			}
		}
		if index < 0 {
			index = len(groups)
			groups = append(groups, foreignTruncateGroup{source: f, request: catalog.ForeignTruncateRequest{Cascade: s.truncateOrZero().foreignCascade, RestartIdentity: s.truncateOrZero().restart}})
		}
		groups[index].request.Tables = append(groups[index].request.Tables, CopyForeignRequest(f.Request))
	}
	// Admit every participant before any selected server is truncated.
	for i := range groups {
		g := &groups[i]
		tx, err := s.enlistForeign(s.op.Context(), g.source.Wrapper, g.source.Request, true)
		if err != nil {
			return nil, err
		}
		g.request.Transaction = tx
		for j := range g.request.Tables {
			g.request.Tables[j].Transaction = tx
		}
	}
	return groups, nil
}
func (s *StatementScope) executeForeignTruncate(groups []foreignTruncateGroup) error {
	s.foreignUnlocked()
	for _, g := range groups {
		if err := g.source.Wrapper.(catalog.ForeignTruncater).ExecForeignTruncate(s.op.Context(), g.request); err != nil {
			return ForeignCallbackError(err)
		}
	}
	return nil
}
