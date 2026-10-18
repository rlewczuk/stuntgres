package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"sync"
	"testing"
)

func TestExpressionBindingsAndTypedTruth(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(id INT,v INT,f BOOL)")
	run(t, e, "INSERT INTO t VALUES(1,1,TRUE),(2,NULL,NULL),(3,2,FALSE),(4,1,TRUE)")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	ast := statement(t, "SELECT $1,v,NULL FROM t WHERE v=$2 ORDER BY id")
	ref, err := e.resolveReference(auth, ast.(*parser.Select).From.(*parser.TableReference).Name)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	b, err := e.describe(op.ctx, ast, auth, ref, sqltype.InputEnvironment{}, op, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if b.parameters.Count != 2 || b.parameters.Types[0].Type.ID() != sqltype.Text || b.parameters.Types[1].Type.ID() != sqltype.Int4 || b.parameters.Types[1].Typmod != -1 {
		t.Fatal(b.parameters)
	}
	project := b.query.Root.(*projectNode)
	filter := project.child.(*sortNode).child.(*filterNode)
	if len(project.expressions) != 3 || project.expressions[0].ResultUsage().Type.ID() != sqltype.Text || project.expressions[2].ResultUsage().Type.ID() != sqltype.Text {
		t.Fatal(project.Schema())
	}
	column := project.expressions[1].(columnExpression)
	if column.origin.Domain() != e.catalog.domain || column.origin.Address.SubID != 2 || column.slot != 1 || column.scope == 0 || column.frame == 0 || column.SourcePosition().Character != 11 {
		t.Fatal(column)
	}
	if !column.Properties().RowDependent || column.Properties().ParameterDependent || column.Properties().Volatility != expr.Immutable || !project.expressions[0].Properties().ParameterDependent {
		t.Fatal("dependence metadata")
	}
	refs := map[ids.ObjectRef]bool{}
	for _, d := range b.dependencies {
		if refs[d.ref] || d.ref.Domain() != e.catalog.domain || d.revision == 0 {
			t.Fatal(d)
		}
		refs[d.ref] = true
	}
	if !refs[column.origin] || len(refs) < 4 {
		t.Fatal(b.dependencies)
	}
	typ, _ := e.CatalogRead().LookupType(sqltype.Bool)
	for _, tc := range []struct {
		v    sqltype.Value
		want Truth
	}{{sqltype.Boolean(typ, true), True}, {sqltype.Boolean(typ, false), False}, {sqltype.Null(typ), Unknown}} {
		got, err := TruthOf(tc.v)
		if err != nil || got != tc.want {
			t.Fatal(got, err)
		}
	}
	if _, err := TruthOf(sqltype.Value{}); err == nil {
		t.Fatal("invalid value accepted")
	}
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	if _, err := TruthOf(sqltype.Null(integer)); err == nil {
		t.Fatal("nonboolean accepted")
	}
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	frame := []sqltype.Value{sqltype.Null(text), sqltype.Null(integer)}
	value, err := filter.predicate.Eval(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.Null(integer), sqltype.Null(integer), sqltype.Null(typ)}, Parameters: frame})
	if err != nil || value.Type() != typ || !value.IsNull() {
		t.Fatal(value, err)
	}
	if _, err := column.Eval(&EvalContext{Context: context.Background(), Scope: 2, Frame: 1, Row: frame}); err == nil {
		t.Fatal("wrong scope accepted")
	}
	var wg sync.WaitGroup
	for n := range 16 {
		wg.Go(func() {
			v := sqltype.String(text, strconv.Itoa(n))
			got, err := project.expressions[0].Eval(&EvalContext{Context: context.Background(), Parameters: []sqltype.Value{v}})
			actual, _ := got.Text()
			if err != nil || actual != strconv.Itoa(n) {
				t.Error(actual, err)
			}
		})
	}
	wg.Wait()
	if !reflect.DeepEqual(b.parameters.Types, []sqltype.Usage{text.Usage(), integer.Usage()}) {
		t.Fatal("execution mutated inference")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := project.expressions[0].Eval(&EvalContext{Context: ctx, Parameters: frame}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestExpressionScalarBehaviorAndSparseDiagnostics(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(id INT,v INT,f BOOL)")
	run(t, e, "INSERT INTO t VALUES(1,1,TRUE),(2,NULL,NULL),(3,2,FALSE),(4,1,TRUE)")
	for _, tc := range []struct {
		q    string
		want [][]driver.Value
	}{{"SELECT 7 FROM t WHERE v=1", [][]driver.Value{{int64(7)}, {int64(7)}}}, {"SELECT id FROM t WHERE f ORDER BY id", [][]driver.Value{{int64(1)}, {int64(4)}}}, {"SELECT id FROM t WHERE NULL", [][]driver.Value{}}, {"SELECT v FROM t WHERE v=NULL", [][]driver.Value{}}, {"SELECT id FROM t WHERE 'yes' ORDER BY id", [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}}} {
		r := run(t, e, tc.q)
		got := tuples(r)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.q, got, tc.want)
		}
		r.Rows.Close()
	}
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	_, err := binder.bind(parser.ParameterExpr{Number: 2147483647, Pos: parser.Position{Character: 8}}, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	_, err = binder.finish()
	state(t, err, "42P18")
}

func TestExpressionOccurrenceSensitiveFallback(t *testing.T) {
	e := New()
	for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Bool, sqltype.Text} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := parser.ParameterExpr{Number: 1, Pos: parser.Position{Character: 8}}
		target, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if target.ResultUsage().Type != nil {
			t.Fatal("target inferred before context")
		}
		expected := b.typ(id).Usage()
		later, err := b.bind(raw, expressionPolicy{context: projectionContext, expected: expected})
		if err != nil {
			t.Fatal(err)
		}
		if later.ResultUsage() != expected || target.ResultUsage().Type != nil {
			t.Fatal("later slot constraint rewrote earlier occurrence")
		}
		output, err := b.finalizeProjection(target)
		if id == sqltype.Text {
			if err != nil || output.ResultUsage() != expected {
				t.Fatal(output, err)
			}
			info, err := b.finish()
			if err != nil || info.Count != 1 || info.Types[0] != expected {
				t.Fatal(info, err)
			}
			continue
		}
		detail := "integer versus text"
		if id == sqltype.Bool {
			detail = "boolean versus text"
		}
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: detail, Position: "8"}
		var diagnostic *sqlstate.Error
		if output != nil || !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatal(output, err, diagnostic)
		}
	}
}

