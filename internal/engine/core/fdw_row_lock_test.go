package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
)

type rowMarkOwnerProbe struct {
	fdwUnitLifecycle
	requests []catalog.ForeignRequest
	begins   int
}

func (p *rowMarkOwnerProbe) ForeignTransactionCapabilities(_ context.Context, request catalog.ForeignRequest) (catalog.ForeignTransactionCapabilities, error) {
	p.requests = append(p.requests, request)
	return catalog.ForeignTransactionCapabilities{StatementRollback: true, TransactionRollback: true, Savepoints: true}, nil
}
func (p *rowMarkOwnerProbe) BeginForeignTransaction(_ context.Context, request catalog.ForeignRequest) (catalog.ForeignTransaction, error) {
	p.requests = append(p.requests, request)
	p.begins++
	return p.tx, nil
}

func TestForeignRowMarkOwnerCharacteristics(t *testing.T) {
	for _, test := range []struct {
		options   session.TxOptions
		isolation string
	}{
		{session.TxOptions{}, "read committed"},
		{session.TxOptions{Isolation: session.ReadUncommitted}, "read uncommitted"},
		{session.TxOptions{Isolation: session.ReadCommitted}, "read committed"},
		{session.TxOptions{Isolation: session.RepeatableRead}, "repeatable read"},
		{session.TxOptions{Isolation: session.Serializable, ReadOnly: true, Deferrable: true, DeferrableSet: true}, "serializable"},
	} {
		t.Run(test.isolation, func(t *testing.T) {
			e := engine.New()
			sess := sessionStart(t, e, nil)
			op := transactionOperation(t, sess)
			defer op.Done()
			ref, err := e.Begin(op, test.options)
			if err != nil {
				t.Fatal(err)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			scope.TxRef = ref
			defer scope.Finish(errors.New("unit cleanup"))
			provider := &rowMarkOwnerProbe{fdwUnitLifecycle: fdwUnitLifecycle{tx: &fdwUnitTransaction{}}}
			// Deliberately false caller-supplied facts cannot override the owner.
			request := catalog.ForeignRequest{HandlerOID: 1, ServerOID: 2, MappingOID: 3, RoleOID: 4, Characteristics: &catalog.ForeignTransactionCharacteristics{Isolation: "invented"}}
			first, err := core.ScopeEnlistForeign(scope, op.Context(), provider, request, false)
			if err != nil {
				t.Fatal(err)
			}
			second, err := core.ScopeEnlistForeign(scope, op.Context(), provider, request, true)
			if err != nil || first != second || provider.begins != 1 || len(provider.requests) != 2 {
				t.Fatal("participant restarted/reconfigured", err)
			}
			for _, observed := range provider.requests {
				modes := observed.Characteristics
				if modes == nil || modes.Isolation != test.isolation || modes.ReadOnly != test.options.ReadOnly || modes.Deferrable != test.options.Deferrable || observed.Transaction != nil {
					t.Fatal("handshake did not receive sole-owner execution modes", observed)
				}
			}
			provider.requests[0].Characteristics.Isolation = "mutated"
			if provider.requests[1].Characteristics.Isolation != test.isolation || request.Characteristics.Isolation != "invented" {
				t.Fatal("capability/begin requests share mutable mode capture")
			}
		})
	}
}
