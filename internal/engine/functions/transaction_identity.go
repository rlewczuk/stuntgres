package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
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
	expr.Info
	kind transactionIdentityKind
	arg  expr.Expr
}

func (n transactionIdentityExpression) validate() error {
	if n.kind > identityMembers || !n.Usage.Valid() {
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
		d := n.Usage.Type
		if !d.IsComposite() || d.CompositeLen() != 2 || d.CompositeMember(0).Name != "xid" || d.CompositeMember(0).Usage.Type.ID() != sqltype.Xid || d.CompositeMember(1).Name != "mode" || d.CompositeMember(1).Usage.Type.ID() != sqltype.Text {
			return fmt.Errorf("stuntgres: invalid MultiXact OUT shape")
		}
		return nil
	}
	if n.Usage.Type.ID() != result {
		return fmt.Errorf("stuntgres: invalid transaction identity result")
	}
	return nil
}
func (n transactionIdentityExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := transactionInquiryRuntime(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := runtime.CheckTransactionInquiryContext(); err != nil {
		return sqltype.Value{}, err
	}
	var xid uint32
	var full uint64
	if n.arg != nil {
		value, err := n.arg.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			return sqltype.Null(n.Usage.Type), nil
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
	switch n.kind {
	case identityAge, identityMultiAge:
		var age int32
		if n.kind == identityAge {
			age, err = runtime.TransactionAge(xid)
		} else {
			age, err = runtime.MultiTransactionAge(xid)
		}
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Integer(n.Usage.Type, int64(age))
	case identityCurrent, identityAssigned:
		full, err = runtime.CurrentTransactionID(n.kind == identityCurrent)
		if err != nil {
			return sqltype.Value{}, err
		}
		if full == 0 {
			return sqltype.Null(n.Usage.Type), nil
		}
		return sqltype.XID8Value(n.Usage.Type, full)
	case identityStatus:
		status, present, err := runtime.TransactionStatus(full)
		if err != nil {
			return sqltype.Value{}, err
		}
		if !present {
			return sqltype.Null(n.Usage.Type), nil
		}
		return sqltype.String(n.Usage.Type, status), nil
	case identityMembers:
		// Scalar misuse still checks statement scope before reporting invocation.
		err := runtime.RequireTransactionIdentityScope()
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Value{}, fmt.Errorf("stuntgres: MultiXact set call requires an invocation")
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid transaction identity operation")
}
func (n transactionIdentityExpression) openMembers(ctx expr.Context) ([]sqltype.Value, error) {
	runtime, err := transactionInquiryRuntime(ctx)
	if err != nil {
		return nil, err
	}
	if err := runtime.CheckTransactionInquiryContext(); err != nil {
		return nil, err
	}
	value, err := n.arg.Eval(ctx)
	if err != nil || value.IsNull() {
		return nil, err
	}
	xid, err := value.XID()
	if err != nil {
		return nil, err
	}
	return runtime.TransactionMembers(xid, n.Usage.Type)
}
