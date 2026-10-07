package engine

import (
	"context"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
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
		bound, err := e.describe(op.ctx, ast, auth, ref, sqltype.InputEnvironment{}, op, nil)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(tc.q, err)
		}
		if !reflect.DeepEqual(bound.usedColumns, tc.used) {
			t.Fatal(tc.q, bound.usedColumns, tc.used)
		}
		for _, u := range bound.parameters.Types {
			if u.Type.ID() != tc.id {
				t.Fatal(tc.q, u)
			}
		}
		project := bound.query.Root.(*projectNode)
		for i, node := range project.expressions {
			comparison, ok := node.(comparisonExpression)
			if !ok {
				t.Fatal(node)
			}
			p := comparison.Properties()
			if p.RowDependent != tc.row || p.ParameterDependent != tc.parameter || p.StageDependent || p.Volatility != Immutable {
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
			for j, child := range []BoundExpr{comparison.left, comparison.right} {
				if child.SourcePosition() != raw.Args[j].SourcePosition() {
					t.Fatal(child.SourcePosition(), raw.Args[j].SourcePosition())
				}
				if child.ResultUsage().Type == nil || !child.ResultUsage().Type.Concrete() {
					t.Fatal("provisional child escaped", child)
				}
				if col, ok := child.(columnExpression); ok {
					found := false
					for _, d := range bound.dependencies {
						if d.ref == col.origin {
							found = true
						}
					}
					if !found || col.origin.Address.SubID != int32(col.slot+1) {
						t.Fatal(col, bound.dependencies)
					}
				}
			}
		}
	}
}

func TestCompareOpsUnknownOccurrenceConflict(t *testing.T) {
	e := New()
	b := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
	p := parameterExpression{expressionInfo: expressionInfo{pos: parser.Position{Character: 8}, properties: ExprProperties{ParameterDependent: true}}, number: 1}
	b.parameters[1] = b.typ(sqltype.Int4).Usage()
	_, err := b.coerceParameter(p, b.typ(sqltype.Text).Usage())
	state(t, err, "42P08")
	// A concrete occurrence never invokes unknown coercion or assignment policy.
	raw := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "="}}}, Args: []parser.Expr{parser.ParameterExpr{Number: 1}, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 2147483648}}}}
	node, err := b.bindComparison(raw)
	if err != nil {
		t.Fatal(err)
	}
	c := node.(comparisonExpression)
	if c.left.ResultUsage().Type.ID() != sqltype.Int4 || c.right.ResultUsage().Type.ID() != sqltype.Int8 || b.parameters[1].Type.ID() != sqltype.Int4 {
		t.Fatal(c, b.parameters)
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
	if err := e.SetObjectACL(context.Background(), column, ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: e.roles.names["postgres"], Grantee: ACLGrantee{Role: reader.Ref}, Privileges: SelectPrivilege}}}); err != nil {
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
		bound, err := e.describe(op.ctx, ast, auth, ref, first, op, nil)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		desc := StatementDescription{temporals: bound.temporals}
		if len(desc.temporals) != 2 || desc.temporals[0].offset >= desc.temporals[1].offset || desc.temporals[0].typmod != -1 || desc.temporals[1].typmod != 0 || desc.temporals[0].scalar != desc.temporals[1].scalar {
			e.mu.Unlock()
			t.Fatal(desc.temporals)
		}
		before := append([]temporalConstant(nil), desc.temporals...)
		for range 2 {
			rebound, err := e.describe(op.ctx, ast, auth, ref, later, op, &desc)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			c := rebound.query.Root.(*projectNode).expressions[0].(comparisonExpression)
			if c.left.ResultUsage().Typmod != -1 || c.right.ResultUsage().Typmod != 0 {
				e.mu.Unlock()
				t.Fatal(c)
			}
			value, err := c.Eval(&EvalContext{Context: context.Background()})
			truth, truthErr := value.Boolean()
			if err != nil || truthErr != nil || !truth || !reflect.DeepEqual(before, desc.temporals) {
				e.mu.Unlock()
				t.Fatal(value, err, truthErr, desc.temporals)
			}
		}
		e.mu.Unlock()
	}
}
