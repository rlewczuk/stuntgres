package functions

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e CatalogForeignKeys) Children() []expr.Expr {
	return nil
}

func (e CatalogForeignKeys) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := e.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(CatalogForeignKeys)
	if e.Usage.Type != info.Usage.Type {
		rebuilt.metadata, err = newCatalogForeignKeyValues(info.Usage.Type)
		if err != nil {
			return nil, err
		}
	}
	if err := rebuilt.Validate(); err != nil {
		return nil, err
	}
	return rebuilt, nil
}

func (e CatalogForeignKeys) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(e.Children()) {
		return nil, fmt.Errorf("stuntgres: invalid CatalogForeignKeys arguments")
	}
	for _, child := range children {
		if child == nil {
			return nil, fmt.Errorf("stuntgres: nil source argument")
		}
	}
	e.Info = info
	return e, nil
}

func (e CatalogForeignKeys) SourceAdmission() (bool, bool) { return true, true }

func (e CatalogForeignKeys) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(CatalogForeignKeys)
	return ok && sqltype.SameRecordUsage(e.ResultUsage(), o.ResultUsage())
}

func (e CatalogForeignKeys) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	c := ctx
	if _, err := evaluationContext(c); err != nil {
		return expr.SourceCursor{}, err
	}
	if err := e.Validate(); err != nil {
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
