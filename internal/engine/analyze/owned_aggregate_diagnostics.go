package analyze

import (
 "fmt"
 "strings"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqlstate"
)

// OwnedAggregateDirectCountValid is shared by selected binding and frozen
// presentation. Counts are expanded invocation operands, not catalog pronargs.
func OwnedAggregateDirectCountValid(d *catalog.Procedure, direct, rows int) bool {
	if d == nil || d.Aggregate == nil || direct < 0 || rows < 1 {
		return false
	}
	required := int(d.Aggregate.Definition.DirectArgs)
	if !d.Aggregate.Variadic {
		return direct == required
	}
	if d.Aggregate.Definition.Kind == "h" {
		return direct-(required-1) == rows
	}
	paired := required == strings.Count(string(d.Signature), ",")+1
	if paired {
		return direct >= required
	}
	return direct == required
}

func (b *Binder) ownedAggregateDirectCountError(raw parser.AggregateExpr, d *catalog.Procedure, name parser.ObjectName, args []expr.Expr) error {
 types := make([]string,len(args))
 for i, arg := range args { types[i] = b.DiagnosticTypeName(arg.ResultUsage().Type) }
 diagnostic := expr.ErrorAt("42883", "function "+catalog.ObjectNameText(name)+"("+strings.Join(types,", ")+") does not exist",raw.Pos).(*sqlstate.Error)
 noun := "arguments"
 if d.Aggregate.Definition.DirectArgs == 1 { noun = "argument" }
 diagnostic.Hint = fmt.Sprintf("There is an ordered-set aggregate %s, but it requires %d direct %s, not %d.",catalog.ObjectNameText(name),d.Aggregate.Definition.DirectArgs,noun,len(raw.Args))
 if d.Aggregate.Variadic && d.Aggregate.Definition.Kind == "h" {
  fixed:=int(d.Aggregate.Definition.DirectArgs)-1
  diagnostic.Hint=fmt.Sprintf("To use the hypothetical-set aggregate %s, the number of hypothetical direct arguments (here %d) must match the number of ordering columns (here %d).",catalog.ObjectNameText(name),len(raw.Args)-fixed,len(raw.Order))
 }
 return diagnostic
}
