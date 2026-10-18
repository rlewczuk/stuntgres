package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type blockingInquiryKind uint8

const (
	ordinaryBlockingInquiry blockingInquiryKind = iota
	safeSnapshotBlockingInquiry
)

func (k blockingInquiryKind) name() string {
	switch k {
	case ordinaryBlockingInquiry:
		return "pg_blocking_pids"
	case safeSnapshotBlockingInquiry:
		return "pg_safe_snapshot_blocking_pids"
	}
	return ""
}

type blockingInquiryExpression struct {
	expr.Info
	kind blockingInquiryKind
	arg  expr.Expr
}

func (b *expressionBinder) bindBlockingInquiry(raw parser.FunctionExpr) (expr.Expr, error) {
	var operand comparisonOperand
	var single [1]string
	names := single[:0]
	if len(raw.Args) == 1 {
		names = single[:]
	} else if len(raw.Args) <= 100 {
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
			name := "unknown"
			if typ := arg.descriptor(); typ != nil {
				name = typ.DiagnosticName()
			}
			names[i] = functions.NamedArgumentType(raw, i, name)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := ordinaryBlockingInquiry
	if name == safeSnapshotBlockingInquiry.name() {
		kind = safeSnapshotBlockingInquiry
	}
	if !visible || name != kind.name() || len(raw.Args) != 1 || raw.Variadic || len(raw.ArgNames) != 0 {
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	target := b.typ(sqltype.Int4).Usage()
	arg, err := b.finalizeOperand(operand, target.Type)
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
	properties := arg.Properties()
	properties.Volatility = expr.Volatile
	info, err := b.builtinRoutineInfo(b.info(b.typ(target.Type.AssociatedArray()).Usage(), raw.Pos, properties), catalogRoutineProvider, kind.name(), sqltype.Int4)
	if err != nil {
		return nil, err
	}
	return blockingInquiryExpression{info, kind, arg}, nil
}

func (n blockingInquiryExpression) arguments() []expr.Expr { return []expr.Expr{n.arg} }

func (n blockingInquiryExpression) validateStored() error {
	if n.kind.name() == "" || n.arg == nil || n.arg.ResultUsage().Type == nil || n.arg.ResultUsage() != n.arg.ResultUsage().Type.Usage() || n.arg.ResultUsage().Type.ID() != sqltype.Int4 {
		return fmt.Errorf("stuntgres: invalid stored blocking inquiry argument")
	}
	properties := n.arg.Properties()
	properties.Volatility = expr.Volatile
	if n.Usage.Type == nil || n.Usage != n.Usage.Type.Usage() || n.Usage.Type.ID() != n.arg.ResultUsage().Type.AssociatedArray() || n.Usage.Type.Element() != n.arg.ResultUsage().Type || n.Properties() != properties {
		return fmt.Errorf("stuntgres: invalid stored blocking inquiry signature")
	}
	return nil
}

func (n blockingInquiryExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	value, err := n.arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	pid, err := value.Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	scope := c.liveScope()
	if scope == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: blocking inquiry lacks statement authority")
	}
	e := scope.op.session.Engine()
	// Validate the statement under its existing engine authority, then release
	// it before entering ordinary manager metadata. Never nest these mutexes.
	e.mu.Lock()
	_, err = scope.writeAuthorizationLocked(e)
	var elements []sqltype.Value
	if err == nil && pid > 0 && n.kind == safeSnapshotBlockingInquiry {
		elements, err = e.safeSnapshotBlockingPidsLocked(ids.SessionID(pid), n.Usage.Type.Element())
	}
	e.mu.Unlock()
	if err != nil {
		return sqltype.Value{}, err
	}
	if pid > 0 && n.kind == ordinaryBlockingInquiry {
		elements, err = e.blockingPidValues(ids.SessionID(pid), n.Usage.Type.Element())
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if n.kind.name() == "" {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid blocking inquiry kind")
	}
	var dimensions []sqltype.ArrayDimension
	if len(elements) != 0 {
		dimensions = []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}
	}
	return sqltype.OwnedArrayValue(n.Usage.Type, dimensions, elements)
}

func appendBlockingPid(values []sqltype.Value, typ *sqltype.Descriptor, token ids.SessionToken, prepared bool, domain *ids.Domain) ([]sqltype.Value, error) {
	pid, visible, err := lock.BlockingPid(token, prepared, domain)
	if err != nil {
		return nil, err
	}
	if !visible {
		return values, nil
	}
	value, err := sqltype.Integer(typ, int64(pid))
	if err != nil {
		return nil, err
	}
	return append(values, value), nil
}

// blockingPidValues projects the lock manager's blocking PIDs of one session.
func (e *Engine) blockingPidValues(pid ids.SessionID, typ *sqltype.Descriptor) ([]sqltype.Value, error) {
	pids, err := e.locks.BlockingPids(pid)
	if err != nil || len(pids) == 0 {
		return nil, err
	}
	values := make([]sqltype.Value, len(pids))
	for i, pid := range pids {
		if values[i], err = sqltype.Integer(typ, int64(pid)); err != nil {
			return nil, err
		}
	}
	return values, nil
}

// Caller holds Engine.mu. These flags describe an unresolved acquisition,
// unlike possible/ready alone, which also exist for nondeferrable readers.
func (e *Engine) safeSnapshotBlockingPidsLocked(pid ids.SessionID, typ *sqltype.Descriptor) ([]sqltype.Value, error) {
	for ref, node := range e.serializable.nodes {
		if ref.Domain() != e.catalog.Domain || ref.Epoch() != e.catalog.state.Epoch() || ref.Session() != (e.catalog.Domain.Session(pid)) || node.prepared || !node.captured || !node.readOnly || !node.deferrable || node.aborted || node.ended || node.committed || node.resolved || node.ready == nil {
			continue
		}
		var values []sqltype.Value
		for writer := range node.possible {
			if writer.ref.Domain() != e.catalog.Domain || writer.ref.Epoch() != ref.Epoch() || e.serializable.nodes[writer.ref] != writer || writer.aborted || writer.ended || writer.committed {
				continue
			}
			var err error
			values, err = appendBlockingPid(values, typ, writer.ref.Session(), writer.prepared, e.catalog.Domain)
			if err != nil {
				return nil, err
			}
		}
		return values, nil
	}
	return nil, nil
}
