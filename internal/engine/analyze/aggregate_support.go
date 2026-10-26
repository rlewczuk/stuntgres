package analyze

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// SelectAggregateSupport uses the ordinary callable shape and overload ranking,
// but only declared inputs: support lookup never supplies defaults or expands a
// variadic array. It does not bind a body, evaluate expressions, or infer params.
func (b *Binder) SelectAggregateSupport(view catalog.Read, reader catalog.DefinitionReader, path NamespaceSelection, name parser.ObjectName, inputs []sqltype.Usage) (SQLCallable, error) {
	raw := parser.FunctionExpr{Name: name, Args: make([]parser.Expr, len(inputs))}
	operands := make([]ComparisonOperand, len(inputs))
	names := make([]string, len(inputs))
	familyArgs := make([]functions.Argument, len(inputs))
	for i, u := range inputs {
		// Support selection consumes types, never SQL datums. In particular
		// INTERNAL is an opaque carrier and cannot even form a SQL NULL.
		operands[i] = ComparisonOperand{Node: plan.ParameterExpr{Info: expr.Info{Usage: u, Property: expr.Properties{ParameterDependent: true}}, Number: i + 1}}
		familyArgs[i] = functions.Argument{Expression: operands[i].Node, Usage: u}
		names[i] = u.Type.DiagnosticName()
		if u.Type.ID() == 2276 {
			names[i] = `"any"`
		}
	}
	var candidates []SQLCallable
	seen := map[catalog.ProcedureSignature]bool{}
	for i := range path.Count() {
		for d := range catalog.DefinitionProceduresNamedAt(view, reader, path.At(i), name.Parts[len(name.Parts)-1].Text) {
			if seen[d.Signature] {
				continue
			}
			seen[d.Signature] = true
			declared := 0
			if d.Signature != "" {
				declared = strings.Count(string(d.Signature), ",") + 1
			}
			if declared != len(inputs) {
				continue
			}
			if functions.IsAggregateContextSupport(uint32(d.Address().ObjectID)) && d.Builtin != nil && d.Builtin.Variadic == 2276 {
				if len(inputs) > 0 && inputs[0].Type.ID() == 2281 {
					candidates = append(candidates, SQLCallable{Procedure: d, Inputs: append([]sqltype.Usage(nil), inputs...), Path: i})
				}
				continue
			}
			// VARIADIC syntax asks the shared shape owner to retain its array formal.
			raw.Variadic = d.SQL != nil || d.Builtin != nil && d.Builtin.Variadic != 0
			candidate, ok := SQLCallableShape(view, reader, d, raw)
			if !ok {
				continue
			}
			// Shape retention is not SQL VARIADIC call syntax: a transition
			// receives exactly this fixed vector, without array expansion.
			raw.Variadic = false
			polymorphic := false
			for _, formal := range candidate.Inputs {
				polymorphic = polymorphic || !formal.Type.Concrete() && formal.Type.ID() != 2281
			}
			if candidate.polymorphic && polymorphic {
				candidate.Targets = append([]sqltype.Usage(nil), candidate.Inputs...)
				if !b.ProbeSQLCallable(raw, familyArgs, &candidate) {
					continue
				}
			}
			candidate.Path = i
			candidates = append(candidates, candidate)
		}
	}
	// Exact declared identities precede base-type ranking, as for ordinary calls.
	var exact []SQLCallable
	for _, candidate := range candidates {
		same := true
		for i, u := range inputs {
			same = same && u.Type == candidate.Inputs[i].Type
		}
		if same {
			exact = append(exact, candidate)
		}
	}
	if len(exact) > 0 {
		candidates = exact
	} else {
		candidates = SelectSQLCallables(view, candidates, operands, nil)
	}
	if len(candidates) != 1 {
		if len(candidates) == 0 && len(inputs) == 2 && inputs[0].Type.ID() == 2281 && inputs[1].Type.ID() == 2276 {
			for i := range path.Count() {
				if path.At(i).ObjectID != ids.PGCatalogNamespaceOID {
					continue
				}
				for _, identity := range catalog.BuiltinRoutineIdentitiesNamed(name.Parts[len(name.Parts)-1].Text) {
					// Released 3970 has the same declared ANY slot as 3971,
					// but no variadic mode. Lookup evidence diagnoses that
					// incompatibility; it does not install executable support.
					if identity.ID == 3970 && identity.Signature == "2281,2276" {
						return SQLCallable{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "function " + catalog.ObjectNameText(name) + `(internal, "any") must accept VARIADIC ANY to be used in this aggregate`}
					}
				}
			}
		}
		code, message := "42883", "function "+catalog.ObjectNameText(name)+"("+strings.Join(names, ", ")+") does not exist"
		if len(candidates) > 1 {
			code = "42725"
			message = "function " + catalog.ObjectNameText(name) + "(" + strings.Join(names, ", ") + ") is not unique"
		}
		return SQLCallable{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	}
	selected := candidates[0]
	if selected.Procedure.RoutineKind != catalog.FunctionRoutine {
		return SQLCallable{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: fmt.Sprintf("function %s(%s) does not exist", catalog.ObjectNameText(name), strings.Join(names, ", "))}
	}
	return selected, nil
}
