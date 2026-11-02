package plan

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

// InstalledXML2Expr preserves provider authority through all scalar rewrites.
type InstalledXML2Expr struct {
	functions.XML2
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledXML2Expr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	inner, err := n.XML2.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.XML2 = inner.(functions.XML2)
	return n, nil
}
func (n InstalledXML2Expr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledXML2Expr) Retag(info expr.Info, children []expr.Expr, changed bool, _ expr.Retag) (expr.Expr, bool, error) {
	next, err := n.Rebuild(info, children)
	return next, changed, err
}
func (n InstalledXML2Expr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(InstalledXML2Expr)
	return ok && n.Procedure == o.Procedure && n.Ref == o.Ref && n.XML2.SameGroup(o.XML2, same, definition)
}
func (InstalledXML2Expr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.InstalledXML2Expr") }
