package functions

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

// No operation implementation: reaching an authority method on these contexts
// is a test failure rather than a simulated transaction/visibility store.
type inquiryDemandContext struct {
	*temporalTestContext
	TransactionInquiryRuntime
	contextErr error
	scopeErr   error
}

func (c *inquiryDemandContext) CheckTransactionInquiryContext() error  { return c.contextErr }
func (c *inquiryDemandContext) RequireTransactionIdentityScope() error { return c.scopeErr }

func TestTransactionInquiryIdentityDemand(t *testing.T) {
	d := temporalTestTypes()
	childErr := errors.New("child evaluation failed")
	contextErr := errors.New("identity context rejected")
	runtime := &inquiryDemandContext{temporalTestContext: &temporalTestContext{Context: context.Background()}, contextErr: contextErr}
	n := transactionIdentityExpression{Info: expr.Info{Usage: d[sqltype.Int4].Usage()}, kind: identityAge, arg: temporalTestArgument{Info: expr.Info{Usage: d[sqltype.Xid].Usage()}, err: childErr}}
	if _, err := n.Eval(runtime); !errors.Is(err, contextErr) {
		t.Fatal("context must precede child", err)
	}
	runtime.contextErr = nil
	if _, err := n.Eval(runtime); !errors.Is(err, childErr) {
		t.Fatal("child error before scope", err)
	}
	for kind := identityAge; kind <= identityMembers; kind++ {
		if kind == identityCurrent || kind == identityAssigned {
			continue
		}
		formal, result := d[sqltype.Xid], d[sqltype.Int4]
		if kind == identityStatus {
			formal, result = d[sqltype.Xid8], d[sqltype.Text]
		}
		n := transactionIdentityExpression{Info: expr.Info{Usage: result.Usage()}, kind: kind, arg: temporalTestLiteral(sqltype.Null(formal))}
		value, err := n.Eval(runtime)
		if err != nil || !value.IsNull() || value.Type() != result {
			t.Fatal("strict typed NULL before scope", kind, value, err)
		}
		if kind == identityMembers {
			rows, err := n.openMembers(runtime)
			if err != nil || len(rows) != 0 {
				t.Fatal("strict member NULL is empty", err)
			}
		}
	}
	xid, err := sqltype.XIDValue(d[sqltype.Xid], 3)
	if err != nil {
		t.Fatal(err)
	}
	n.kind, n.arg = identityMembers, temporalTestLiteral(xid)
	runtime.scopeErr = errors.New("stuntgres: transaction identity lacks statement scope")
	if _, err := n.Eval(runtime); !errors.Is(err, runtime.scopeErr) {
		t.Fatal("member scalar scope precedes invocation", err)
	}
	runtime.scopeErr = nil
	if _, err := n.Eval(runtime); err == nil || err.Error() != "stuntgres: MultiXact set call requires an invocation" {
		t.Fatal("member scalar invocation", err)
	}
}
