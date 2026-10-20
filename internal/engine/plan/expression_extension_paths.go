package plan

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// ExtensionPathsExpr is the pg_extension_update_paths set source. The engine
// opens it against the live extension catalog.
type ExtensionPathsExpr struct {
	expr.Info
	Arg expr.Expr
}

func (n ExtensionPathsExpr) Validate() error {
	if n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Arg.ResultUsage().Type.ID() != sqltype.Name || n.Usage.Type == nil || n.Usage.Type.ID() != 2249 || !n.Usage.Type.IsComposite() || n.Usage.Type.CompositeLen() != 3 {
		return fmt.Errorf("stuntgres: invalid extension path signature")
	}
	for i, name := range [...]string{"source", "target", "path"} {
		member := n.Usage.Type.CompositeMember(i)
		if member.Attribute != int32(i+1) || member.Name != name || member.Usage.Type.ID() != sqltype.Text {
			return fmt.Errorf("stuntgres: invalid extension path record")
		}
	}
	return nil
}

func (n ExtensionPathsExpr) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: extension path set used as scalar")
}

func (e ExtensionPathsExpr) Children() []expr.Expr {
	return []expr.Expr{e.Arg}
}

func (e ExtensionPathsExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := e.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(ExtensionPathsExpr)
	if err := rebuilt.Validate(); err != nil {
		return nil, err
	}
	return rebuilt, nil
}

func (e ExtensionPathsExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(e.Children()) {
		return nil, fmt.Errorf("stuntgres: invalid extensionPathsExpression arguments")
	}
	for _, child := range children {
		if child == nil {
			return nil, fmt.Errorf("stuntgres: nil source argument")
		}
	}
	e.Info = info
	e.Arg = children[0]
	return e, nil
}

func (e ExtensionPathsExpr) SourceAdmission() (bool, bool) { return true, true }

func (e ExtensionPathsExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(ExtensionPathsExpr)
	return ok && sqltype.SameRecordUsage(e.ResultUsage(), o.ResultUsage())
}

// OpenSource materializes the paths in the engine's runtime.
func (e ExtensionPathsExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	values, err := runtime.OpenExtensionPaths(e)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(e.Usage.Type, values), nil
}
