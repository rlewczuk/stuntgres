package stored

import (
	"fmt"
	"reflect"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Same-Usage casts are erased by ordinary binding. A replayed standard CAST
// starts before its surviving operand, unlike postfix syntax. Follow only the
// exact frozen annotations of such erased boundaries, retaining the original
// source position and the accepted operand's complete Usage as authority.
func (v *View) xmlSourceOccurrenceAt(path analyze.ViewLiteralPath, raw parser.Expr, bound expr.Expr) bool {
	if raw == nil || bound == nil {
		return false
	}
	path.Argument = 0
	for raw.SourcePosition() != bound.SourcePosition() {
		cast, ok := raw.(parser.CastExpr)
		if !ok || cast.FrozenMethod != "" {
			return false
		}
		if usage, captured := v.castUsages[path]; !captured || usage != bound.ResultUsage() {
			return false
		}
		raw = cast.Child
		if raw == nil {
			return false
		}
		path.Node++
	}
	return true
}

type xmlCapture struct {
	Usage sqltype.Usage
	Sources []expr.Expr
	Name string
	Names []string
}

// Authenticate the parent in its actual owning frame. Source occurrence checks
// run in validateLiteralTree, where each child's exact ordinal is already known.
func (v *View) capturedXML(path analyze.ViewLiteralPath, raw parser.Expr) (xmlCapture, error) {
	found := false
	var captured xmlCapture
	var failure error
	accept := func(usage sqltype.Usage, children []expr.Expr, opener parser.Position, name string, names []string) {
		sources:=make([]expr.Expr,len(children))
		for i,child:=range children {
			var err error
			sources[i],err=plan.XMLSourceOperand(child,opener)
			if err!=nil{failure=err;return}
		}
		if found&&(captured.Usage!=usage||!reflect.DeepEqual(captured.Sources,sources)||captured.Name!=name||!reflect.DeepEqual(captured.Names,names)){
			failure=fmt.Errorf("stuntgres: ambiguous frozen XML occurrence")
			return
		}
		captured=xmlCapture{Usage:usage,Sources:sources,Name:name,Names:names}
		found=true
	}
	check := func(node expr.Expr) {
		if failure!=nil{return}
		switch n:=node.(type) {
		case functions.XMLConstruct:
			if r,ok:=raw.(parser.XMLConstructExpr);ok&&n.SourcePosition()==r.Pos&&n.MatchesSyntax(r){
				failure=n.ValidateStoredProvider()
				if failure==nil{accept(n.ResultUsage(),n.Children(),n.Pos,n.Name(),n.Names())}
			}
		case functions.XMLDocumentPredicate:
			if r,ok:=raw.(parser.XMLDocumentPredicateExpr);ok&&reflect.DeepEqual(n.Syntax,r)&&n.SourcePosition()==r.Pos&&n.Negated==r.Negated{
				failure=n.ValidateStoredProvider()
				if failure==nil{accept(n.ResultUsage(),[]expr.Expr{n.Child},n.Pos,"",nil)}
			}
		case functions.XMLParse:
			if r,ok:=raw.(parser.XMLParseExpr);ok&&reflect.DeepEqual(n.Syntax,r)&&n.SourcePosition()==r.Pos&&n.Document==r.Document&&n.Whitespace==r.Whitespace{
				failure=n.ValidateStoredProvider()
				if failure==nil{accept(n.ResultUsage(),[]expr.Expr{n.Child},n.Pos,"",nil)}
			}
		case plan.CoercionExpr:
			if !n.XMLSerializeResult{return}
			source,target,err:=plan.XMLSerialization(n)
			if err!=nil{failure=err;return}
			if r,ok:=raw.(parser.XMLSerializeExpr);ok&&reflect.DeepEqual(source.Syntax,r)&&n.SourcePosition()==r.Pos&&source.Document==r.Document&&source.Indent==r.Indent{
				accept(target,[]expr.Expr{source.Child},source.Pos,"",nil)
			}
		}
	}
	var walk func(plan.Node)
	walk = func(node plan.Node) {
		if node == nil || failure != nil {
			return
		}
		if p, ok := node.(*plan.ProjectNode); ok && p.Frame != path.Query {
			return
		}
		plan.QueryExpressions(node, func(bound expr.Expr) { plan.WalkExpression(bound, check) })
		left, right := plan.QueryInputs(node)
		walk(left)
		walk(right)
	}
	walk(v.frames[path.Query])
	if failure != nil {
		return xmlCapture{}, failure
	}
	if !found {
		return xmlCapture{}, fmt.Errorf("stuntgres: missing frozen XML occurrence")
	}
	return captured, nil
}
