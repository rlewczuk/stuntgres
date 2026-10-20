package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDomainCheckNominalLiteralAdmissionInternal(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.nominal_positive AS integer CHECK(VALUE>0)"))
	for _, tc := range []struct{ query, bindCode, evalCode string }{
		{"SELECT nominal_positive '1'", "", ""},
		{"SELECT public.nominal_positive '0'", "", "23514"},
		{"SELECT uuid 'not-a-uuid'", "0A000", ""},
		{`SELECT "name" 'x'`, "0A000", ""},
		{"SELECT public.int4 '1'", "42704", ""},
	} {
		t.Run(tc.query, func(t *testing.T) {
			raw := statement(t, tc.query).(*parser.Select).Targets[0].(parser.TypedLiteralExpr)
			if !raw.RequiresDomain {
				t.Fatal("nominal syntax lost domain-only admission fact")
			}
			e.mu.Lock()
			auth, err := e.authorizationLocked(op)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			binder := newExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
			binder.Query.Auth = &auth
			node, err := binder.Bind(raw, expressionPolicy{Context: ProjectionContext})
			e.mu.Unlock()
			if tc.bindCode != "" {
				var d *sqlstate.Error
				if !errors.As(err, &d) || d.Code != tc.bindCode {
					t.Fatal("nominal admission", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Catalog: auth.Catalog, Input: binder.Input}}
			value, err := node.Eval(&evaluation)
			if tc.evalCode != "" {
				var d *sqlstate.Error
				if !errors.As(err, &d) || d.Code != tc.evalCode {
					t.Fatal("domain literal conversion", err)
				}
				return
			}
			if err != nil || !value.Type().IsDomain() {
				t.Fatal("lost logical domain value", value, err)
			}
		})
	}
}
