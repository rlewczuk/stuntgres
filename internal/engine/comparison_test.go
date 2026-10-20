package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestCompareOpsBindingPropertiesAndOccurrences(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(i INT,j BIGINT,b BOOL)")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	for _, tc := range []struct {
		q              string
		id             sqltype.ID
		row, parameter bool
		used           []int
	}{
		{"SELECT 1<i FROM t", 0, true, false, []int{0}},
		{"SELECT 1<$1 FROM t", sqltype.Int4, false, true, nil},
		{"SELECT $1<i FROM t", sqltype.Int4, true, true, []int{0}},
		{"SELECT i=$1,j=$1 FROM t", sqltype.Int4, true, true, []int{0, 1}},
		{"SELECT j=$1,i=$1 FROM t", sqltype.Int8, true, true, []int{1, 0}},
		{"SELECT $1=$1 FROM t", sqltype.Text, false, true, nil},
		{"SELECT $1<$2 FROM t", sqltype.Text, false, true, nil},
		{"SELECT NULL=NULL FROM t", 0, false, false, nil},
	} {
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		ast := statement(t, tc.q)
		ref, err := e.resolveReference(auth, ast.(*parser.Select).From.(*parser.TableReference).Name)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		bound, err := describe(op.ctx, e.analysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(tc.q, err)
		}
		if len(bound.querySources) != 1 || bound.querySources[0].Ref != ref || !reflect.DeepEqual(bound.querySources[0].Uses, tc.used) {
			t.Fatal(tc.q, bound.querySources, tc.used)
		}
		for _, u := range bound.parameters.Types {
			if u.Type.ID() != tc.id {
				t.Fatal(tc.q, u)
			}
		}
		project := bound.query.Root.(*plan.ProjectNode)
		for i, node := range project.Expressions {
			comparison, ok := node.(plan.ComparisonExpr)
			if !ok {
				t.Fatal(node)
			}
			p := comparison.Properties()
			if p.RowDependent != tc.row || p.ParameterDependent != tc.parameter || p.StageDependent || p.Volatility != expr.Immutable {
				t.Fatal(tc.q, p)
			}
			raw := ast.(*parser.Select).Targets[i].(parser.OperatorExpr)
			if comparison.ResultUsage().Type.ID() != sqltype.Bool || comparison.SourcePosition() != raw.SourcePosition() {
				t.Fatal(comparison)
			}
			result := project.Schema()[i]
			if result.Name != "?column?" || result.OriginTable != 0 || result.OriginAttribute != 0 || result.NullabilityKnown {
				t.Fatal(result)
			}
			for j, child := range []expr.Expr{comparison.Left, comparison.Right} {
				if child.SourcePosition() != raw.Args[j].SourcePosition() {
					t.Fatal(child.SourcePosition(), raw.Args[j].SourcePosition())
				}
				if child.ResultUsage().Type == nil || !child.ResultUsage().Type.Concrete() {
					t.Fatal("provisional child escaped", child)
				}
				if col, ok := child.(plan.ColumnExpr); ok {
					found := false
					for _, d := range bound.dependencies {
						if d.Ref == col.Origin {
							found = true
						}
					}
					if !found || col.Origin.Address.SubID != int32(col.Slot+1) {
						t.Fatal(col, bound.dependencies)
					}
				}
			}
		}
	}
}

func TestCompareOpsUnknownOccurrenceConflict(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	p := plan.ParameterExpr{Info: expr.Info{Pos: parser.Position{Character: 8}, Property: expr.Properties{ParameterDependent: true}}, Number: 1}
	b.params.parameters[1] = b.typ(sqltype.Int4).Usage()
	_, err := b.coerceParameter(p, b.typ(sqltype.Text).Usage())
	state(t, err, "42P08")
	// A concrete occurrence never invokes unknown coercion or assignment policy.
	raw := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "="}}}, Args: []parser.Expr{parser.ParameterExpr{Number: 1}, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 2147483648}}}}
	node, err := b.bindComparison(raw)
	if err != nil {
		t.Fatal(err)
	}
	c := node.(plan.ComparisonExpr)
	if c.Left.ResultUsage().Type.ID() != sqltype.Int4 || c.Right.ResultUsage().Type.ID() != sqltype.Int8 || b.params.parameters[1].Type.ID() != sqltype.Int4 {
		t.Fatal(c, b.params.parameters)
	}
}

func TestCompareOpsRightOnlyAuthorization(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER reader")
	roleExecute(t, e, op, "CREATE TABLE secret(i INT,j INT)")
	roleExecute(t, e, op, "INSERT INTO secret VALUES(1,2)")
	ref := roleTableRef(t, e, "secret")
	reader, _ := e.Roles().Lookup("reader")
	column := ref
	column.Address.SubID = 1
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	roleExecute(t, e, op, "SELECT 0<i FROM secret").Rows.Close()
	roleError(t, e, op, "SELECT 0<j FROM secret", "42501", "permission denied for table secret", "")
	roleError(t, e, op, "SELECT 7 FROM secret WHERE 0<j", "42501", "permission denied for table secret", "")
}

func TestCompareOpsTemporalCaptureRebind(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(i INT)")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	first := sqltype.InputEnvironment{TransactionStart: time.Date(2003, 7, 7, 4, 5, 6, 0, time.UTC)}
	later := sqltype.InputEnvironment{TransactionStart: time.Date(2004, 8, 8, 12, 13, 14, 0, time.UTC)}
	for _, q := range []string{"SELECT 'now'=TIME(0) 'now' FROM t", "SELECT 'now'=TIMESTAMP(0) 'now' FROM t"} {
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		ast := statement(t, q)
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
		desc := StatementDescription{temporals: bound.prepared.temporals}
		before := append([]temporalConstant(nil), desc.temporals...)
		for range 2 {
			rebound, err := describe(op.ctx, e.analysisHost(op), ast, auth, ref, later, &desc, nil)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			c := rebound.query.Root.(*plan.ProjectNode).Expressions[0].(plan.ComparisonExpr)
			if c.Left.ResultUsage().Typmod != -1 || c.Right.ResultUsage().Typmod != 0 {
				e.mu.Unlock()
				t.Fatal(c)
			}
			value, err := c.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
			truth, truthErr := value.Boolean()
			if err != nil || truthErr != nil || !truth || !reflect.DeepEqual(before, desc.temporals) {
				e.mu.Unlock()
				t.Fatal(value, err, truthErr, desc.temporals)
			}
		}
		e.mu.Unlock()
	}
}
