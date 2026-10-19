package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"time"
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
func (n commitTimestampExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	var xid uint32
	if n.arg != nil {
		value, err := n.arg.Eval(ctx)
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
	var stamp time.Time
	var origin uint32
	var present bool
	if n.kind == commitTimestampLast || xid >= 3 {
		runtime, err := transactionInquiryRuntime(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		if n.kind == commitTimestampLast {
			xid, stamp, origin, present, err = runtime.LastCommittedTransaction()
		} else {
			stamp, origin, present, err = runtime.CommitTimestamp(xid)
		}
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if n.kind == commitTimestampScalar {
		if !present {
			return sqltype.Null(n.Usage.Type), nil
		}
		return sqltype.TimestampFromTime(n.Usage.Type, stamp)
	}
	shape := n.Usage.Type
	fields := make([]sqltype.Value, shape.CompositeLen())
	for i := range fields {
		fields[i] = sqltype.Null(shape.CompositeMember(i).Usage.Type)
	}
	if present {
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
		fields[i], err = sqltype.TimestampFromTime(shape.CompositeMember(i).Usage.Type, stamp)
		if err != nil {
			return sqltype.Value{}, err
		}
		fields[i+1], err = sqltype.OIDValue(shape.CompositeMember(i+1).Usage.Type, origin)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.OwnedCompositeValue(shape, fields)
}
