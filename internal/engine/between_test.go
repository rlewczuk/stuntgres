package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestBetweenBindingProperties(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INTEGER,l INTEGER,u BIGINT)")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	for _, tc := range []struct {
		expr string
		ids  []sqltype.ID
		used []int
		row  bool
	}{
		{"a BETWEEN l AND u", nil, []int{0, 1, 2}, true},
		{"$1 BETWEEN l AND u", []sqltype.ID{23}, []int{1, 2}, true},
		{"a BETWEEN $1 AND $2", []sqltype.ID{23, 23}, []int{0}, true},
		{"$1 BETWEEN $2 AND $3", []sqltype.ID{25, 25, 25}, nil, false},
		{"$1 NOT BETWEEN SYMMETRIC l AND u", []sqltype.ID{23}, []int{1, 2}, true},
		{"0 BETWEEN -1 AND u", nil, []int{2}, true},
	} {
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		ast := statement(t, "SELECT "+tc.expr+" FROM t")
		ref, err := e.resolveReference(auth, ast.(*parser.Select).From.(*parser.TableReference).Name)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		bound, err := describe(op.ctx, e.analysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(tc.expr, err)
		}
		if len(bound.QuerySources) != 1 || bound.QuerySources[0].Ref != ref || !reflect.DeepEqual(bound.QuerySources[0].Uses, tc.used) {
			t.Fatal(tc.expr, bound.QuerySources, tc.used)
		}
		if len(bound.Parameters.Types) != len(tc.ids) {
			t.Fatal(bound.Parameters)
		}
		for i, u := range bound.Parameters.Types {
			if u.Type.ID() != tc.ids[i] {
				t.Fatal(tc.expr, u)
			}
		}
		node := bound.Query.Root.(*plan.ProjectNode).Expressions[0]
		p := node.Properties()
		if node.ResultUsage().Type.ID() != sqltype.Bool || p.RowDependent != tc.row || p.ParameterDependent != (len(tc.ids) > 0) || p.StageDependent || p.Volatility != expr.Immutable {
			t.Fatal(tc.expr, node, p)
		}
	}
}

func TestBetweenUpperEndpointAuthorization(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER reader")
	roleExecute(t, e, op, "CREATE TABLE secret(i INT,j INT)")
	ref := roleTableRef(t, e, "secret")
	reader, _ := e.Roles().Lookup("reader")
	column := ref
	column.Address.SubID = 1
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	roleError(t, e, op, "SELECT 0 BETWEEN i AND j FROM secret", "42501", "permission denied for table secret", "")
	roleError(t, e, op, "SELECT 7 FROM secret WHERE 0 BETWEEN 1 AND j", "42501", "permission denied for table secret", "")
}

func TestBetweenTemporalCaptureRebind(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(i INT)")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	first := sqltype.InputEnvironment{TransactionStart: time.Date(2003, 7, 7, 4, 5, 6, 0, time.UTC)}
	later := sqltype.InputEnvironment{TransactionStart: time.Date(2004, 8, 8, 12, 13, 14, 0, time.UTC)}
	for _, typ := range []string{"TIME(3)", "TIMESTAMP(3)"} {
		for _, form := range []string{"BETWEEN", "NOT BETWEEN", "BETWEEN SYMMETRIC", "NOT BETWEEN SYMMETRIC"} {
			e.mu.Lock()
			auth, err := e.authorizationLocked(op)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			ast := statement(t, "SELECT 'now' "+form+" "+typ+" 'now' AND "+typ+" 'now' FROM t")
			ref, err := e.resolveReference(auth, ast.(*parser.Select).From.(*parser.TableReference).Name)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			bound, err := describe(op.ctx, e.analysisHost(op), ast, auth, ref, first, nil, nil)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			desc := StatementDescription{temporals: bound.Prepared.Temporals}
			before := append([]analyze.TemporalConstant(nil), desc.temporals...)
			wantCaptures := 4
			if form == "BETWEEN SYMMETRIC" || form == "NOT BETWEEN SYMMETRIC" {
				wantCaptures = 8
			}
			if len(before) != wantCaptures {
				e.mu.Unlock()
				t.Fatal(before)
			}
			for i, capture := range before {
				if capture.Scalar != before[0].Scalar || (capture.Typmod != -1 && capture.Typmod != 3) || (i > 0 && capture.Offset < before[i-1].Offset) {
					e.mu.Unlock()
					t.Fatal("incoherent initial capture", before)
				}
			}
			for range 2 {
				rebound, err := describe(op.ctx, e.analysisHost(op), ast, auth, ref, later, &desc, nil)
				if err != nil {
					e.mu.Unlock()
					t.Fatal(err)
				}
				node := rebound.Query.Root.(*plan.ProjectNode).Expressions[0]
				value, evalErr := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
				truth, truthErr := value.Boolean()
				want := form == "BETWEEN" || form == "BETWEEN SYMMETRIC"
				if evalErr != nil || truthErr != nil || truth != want || !reflect.DeepEqual(desc.temporals, before) || len(rebound.Prepared.Temporals) != 0 {
					e.mu.Unlock()
					t.Fatal("frozen capture/result changed", value, evalErr, truthErr, desc.temporals, rebound.Prepared.Temporals)
				}
			}
			e.mu.Unlock()
		}
	}
}