func TestExpressionActualColumnAuthorization(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER reader")
	roleExecute(t, e, op, "CREATE TABLE secret(id INT,v INT,f BOOL)")
	roleExecute(t, e, op, "INSERT INTO secret VALUES(1,1,TRUE)")
	ref := roleTableRef(t, e, "secret")
	reader, _ := e.Roles().Lookup("reader")
	column := ref
	column.Address.SubID = 2
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	for _, q := range []string{"SELECT 7 FROM secret", "SELECT v FROM secret", "SELECT 7 FROM secret WHERE v=1", "SELECT 7 FROM secret ORDER BY v", "SELECT (v+1)*2 FROM secret", "SELECT 7 FROM secret WHERE v+1=2"} {
		if _, err := e.Describe(op, statement(t, q)); err != nil {
			t.Fatal(err)
		}
		r := roleExecute(t, e, op, q)
		if r.Count != 1 {
			t.Fatal(q, r.Count)
		}
		r.Rows.Close()
	}
	before := e.catalog.state
	for _, q := range []string{"SELECT id FROM secret", "SELECT 7 FROM secret WHERE f", "SELECT 7 FROM secret ORDER BY id", "SELECT v FROM secret WHERE id=1", "SELECT id+v FROM secret", "SELECT v+1 FROM secret WHERE id+1=2"} {
		if _, err := e.Describe(op, statement(t, q)); err != nil {
			t.Fatal("ACL moved into Prepare", err)
		}
		roleError(t, e, op, q, "42501", "permission denied for table secret", "")
		if e.catalog.state != before {
			t.Fatal("denial mutated catalog")
		}
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	r := roleExecute(t, e, op, "SELECT id,v,f FROM secret")
	if !reflect.DeepEqual(tuples(r), [][]driver.Value{{int64(1), int64(1), true}}) {
		t.Fatal("denial mutated rows")
	}
	r.Rows.Close()
}

func TestExpressionInvalidValuesRemainInvariantErrors(t *testing.T) {
	ctx := &EvalContext{Context: context.Background(), Scope: 1, Frame: 1, Row: []sqltype.Value{{}}, Parameters: []sqltype.Value{{}}}
	for _, expression := range []expr.Expr{
		literalExpression{},
		columnExpression{scope: 1, frame: 1, slot: 0},
		parameterExpression{number: 1},
	} {
		value, err := expression.Eval(ctx)
		if err == nil || value.Type() != nil {
			t.Fatal("invalid zero became successful SQL NULL", value, err)
		}
	}
}
