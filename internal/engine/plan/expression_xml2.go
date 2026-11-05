package plan

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InstalledXML2Expr preserves provider authority through all scalar rewrites.
type InstalledXML2Expr struct {
	functions.XML2
	// Kernel retains the actual bind-time inner declaration of a SQL wrapper.
	// Current lookup selects invocation metadata, never its original lifetime.
	Kernel *catalog.Procedure
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
	// Inline is an invocation-private preflight route, never retained authority.
	Inline bool
}

func (n InstalledXML2Expr) Eval(c expr.Context) (sqltype.Value, error) {
	member, ok := functions.XML2Lookup(n.Name, len(n.Args))
	if !ok || member.Kernel < 0 {
		return n.XML2.Eval(c)
	}
	runtime, ok := c.(interface {
		EvalXML2(InstalledXML2Expr) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing installed xml2 runtime")
	}
	return runtime.EvalXML2(n)
}

func (n InstalledXML2Expr) PreflightFold() expr.Preflight {
	if n.Operation == functions.XML2Nodeset || n.Operation == functions.XML2List {
		return expr.PreflightXML2
	}
	return n.XML2.PreflightFold()
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
	return ok && n.Procedure == o.Procedure && n.Kernel == o.Kernel && n.Ref == o.Ref && n.XML2.SameGroup(o.XML2, same, definition)
}
func (InstalledXML2Expr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.InstalledXML2Expr") }
