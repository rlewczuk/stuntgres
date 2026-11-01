package plan

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

// InstalledSSLInfoExpr retains installed authority despite fixed non-TLS values.
type InstalledSSLInfoExpr struct {
	functions.SSLInfo
	Procedure *catalog.Procedure
	Ref ids.ObjectRef
}

func (n InstalledSSLInfoExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	inner, err := n.SSLInfo.Rebuild(info, children)
	if err != nil { return nil, err }
	n.SSLInfo = inner.(functions.SSLInfo)
	return n, nil
}
func (n InstalledSSLInfoExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledSSLInfoExpr) Retag(info expr.Info, children []expr.Expr, changed bool, _ expr.Retag) (expr.Expr, bool, error) {
	next, err := n.Rebuild(info, children)
	return next, changed, err
}
func (n InstalledSSLInfoExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(InstalledSSLInfoExpr)
	return ok && n.Procedure == o.Procedure && n.Ref == o.Ref && n.SSLInfo.SameGroup(o.SSLInfo, same, definition)
}
