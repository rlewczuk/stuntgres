package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type namespaceInquiryKind uint8

const (
	myTempNamespace namespaceInquiryKind = iota
	otherTempNamespace
)

func (k namespaceInquiryKind) name() string {
	if k == myTempNamespace {
		return "pg_my_temp_schema"
	}
	if k == otherTempNamespace {
		return "pg_is_other_temp_schema"
	}
	return ""
}

type namespaceInquiryExpression struct {
	expressionInfo
	kind namespaceInquiryKind
	arg  BoundExpr
}

func (b *expressionBinder) bindNamespaceInquiry(raw parser.FunctionExpr) (BoundExpr, error) {
	var operand comparisonOperand
	var single [1]string
	names := single[:0]
	if len(raw.Args) == 1 {
		names = single[:]
	} else if len(raw.Args) != 0 && len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		if len(raw.Args) == 1 {
			operand = arg
		}
		if len(raw.Args) <= 100 {
			names[i] = "unknown"
			if typ := arg.descriptor(); typ != nil {
				names[i] = typ.DiagnosticName()
			}
			names[i] = functionNamedType(raw, i, names[i])
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind, arity, result := myTempNamespace, 0, b.typ(sqltype.Oid).Usage()
	if name == "pg_is_other_temp_schema" {
		kind, arity, result = otherTempNamespace, 1, b.typ(sqltype.Bool).Usage()
	}
	if !visible || name != kind.name() || len(raw.Args) != arity || raw.Variadic || len(raw.ArgNames) != 0 {
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	properties := ExprProperties{Volatility: Stable}
	var arg BoundExpr
	if arity == 1 {
		target := b.typ(sqltype.Oid).Usage()
		arg, err = b.finalizeOperand(operand, target.Type)
		if err != nil {
			return nil, err
		}
		if arg.ResultUsage() != target {
			plan, failure := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
			if failure != nil {
				if d, ok := failure.(*sqlstate.Error); ok && (d.Code == "42804" || d.Code == "42846") {
					return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
				}
				return nil, failure
			}
			arg = coercionExpression{b.info(target, raw.Args[0].SourcePosition(), coercionProperties(arg, plan)), arg, plan, sqltype.Implicit, false}
		}
		properties = arg.Properties()
		properties.Volatility = max(Stable, properties.Volatility)
	}
	return namespaceInquiryExpression{b.info(result, raw.Pos, properties), kind, arg}, nil
}

func (n namespaceInquiryExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var oid uint32
	if n.kind == otherTempNamespace {
		value, err := n.arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			return sqltype.Null(n.usage.Type), nil
		}
		oid, err = value.OID()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	scope := c.liveScope()
	if scope == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: namespace inquiry lacks statement authority")
	}
	e := scope.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := scope.namespaceAuthorizationLocked(e)
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.kind == myTempNamespace {
		return sqltype.OIDValue(n.usage.Type, uint32(temporaryNamespace(auth).ObjectID))
	}
	if n.kind != otherTempNamespace {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid namespace inquiry")
	}
	ns, _ := auth.catalog.state.descriptors[ObjectAddress{ClassID: NamespaceClassID, ObjectID: OID(oid)}].(*namespaceDescriptor)
	other := ns != nil && ns.temporary != (SessionToken{}) && ns.temporary != auth.session.token
	return sqltype.Boolean(n.usage.Type, other), nil
}

// Engine.mu is held. Every inquiry sees the statement's private candidate before
// its transaction overlay; neither lookup nor evaluation publishes shared state.
func (s *StatementScope) namespaceAuthorizationLocked(e *Engine) (AuthorizationView, error) {
	auth, err := s.writeAuthorizationLocked(e)
	if err != nil {
		return auth, err
	}
	if s.catalog != nil {
		auth.recaptureCatalog(e.privateCatalogLocked(s.catalog))
	}
	if s.catalog != nil && temporaryNamespace(auth) == (ObjectAddress{}) {
		for a, d := range s.catalog.descriptors {
			if ns, ok := d.(*namespaceDescriptor); ok && !ns.temporaryToast && ns.temporary == auth.session.token {
				auth.stagedTemporary = a
				break
			}
		}
	}
	return auth, nil
}

// Resolve pending pg_temp only when a non-NULL schema inquiry is demanded.
// Pair creation joins the existing statement candidate and ordinary Finish owns
// publication or abort. Prepare and no-pending paths create no catalog stage.
func (s *StatementScope) schemaInquiryPathLocked(e *Engine) (AuthorizationView, []ObjectAddress, error) {
	auth, err := s.namespaceAuthorizationLocked(e)
	if err != nil {
		return auth, nil, err
	}
	path, pending, err := e.explicitPath(auth)
	if err != nil || !pending {
		return auth, path, err
	}
	changes := s.catalog
	if changes == nil {
		changes = cloneCatalogChanges(s.owner.catalog)
	}
	ref, err := e.temporaryNamespaceStagedLocked(auth, changes)
	if err != nil {
		return auth, nil, err
	}
	s.catalog = changes
	auth.recaptureCatalog(e.privateCatalogLocked(changes))
	auth.stagedTemporary = ref.Address
	s.currentSchemaResult = sqltype.Value{}
	s.searchPathResults = [2]sqltype.Value{}
	path, _, err = e.explicitPath(auth)
	return auth, path, err
}

func (n namespaceInquiryExpression) arguments() []BoundExpr {
	if n.arg == nil {
		return nil
	}
	return []BoundExpr{n.arg}
}

func (n namespaceInquiryExpression) validateStored() error {
	result := sqltype.Oid
	properties := ExprProperties{Volatility: Stable}
	if n.kind == otherTempNamespace {
		result = sqltype.Bool
		if n.arg == nil || n.arg.ResultUsage().Type == nil || n.arg.ResultUsage() != n.arg.ResultUsage().Type.Usage() || n.arg.ResultUsage().Type.ID() != sqltype.Oid {
			return fmt.Errorf("stuntgres: invalid stored namespace inquiry argument")
		}
		properties = n.arg.Properties()
		properties.Volatility = max(Stable, properties.Volatility)
	} else if n.kind != myTempNamespace || n.arg != nil {
		return fmt.Errorf("stuntgres: invalid stored namespace inquiry kind")
	}
	if n.usage.Type == nil || n.usage != n.usage.Type.Usage() || n.usage.Type.ID() != result || n.Properties() != properties {
		return fmt.Errorf("stuntgres: invalid stored namespace inquiry signature")
	}
	return nil
}
