package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

// This boundary sees only the exact consumed column default, never a command
// graph. Bind transfers the prepared scalar closure; Execute selects it after
// ordinary current binding and before default preflight.
func (s *StatementScope) selectAcceptedDefault(evaluation *exec.Context, origin analyze.DefaultOrigin, node expr.Expr) (analyze.DefaultOrigin, expr.Expr, error) {
	portal := s.protocolOrZero().execute
	if portal == nil || portal.defaults == nil || origin.Definition.Expression == nil {
		return origin, node, nil
	}
	if portal.Retired || portal.Transaction != s.TxRef || portal.Folds == nil || !portal.defaults.MatchesGeneration(portal.Folds.Generation) {
		return origin, nil, ids.ErrCatalogReplaced
	}
	artifact, err := portal.defaults.Lookup(evaluation.DefaultOccurrence, origin.Fold.Owner, origin.Fold.Attribute)
	if err != nil {
		return origin, nil, err
	}
	if artifact == nil {
		return origin, node, nil
	}
	if err := stored.AcceptedDefaultSource(evaluation.Catalog, artifact); err != nil {
		return origin, nil, err
	}
	original, ref, attribute, definition := artifact.Source()
	if origin.Consumer == nil || origin.Consumer.Address() != ref.Address || evaluation.Catalog.Get(ref.Address) != origin.Consumer || origin.Definition.OID != definition.OID {
		return origin, nil, ids.ErrCatalogReplaced
	}
	origin.Consumer, origin.Definition = original, definition
	origin.Fold.Owner, origin.Fold.Attribute, origin.Fold.Definition = ref, attribute, definition.OID
	origin.Accepted = artifact
	return origin, artifact.Expression(), nil
}

func (h *statementHost) PrepareAcceptedDefault(c *exec.Context, consumer *catalog.Relation, fold exec.FoldOrigin, policy exec.DefaultPreparationPolicy) (expr.Expr, *exec.AcceptedDefault, bool, error) {
	s := h.scope()
	origin := analyze.DefaultOrigin{Consumer: consumer, Definition: consumer.ColumnMeta.Defaults[fold.Attribute], Fold: fold, Occurrence: c.DefaultOccurrence}
	origin, node, err := s.selectAcceptedDefault(c, origin, origin.Definition.Expression)
	if err != nil {
		return nil, nil, false, err
	}
	analyze.ApplyDefaultOrigin(origin, c)
	bind := s.protocolOrZero().bind
	if bind != nil && origin.Accepted == nil {
		if bind.defaultCandidate == nil {
			bind.defaultCandidate = exec.NewAcceptedDefaultSet()
		}
		prepared, changed, err := bind.defaultCandidate.Prepare(c, c.DefaultOccurrence, consumer, fold.Owner, fold.Attribute, policy)
		return prepared, nil, changed, err
	}
	prepared, changed, err := exec.PreflightDefault(c, node, policy)
	return prepared, origin.Accepted, changed || origin.Accepted != nil, err
}
