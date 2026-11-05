package plan

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type InstalledXML2XPathTableExpr struct {
	functions.XML2XPathTable
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledXML2XPathTableExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if n.Procedure == nil || info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("xpath_table lost installed identity")
	}
	next, err := n.XML2XPathTable.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.XML2XPathTable = next.(functions.XML2XPathTable)
	return n, nil
}
func (n InstalledXML2XPathTableExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledXML2XPathTableExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	runtime, ok := ctx.(interface {
		OpenXML2XPathTable(InstalledXML2XPathTableExpr) (expr.SourceCursor, error)
	})
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("missing installed xpath_table runtime")
	}
	return runtime.OpenXML2XPathTable(n)
}
func (n InstalledXML2XPathTableExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledXML2XPathTableExpr)
	if !ok || n.Ref != o.Ref || n.Procedure != o.Procedure || n.Source != o.Source || n.Occurrence != o.Occurrence || !sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage()) || len(n.Args) != len(o.Args) {
		return false
	}
	for i, arg := range n.Args {
		a, b := expr.InfoOf(arg), expr.InfoOf(o.Args[i])
		if a.Source != b.Source || a.Pos != b.Pos || a.Occurrence != b.Occurrence || a.Usage != b.Usage {
			return false
		}
	}
	return true
}
func (n InstalledXML2XPathTableExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(InstalledXML2XPathTableExpr)
	if !ok || n.Ref != o.Ref || n.Procedure != o.Procedure || !sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage()) || len(n.Args) != len(o.Args) {
		return false
	}
	for i, arg := range n.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
func (InstalledXML2XPathTableExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledXML2XPathTableExpr")
}
