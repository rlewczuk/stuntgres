package functions

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestTransactionInquiryTimestampDemand(t *testing.T) {
	d := temporalTestTypes()
	plain := &temporalTestContext{Context: context.Background()}
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "timestamp", Usage: d[sqltype.Timestamptz].Usage()}, {Attribute: 2, Name: "roident", Usage: d[sqltype.Oid].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	childErr := errors.New("timestamp child failed")
	for _, kind := range []commitTimestampKind{commitTimestampScalar, commitTimestampOrigin} {
		result := d[sqltype.Timestamptz]
		if kind == commitTimestampOrigin {
			result = shape
		}
		n := commitTimestampExpression{Info: expr.Info{Usage: result.Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, kind: kind, arg: temporalTestArgument{Info: expr.Info{Usage: d[sqltype.Xid].Usage()}, err: childErr}}
		if _, err := n.Eval(plain); !errors.Is(err, childErr) {
			t.Fatal("child before runtime assertion", err)
		}
		n.arg = temporalTestLiteral(sqltype.Null(d[sqltype.Xid]))
		value, err := n.Eval(plain)
		if err != nil || !value.IsNull() || value.Type() != result {
			t.Fatal("whole typed NULL without runtime", value, err)
		}
		for xid := uint32(0); xid <= 3; xid++ {
			value, err := sqltype.XIDValue(d[sqltype.Xid], xid)
			if err != nil {
				t.Fatal(err)
			}
			n.arg = temporalTestLiteral(value)
			got, err := n.Eval(plain)
			switch xid {
			case 0:
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != "cannot retrieve commit timestamp for transaction 0" {
					t.Fatal("zero before authority", err)
				}
			case 1, 2:
				if err != nil || got.Type() != result {
					t.Fatal("special bypass runtime", got, err)
				}
				if kind == commitTimestampScalar {
					if !got.IsNull() {
						t.Fatal("scalar special is NULL")
					}
				} else if got.IsNull() || !got.CompositeField(0).IsNull() || !got.CompositeField(1).IsNull() {
					t.Fatal("origin special is a present all-NULL record", got)
				}
			case 3:
				if err == nil || !strings.Contains(err.Error(), "missing transaction inquiry runtime") {
					t.Fatal("normal xid demands authority", err)
				}
			}
		}
	}
	n := commitTimestampExpression{kind: commitTimestampLast}
	if _, err := n.Eval(plain); err == nil || !strings.Contains(err.Error(), "missing transaction inquiry runtime") {
		t.Fatal("last demands authority", err)
	}
}
