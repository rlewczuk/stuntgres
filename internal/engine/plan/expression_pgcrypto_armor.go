package plan

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InstalledPgpArmorHeadersExpr retains the selected installation through structural
// rewrites. Admission validates it against the current catalog and ACL.
type InstalledPgpArmorHeadersExpr struct {
	functions.PgpArmorHeaders
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledPgpArmorHeadersExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("stuntgres: pgp_armor_headers lost installed identity")
	}
	next, err := n.PgpArmorHeaders.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.PgpArmorHeaders = next.(functions.PgpArmorHeaders)
	return n, nil
}
func (n InstalledPgpArmorHeadersExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}

func (n InstalledPgpArmorHeadersExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	runtime, ok := ctx.(interface {
		OpenPgpArmorHeaders(InstalledPgpArmorHeadersExpr) (expr.SourceCursor, error)
	})
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing installed pgp_armor_headers runtime")
	}
	return runtime.OpenPgpArmorHeaders(n)
}

func (n InstalledPgpArmorHeadersExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledPgpArmorHeadersExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}

func (n InstalledPgpArmorHeadersExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(InstalledPgpArmorHeadersExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage()) && same(n.Arg, o.Arg)
}
