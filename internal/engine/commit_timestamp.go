package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type commitTimestampKind uint8

const (
	commitTimestampScalar commitTimestampKind = iota
	commitTimestampOrigin
	commitTimestampLast
)

func (k commitTimestampKind) name() string {
	switch k {
	case commitTimestampScalar:
		return "pg_xact_commit_timestamp"
	case commitTimestampOrigin:
		return "pg_xact_commit_timestamp_origin"
	default:
		return "pg_last_committed_xact"
	}
}

type commitTimestampExpression struct {
	expr.Info
	kind commitTimestampKind
	arg  expr.Expr
}

func (n commitTimestampExpression) validate() error {
	if n.kind > commitTimestampLast || !n.Usage.Valid() || n.Property.Volatility != expr.Volatile {
		return fmt.Errorf("stuntgres: invalid stored commit timestamp provider")
	}
	if (n.arg == nil) != (n.kind == commitTimestampLast) {
		return fmt.Errorf("stuntgres: invalid commit timestamp arity")
	}
	if n.arg != nil && n.arg.ResultUsage().Type.ID() != sqltype.Xid {
		return fmt.Errorf("stuntgres: invalid commit timestamp argument")
	}
	if n.kind == commitTimestampScalar {
		if n.Usage.Type.ID() != sqltype.Timestamptz {
			return fmt.Errorf("stuntgres: invalid commit timestamp result")
		}
	} else {
		count := 2
		if n.kind == commitTimestampLast {
			count = 3
		}
		if !n.Usage.Type.IsComposite() || n.Usage.Type.CompositeLen() != count {
			return fmt.Errorf("stuntgres: invalid commit timestamp record")
		}
		for i := range count {
			name, id := "timestamp", sqltype.Timestamptz
			if i == count-1 {
				name, id = "roident", sqltype.Oid
			} else if count == 3 && i == 0 {
				name, id = "xid", sqltype.Xid
			}
			member := n.Usage.Type.CompositeMember(i)
			if member.Name != name || member.Usage.Type.ID() != id {
				return fmt.Errorf("stuntgres: invalid commit timestamp OUT field")
			}
		}
	}
	return nil
}

func (b *expressionBinder) bindCommitTimestamp(raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := commitTimestampScalar
	if name == "pg_xact_commit_timestamp_origin" {
		kind = commitTimestampOrigin
	}
	if name == "pg_last_committed_xact" {
		kind = commitTimestampLast
	}
	var operand functions.Argument
	var one [1]string
	var names []string
	if len(raw.Args) <= 1 {
		names = one[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i == 0 {
			operand = arg
		}
		if names != nil {
			names[i] = "unknown"
			if arg.Usage.Type != nil {
				names[i] = b.diagnosticTypeName(arg.Usage.Type)
			}
			names[i] = functions.NamedArgumentType(raw, i, names[i])
		}
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	count := 1
	if kind == commitTimestampLast {
		count = 0
	}
	valid := visible && name == kind.name() && !raw.ResolutionOnly && !raw.Variadic && len(raw.Args) == count
	if len(raw.ArgNames) != 0 {
		valid = valid && kind == commitTimestampOrigin && len(raw.ArgNames) == 1 && raw.ArgNames[0].Text == "xid"
	}
	if count == 1 && operand.Usage.Type != nil {
		valid = valid && operand.Usage.Type.BaseType() == b.typ(sqltype.Xid)
	}
	if !valid {
		return nil, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, false)
	}
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, err
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	var arg expr.Expr
	properties := expr.Properties{Volatility: expr.Volatile}
	if count == 1 {
		arg, err = b.CoerceArgument(operand, functions.CoercionRequest{Intent: functions.ConditionalArgument, Target: b.typ(sqltype.Xid).Usage(), Label: name})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	usage := b.typ(sqltype.Timestamptz).Usage()
	if kind != commitTimestampScalar {
		members := make([]sqltype.CompositeMember, 0, 3)
		if kind == commitTimestampLast {
			members = append(members, sqltype.CompositeMember{Attribute: 1, Name: "xid", Usage: b.typ(sqltype.Xid).Usage()})
		}
		members = append(members, sqltype.CompositeMember{Attribute: int32(len(members) + 1), Name: "timestamp", Usage: usage})
		members = append(members, sqltype.CompositeMember{Attribute: int32(len(members) + 1), Name: "roident", Usage: b.typ(sqltype.Oid).Usage()})
		shape, err := sqltype.RecordShape(members)
		if err != nil {
			return nil, err
		}
		usage = shape.Usage()
	}
	formal := [1]sqltype.ID{sqltype.Xid}
	info, err := b.builtinRoutineInfo(b.info(usage, raw.Pos, properties), catalogRoutineProvider, name, formal[:count]...)
	if err != nil {
		return nil, err
	}
	return commitTimestampExpression{info, kind, arg}, nil
}

func (n commitTimestampExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	var xid uint32
	if n.arg != nil {
		value, err := n.arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			return sqltype.Null(n.Usage.Type), nil
		}
		xid, err = value.XID()
		if err != nil {
			return sqltype.Value{}, err
		}
		if xid == 0 {
			return sqltype.Value{}, sqlError("22023", "cannot retrieve commit timestamp for transaction 0")
		}
	}
	var entry commitTimestampEntry
	if n.kind == commitTimestampLast || xid >= 3 {
		scope := c.liveScope()
		if scope == nil {
			return sqltype.Value{}, fmt.Errorf("stuntgres: missing commit timestamp scope")
		}
		e := scope.op.session.Engine()
		e.mu.Lock()
		if !e.visibility.TracksCommitTimestamps() {
			e.mu.Unlock()
			return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "could not get commit timestamp data", Hint: `Make sure the configuration parameter "track_commit_timestamp" is set.`}
		}
		if n.kind == commitTimestampLast {
			xid, entry = e.visibility.LastCommittedXID(), e.visibility.LastCommitTimestamp()
		} else {
			entry = e.visibility.LookupCommitTimestampLocked(xid)
		}
		e.mu.Unlock()
	}
	if n.kind == commitTimestampScalar {
		if !entry.Present {
			return sqltype.Null(n.Usage.Type), nil
		}
		return sqltype.TimestampFromTime(n.Usage.Type, entry.Stamp)
	}
	shape := n.Usage.Type
	fields := make([]sqltype.Value, shape.CompositeLen())
	for i := range fields {
		fields[i] = sqltype.Null(shape.CompositeMember(i).Usage.Type)
	}
	if entry.Present {
		i := 0
		if n.kind == commitTimestampLast {
			var err error
			fields[0], err = sqltype.XIDValue(shape.CompositeMember(0).Usage.Type, xid)
			if err != nil {
				return sqltype.Value{}, err
			}
			i++
		}
		var err error
		fields[i], err = sqltype.TimestampFromTime(shape.CompositeMember(i).Usage.Type, entry.Stamp)
		if err != nil {
			return sqltype.Value{}, err
		}
		fields[i+1], err = sqltype.OIDValue(shape.CompositeMember(i+1).Usage.Type, entry.Origin)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.OwnedCompositeValue(shape, fields)
}
