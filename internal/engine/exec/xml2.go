package exec

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func preflightXML2(c *Context, original expr.Composite, call plan.InstalledXML2Expr, changed, decisiveDefaults bool) (expr.Expr, bool, error) {
	if err := call.Validate(); err != nil {
		return nil, false, err
	}
	if literalNullChild(call) {
		return plan.LiteralExpr{Info: foldedExpressionInfo(call), Value: sqltype.Null(call.Usage.Type)}, true, nil
	}
	pruned, folded, err := preflightStrictParameterNull(c, call, decisiveDefaults)
	if err != nil || folded {
		return pruned, folded, err
	}
	current := call.Procedure
	auth, _, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return nil, false, err
	}
	if auth.Live() {
		current, err = ValidateStoredRoutineDescriptor(auth.Catalog, current)
		if err != nil {
			return nil, false, err
		}
	}
	if current == nil || extension.XML2ProviderOf(current) != extension.StockXML2 {
		return nil, false, fmt.Errorf("stuntgres: invalid installed xml2 set call")
	}
	p := original.Properties()
	independent := !p.RowDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion
	generic := c.PreserveParameterDemand && p.ParameterDependent
	compute := independent && !generic && call.Properties().Volatility == expr.Immutable && !planningExecutionBarrier(c, call)
	wrapper := current.DeclarationBody != nil
	inline := wrapper && !compute && current.Configuration == nil && !current.SecurityDefiner
	changed = changed || call.Inline != inline
	call.Inline = inline
	if generic {
		// Generic parameter NULL does not erase the outer initialization ACL.
		if err := AdmitRoutineCall(c, call, true, false); err != nil {
			return nil, false, err
		}
		if inline {
			// An expanded recipe initializes its genuine inner expression even
			// when that expression's strict body will receive a NULL argument.
			if _, err := BeginExpandedNativeDeclaration(c, call.Procedure, call.Info); err != nil {
				return nil, false, err
			}
		}
		return call, changed, nil
	}
	if !compute {
		return call, changed, nil
	}
	return preflightEvaluate(c, call, changed)
}

// EvalXML2 invokes the actual selected structural SQL body or its authenticated
// expanded route. Both retain the original outer source and genuine inner call.
func (c *Context) EvalXML2(n plan.InstalledXML2Expr) (value sqltype.Value, err error) {
	if err = EvaluationContext(c); err != nil {
		return value, err
	}
	if err = n.Validate(); err != nil {
		return value, err
	}
	member, ok := functions.XML2Lookup(n.Name, len(n.Args))
	if !ok || member.Kernel < 0 || n.Procedure == nil || n.RoutineReference != n.Procedure {
		return value, fmt.Errorf("stuntgres: invalid xml2 wrapper execution")
	}
	var values [4]sqltype.Value
	null := false
	for i, arg := range n.Args {
		values[i], err = arg.Eval(c)
		if err != nil {
			return value, err
		}
		null = null || values[i].IsNull()
	}
	if null && c.ConstantPlanning {
		return sqltype.Null(n.Usage.Type), nil
	}
	var entry nativeRoutineEntry
	configured := !n.Inline && !null && n.Procedure.Configuration != nil
	invocation, err := c.prepareNativeRoutineEntry(&entry, configured, n.Info.RoutineReference, n.Info.RoutineRecipeReference, n.Info.RoutineBodyInputs(values[:len(n.Args)]))
	if err != nil {
		return value, err
	}
	auth, dependencies, err := RoutineInvocationAuthority(invocation)
	if err != nil {
		return value, err
	}
	outer, err := ValidateStoredRoutineDescriptor(auth.Catalog, n.Procedure)
	if err != nil {
		return value, err
	}
	aggregate := entry.parent.aggregate != nil
	if aggregate {
		if n.Inline {
			return value, fmt.Errorf("stuntgres: aggregate support cannot adopt an inline XML2 source")
		}
	} else {
		// Ordinary calls retain their own definition-purpose admission. A real
		// aggregate continuation instead proved its distinct support purpose.
		if err = AdmitRecipeRoutineDescriptor(invocation, auth, dependencies, n.Procedure); err != nil {
			return value, err // Outer denial has no body statement Context.
		}
	}
	if !n.Inline && null {
		return sqltype.Null(n.Usage.Type), nil
	}
	if configured {
		host := invocation.LiveHost()
		if host == nil {
			return value, fmt.Errorf("stuntgres: configured native SQL body requires serial invocation")
		}
		leave, failure := host.EnterNativeRoutineConfiguration(outer)
		if failure != nil {
			return value, failure
		}
		defer func() { err = leave(err) }()
		invocation.Input, invocation.Output = host.Environments()
	}
	var body Context
	if n.Inline {
		body, err = BeginExpandedNativeDeclaration(invocation, n.Procedure, n.Info)
	} else {
		token, failure := invocation.enterNativeRoutine(&entry, &body)
		if failure != nil {
			return value, failure
		}
		defer token.Leave()
		defer func() { err = nativeDeclarationStatementContext(c, n.Procedure, err) }()
	}
	if err != nil {
		return value, err
	}
	inner, info, err := NativeDeclarationCall(&body, n.Procedure, n.Info, 0)
	if err != nil {
		return value, err
	}
	if n.Kernel == nil || !SameRoutineDeclaration(inner, n.Kernel) || extension.XML2ProviderOf(inner) != extension.StockXML2 || inner.Origin.Declaration != member.Kernel || inner.Origin.Installation.Cohort != n.Procedure.Origin.Installation.Cohort {
		return value, fmt.Errorf("stuntgres: foreign xml2 declaration kernel")
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	// Expand precisely the installed RETURN recipe into fixed invocation storage.
	text := n.Args[0].ResultUsage().Type
	switch n.Operation {
	case functions.XML2Nodeset:
		if len(n.Args) == 3 {
			values[3] = values[2]
		} else {
			values[3] = sqltype.String(text, "")
		}
		values[2] = sqltype.String(text, "")
	case functions.XML2List:
		values[2] = sqltype.String(text, ",")
	default:
		return value, fmt.Errorf("stuntgres: invalid xml2 wrapper operation")
	}
	if !n.Inline && body.Input.XMLInput != nil {
		body.Input.XMLInput = xml2BodyNotices{XMLInputDiagnostics: body.Input.XMLInput, outer: outer}
	}
	return functions.XML2Invoke(&body, info, n.Operation, values[:functions.XML2Members[member.Kernel].Arity])
}

type xml2BodyNotices struct {
	sqltype.XMLInputDiagnostics
	outer *catalog.Procedure
}

func (n xml2BodyNotices) XMLInputWarning(d sqlstate.Error) {
	if d.Context != "" {
		d.Context += "\n"
	}
	d.Context += SQLRoutineStatementFrame(n.outer.Name(), 1)
	n.XMLInputDiagnostics.XMLInputWarning(d)
}
