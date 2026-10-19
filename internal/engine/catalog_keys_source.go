package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e catalogForeignKeysExpression) Children() []expr.Expr {
	return nil
}

func (e catalogForeignKeysExpression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := e.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(catalogForeignKeysExpression)
	if err := rebuilt.validate(); err != nil {
		return nil, err
	}
	return rebuilt, nil
}

func (e catalogForeignKeysExpression) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(e.Children()) {
		return nil, fmt.Errorf("stuntgres: invalid catalogForeignKeysExpression arguments")
	}
	for _, child := range children {
		if child == nil {
			return nil, fmt.Errorf("stuntgres: nil source argument")
		}
	}
	e.Info = info
	return e, nil
}

func (e catalogForeignKeysExpression) SourceAdmission() (bool, bool) { return true, true }

func (e catalogForeignKeysExpression) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(catalogForeignKeysExpression)
	return ok && sqltype.SameRecordUsage(e.ResultUsage(), o.ResultUsage())
}

func (e catalogForeignKeysExpression) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return expr.SourceCursor{}, err
	}
	if err := e.validate(); err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.NewSourceCursor(expr.SourceCursor{
		Type: e.Usage.Type, State: e.metadata, Advance: nextCatalogForeignKeySource,
	}), nil
}

func nextCatalogForeignKeySource(_ context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	if s.Position == len(catalogForeignKeys) {
		return sqltype.Value{}, false, nil
	}
	link := &catalogForeignKeys[s.Position]
	metadata := &s.State.(*catalogForeignKeyValues).fields[s.Position]
	s.Position++
	fields := make([]sqltype.Value, 6)
	copy(fields, metadata[:])
	fields[4] = sqltype.Boolean(s.Type.CompositeMember(4).Usage.Type, link.array)
	fields[5] = sqltype.Boolean(s.Type.CompositeMember(5).Usage.Type, link.optional)
	value, err := sqltype.OwnedCompositeValue(s.Type, fields)
	return value, err == nil, err
}
