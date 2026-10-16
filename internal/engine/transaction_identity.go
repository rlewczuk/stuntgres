package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type transactionIdentityKind uint8

const (
	identityAge transactionIdentityKind = iota
	identityMultiAge
	identityCurrent
	identityAssigned
	identityStatus
	identityMembers
)

func (k transactionIdentityKind) name() string {
	switch k {
	case identityAge:
		return "age"
	case identityMultiAge:
		return "mxid_age"
	case identityCurrent:
		return "pg_current_xact_id"
	case identityAssigned:
		return "pg_current_xact_id_if_assigned"
	case identityStatus:
		return "pg_xact_status"
	case identityMembers:
		return "pg_get_multixact_members"
	}
	return ""
}

type transactionIdentityExpression struct {
	expressionInfo
	kind transactionIdentityKind
	arg  BoundExpr
}

func (n transactionIdentityExpression) validate() error {
	if n.kind > identityMembers || !n.usage.Valid() {
		return fmt.Errorf("stuntgres: invalid stored transaction identity provider")
	}
	count := n.kind != identityCurrent && n.kind != identityAssigned
	if (n.arg != nil) != count {
		return fmt.Errorf("stuntgres: invalid transaction identity arity")
	}
	formal := sqltype.Xid
	if n.kind == identityStatus {
		formal = sqltype.Xid8
	}
	if n.arg != nil && n.arg.ResultUsage().Type.BaseType().ID() != formal {
		return fmt.Errorf("stuntgres: invalid transaction identity argument")
	}
	result := sqltype.Int4
	switch n.kind {
	case identityCurrent, identityAssigned:
		result = sqltype.Xid8
	case identityStatus:
		result = sqltype.Text
	case identityMembers:
		d := n.usage.Type
		if !d.IsComposite() || d.CompositeLen() != 2 || d.CompositeMember(0).Name != "xid" || d.CompositeMember(0).Usage.Type.ID() != sqltype.Xid || d.CompositeMember(1).Name != "mode" || d.CompositeMember(1).Usage.Type.ID() != sqltype.Text {
			return fmt.Errorf("stuntgres: invalid MultiXact OUT shape")
		}
		return nil
	}
	if n.usage.Type.ID() != result {
		return fmt.Errorf("stuntgres: invalid transaction identity result")
	}
	return nil
}
func (b *expressionBinder) identityExpression(raw parser.FunctionExpr, kind transactionIdentityKind, arg BoundExpr) (BoundExpr, error) {
	usage := b.typ(sqltype.Int4).Usage()
	properties := ExprProperties{Volatility: Stable}
	if arg != nil {
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	switch kind {
	case identityCurrent, identityAssigned:
		usage = b.typ(sqltype.Xid8).Usage()
	case identityStatus:
		usage = b.typ(sqltype.Text).Usage()
		properties.Volatility = Volatile
	case identityMembers:
		shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "xid", Usage: b.typ(sqltype.Xid).Usage()}, {Attribute: 2, Name: "mode", Usage: b.typ(sqltype.Text).Usage()}})
		if err != nil {
			return nil, err
		}
		usage = shape.Usage()
		properties.Volatility = Volatile
	}
	formal := [1]sqltype.ID{sqltype.Xid}
	count := 1
	if kind == identityStatus {
		formal[0] = sqltype.Xid8
	}
	if arg == nil {
		count = 0
	}
	provider := catalogRoutineProvider
	if kind == identityAge {
		provider = datetimeRoutineProvider
	}
	info, err := b.builtinRoutineInfo(b.info(usage, raw.Pos, properties), provider, kind.name(), formal[:count]...)
	if err != nil {
		return nil, err
	}
	return transactionIdentityExpression{info, kind, arg}, nil
}
func (b *expressionBinder) bindTransactionIdentity(raw parser.FunctionExpr) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := identityMultiAge
	for candidate := identityMultiAge; candidate <= identityMembers; candidate++ {
		if candidate.name() == name {
			kind = candidate
			break
		}
	}
	var operand functions.Argument[BoundExpr]
	names := make([]string, len(raw.Args))
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i == 0 {
			operand = arg
		}
		names[i] = "unknown"
		if arg.Usage.Type != nil {
			names[i] = b.diagnosticTypeName(arg.Usage.Type)
		}
		names[i] = functionNamedType(raw, i, names[i])
	}
	if err := b.validateRangeInspectSyntax(raw); err != nil {
		return nil, err
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	count := 1
	if kind == identityCurrent || kind == identityAssigned {
		count = 0
	}
	formal := sqltype.Xid
	if kind == identityStatus {
		formal = sqltype.Xid8
	}
	valid := visible && name == kind.name() && !raw.ResolutionOnly && !raw.Variadic && len(raw.Args) == count
	if len(raw.ArgNames) != 0 {
		valid = valid && kind == identityMembers && len(raw.ArgNames) == 1 && raw.ArgNames[0].Text == "multixid"
	}
	if count == 1 && operand.Usage.Type != nil {
		valid = valid && operand.Usage.Type.BaseType() == b.typ(formal)
	}
	if !valid {
		return nil, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, false)
	}
	if err := rangeInspectDecorationError(raw); err != nil {
		return nil, err
	}
	if raw.Star {
		return nil, expressionError("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	var arg BoundExpr
	if count == 1 {
		arg, err = b.CoerceArgument(operand, functions.CoercionRequest{Intent: functions.ConditionalArgument, Target: b.typ(formal).Usage(), Label: name})
		if err != nil {
			return nil, err
		}
	}
	return b.identityExpression(raw, kind, arg)
}
func (n transactionIdentityExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var xid uint32
	var full uint64
	if n.arg != nil {
		value, err := n.arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			return sqltype.Null(n.usage.Type), nil
		}
		if n.kind == identityStatus {
			full, err = value.XID8()
		} else {
			xid, err = value.XID()
		}
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	scope := c.liveScope()
	if scope == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: transaction identity lacks statement scope")
	}
	e := scope.op.session.Engine()
	if n.kind == identityMultiAge {
		age, err := e.locks.multiAge(scope.ticket, xid)
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Integer(n.usage.Type, int64(age))
	}
	if n.kind == identityMembers {
		return sqltype.Value{}, fmt.Errorf("stuntgres: MultiXact set call requires an invocation")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if scope.txRef.epoch != e.state.epoch {
		return sqltype.Value{}, ErrCatalogReplaced
	}
	switch n.kind {
	case identityAge:
		age, err := e.transactionAgeLocked(scope.txRef, xid)
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Integer(n.usage.Type, int64(age))
	case identityCurrent:
		if err := e.assignTransactionXIDLocked(scope.txRef); err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.XID8Value(n.usage.Type, e.visibility[scope.txRef].fullXID)
	case identityAssigned:
		full = e.visibility[scope.txRef].fullXID
		if full == 0 {
			return sqltype.Null(n.usage.Type), nil
		}
		return sqltype.XID8Value(n.usage.Type, full)
	case identityStatus:
		status, present, err := e.transactionStatusLocked(full)
		if err != nil {
			return sqltype.Value{}, err
		}
		if !present {
			return sqltype.Null(n.usage.Type), nil
		}
		return sqltype.String(n.usage.Type, status), nil
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid transaction identity operation")
}
func (n transactionIdentityExpression) openMembers(c *EvalContext) ([]sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return nil, err
	}
	value, err := n.arg.Eval(c)
	if err != nil || value.IsNull() {
		return nil, err
	}
	xid, err := value.XID()
	if err != nil {
		return nil, err
	}
	scope := c.liveScope()
	if scope == nil {
		return nil, fmt.Errorf("stuntgres: MultiXact lacks statement scope")
	}
	members, err := scope.op.session.Engine().locks.multiXactMembers(scope.ticket, xid)
	if err != nil {
		return nil, err
	}
	rows := make([]sqltype.Value, len(members))
	for i, m := range members {
		x, err := sqltype.XIDValue(n.usage.Type.CompositeMember(0).Usage.Type, m.xid)
		if err != nil {
			return nil, err
		}
		mode := sqltype.String(n.usage.Type.CompositeMember(1).Usage.Type, m.mode.name())
		rows[i], err = sqltype.OwnedCompositeValue(n.usage.Type, []sqltype.Value{x, mode})
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}
