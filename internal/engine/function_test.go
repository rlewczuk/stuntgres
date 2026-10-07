package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func nullCountRaw(name string, args ...parser.Expr) parser.FunctionExpr {
	return parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args, Pos: parser.Position{Character: 8}}
}

func nullCountUnitDiagnostic(t *testing.T, err error, want sqlstate.Error) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	var got *sqlstate.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("got %#v (%v), want %#v", got, err, want)
	}
}

func TestNullCountResolver(t *testing.T) {
	e := New()
	for _, name := range []string{"num_nulls", "num_nonnulls"} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		_, err := b.bind(nullCountRaw(name), expressionPolicy{context: projectionContext})
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42883", Message: "function " + name + "() does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."})
		args := make([]parser.Expr, 100)
		for i := range args {
			args[i] = parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
		}
		bound, err := b.bind(nullCountRaw(name, args...), expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		v, err := bound.Eval(&EvalContext{Context: context.Background()})
		n, _ := v.Integer()
		want := int64(100)
		if name == "num_nonnulls" {
			want = 0
		}
		if err != nil || n != want || v.IsNull() || v.Type().ID() != sqltype.Int4 {
			t.Fatal(v, err)
		}
		// Child transformation errors precede the outer function's arity guard.
		args = append(args, parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}})
		args[0] = parser.ParameterExpr{Number: 0, Pos: parser.Position{Character: 18}}
		_, err = b.bind(nullCountRaw(name, args...), expressionPolicy{context: projectionContext})
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42P02", Message: "there is no parameter $0", Position: "18"})
		args[0] = parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
		_, err = b.bind(nullCountRaw(name, args...), expressionPolicy{context: projectionContext})
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"})
	}
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	arg := parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue, Pos: parser.Position{Character: 27}}}
	raw := nullCountRaw("num_nulls", arg)
	raw.Variadic = true
	_, err := b.bind(raw, expressionPolicy{context: projectionContext})
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42804", Message: "VARIADIC argument must be an array", Position: "27"})
	raw.Args = append(raw.Args, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}})
	_, err = b.bind(raw, expressionPolicy{context: projectionContext})
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42883", Message: "function num_nulls(unknown, integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."})
	_, err = b.bind(nullCountRaw("num_nulls", parser.ParameterExpr{Number: 0, Pos: parser.Position{Character: 18}}), expressionPolicy{context: projectionContext})
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42P02", Message: "there is no parameter $0", Position: "18"})
}

func TestNullCountAnyOccurrences(t *testing.T) {
	e := New()
	ctx := context.Background()
	for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Text, sqltype.Bool} {
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		parameter := parser.ParameterExpr{Number: 1, Pos: parser.Position{Character: 18}}
		bound, err := b.bind(nullCountRaw("num_nulls", parameter), expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if bound.ResultUsage() != b.typ(sqltype.Int4).Usage() || !bound.Properties().ParameterDependent {
			t.Fatal(bound)
		}
		if _, err := b.finish(); err == nil {
			t.Fatal("unresolved ANY escaped analysis")
		} else {
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42P18", Message: "could not determine data type of parameter $1"})
		}
		if err := b.constrain(1, b.typ(id).Usage(), parameter.Pos); err != nil {
			t.Fatal(err)
		}
		_, err = b.finish()
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "18"})
		// A concrete type known before call transformation is usable unchanged.
		known := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		if err := known.constrain(1, known.typ(id).Usage(), parameter.Pos); err != nil {
			t.Fatal(err)
		}
		call, err := known.bind(nullCountRaw("num_nonnulls", parameter), expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if info, err := known.finish(); err != nil || info.Count != 1 || info.Types[0] != known.typ(id).Usage() {
			t.Fatal(info, err)
		}
		v, err := call.Eval(&EvalContext{Context: ctx, Parameters: []sqltype.Value{sqltype.Null(known.typ(id))}})
		n, _ := v.Integer()
		if err != nil || n != 0 {
			t.Fatal(v, err)
		}
	}
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	_, err := b.bind(nullCountRaw("num_nulls", parser.ParameterExpr{Number: 2147483647}), expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.parameters) != 1 || len(b.anyParameters) != 1 {
		t.Fatal("sparse analysis expanded parameter index")
	}
	_, err = b.finish()
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42P18", Message: "could not determine data type of parameter $1"})
}

type nullCountWitness struct {
	expressionInfo
	value sqltype.Value
	err   error
	calls *int
}

func (w nullCountWitness) Eval(c *EvalContext) (sqltype.Value, error) {
	*w.calls++
	return w.value, w.err
}

func TestNullCountTypedEvaluation(t *testing.T) {
	e := New()
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	var args []BoundExpr
	for _, tc := range []struct {
		id    sqltype.ID
		input string
	}{
		{sqltype.Int2, "0"}, {sqltype.Int4, "0"}, {sqltype.Int8, "2147483648"}, {sqltype.Text, ""}, {sqltype.Bool, "false"},
		{sqltype.Numeric, "1.5"}, {sqltype.Float4, "1.5"}, {sqltype.Float8, "2.5"}, {sqltype.Time, "12:34:56"},
		{sqltype.Timestamp, "2000-01-02 03:04:05"}, {sqltype.UUID, "00000000-0000-0000-0000-000000000001"},
	} {
		typ := b.typ(tc.id)
		value, err := typ.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(tc, err)
		}
		args = append(args, literalExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, value: value}, literalExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, value: sqltype.Null(typ)})
	}
	for _, kind := range []nullCountKind{countNulls, countNonnulls} {
		call := nullCountExpression{expressionInfo: expressionInfo{usage: b.typ(sqltype.Int4).Usage()}, kind: kind, args: args}
		for range 2 {
			v, err := call.Eval(&EvalContext{Context: ctx})
			n, _ := v.Integer()
			if err != nil || v.IsNull() || v.Type() != b.typ(sqltype.Int4) || n != 11 {
				t.Fatal(v, err)
			}
		}
		evaluation := &EvalContext{Context: ctx}
		if allocations := testing.AllocsPerRun(10, func() {
			_, err := call.Eval(evaluation)
			if err != nil {
				panic(err)
			}
		}); allocations != 0 {
			t.Fatal("per-Eval allocation", allocations)
		}
	}
	calls := 0
	sentinel := errors.New("child failure")
	typ := b.typ(sqltype.Text)
	first := nullCountWitness{expressionInfo: expressionInfo{usage: typ.Usage()}, value: sqltype.Null(typ), calls: &calls}
	last := nullCountWitness{expressionInfo: expressionInfo{usage: typ.Usage()}, value: sqltype.String(typ, ""), calls: &calls}
	call := nullCountExpression{expressionInfo: expressionInfo{usage: b.typ(sqltype.Int4).Usage()}, kind: countNulls, args: []BoundExpr{first, last}}
	v, err := call.Eval(&EvalContext{Context: ctx})
	n, _ := v.Integer()
	if err != nil || n != 1 || calls != 2 {
		t.Fatal(v, err, calls)
	}
	last.err = sentinel
	call.args[1] = last
	calls = 0
	if _, err := call.Eval(&EvalContext{Context: ctx}); !errors.Is(err, sentinel) || calls != 2 {
		t.Fatal(err, calls)
	}
	first.err = sentinel
	call.args[0] = first
	calls = 0
	if _, err := call.Eval(&EvalContext{Context: ctx}); !errors.Is(err, sentinel) || calls != 1 {
		t.Fatal("first error", err, calls)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	calls = 0
	if _, err := call.Eval(&EvalContext{Context: canceled}); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal(err, calls)
	}
	first.err = nil
	first.value = sqltype.Value{}
	call.args = []BoundExpr{first}
	if v, err := call.Eval(&EvalContext{Context: ctx}); err == nil || v.Type() != nil {
		t.Fatal("zero value counted", v, err)
	}
	first.value = sqltype.Null(b.typ(sqltype.Int4))
	call.args[0] = first
	if v, err := call.Eval(&EvalContext{Context: ctx}); err == nil || v.Type() != nil {
		t.Fatal("type mismatch counted", v, err)
	}
	if _, err := (nullCountExpression{}).Eval(&EvalContext{Context: ctx}); err == nil {
		t.Fatal("zero call accepted")
	}
}

func TestNullCountDependenciesAndAuthorization(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER reader")
	roleExecute(t, e, op, "CREATE TABLE secret(id INT,v INT,f BOOL)")
	roleExecute(t, e, op, "INSERT INTO secret VALUES(1,NULL,TRUE)")
	ref := roleTableRef(t, e, "secret")
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	ast := statement(t, "SELECT num_nulls(num_nonnulls(v),f),num_nonnulls(v) FROM secret").(*parser.Select)
	bound, err := e.describe(op.ctx, ast, auth, ref, sqltype.InputEnvironment{}, op, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(bound.querySources) != 1 || bound.querySources[0].ref != ref || !reflect.DeepEqual(bound.querySources[0].uses, []int{1, 2}) {
		t.Fatal(bound.querySources)
	}
	project := bound.query.Root.(*projectNode)
	for _, expression := range project.expressions {
		if p := expression.Properties(); p.Volatility != Immutable || !p.RowDependent || p.ParameterDependent || p.StageDependent {
			t.Fatal(p)
		}
	}
	for _, column := range project.Schema() {
		if column.OriginTable != 0 || column.OriginAttribute != 0 || column.NullabilityKnown || column.Name == "?column?" || column.Usage.Type.ID() != sqltype.Int4 || column.Usage.Typmod != -1 || column.Usage.Collation != (sqltype.CollationRef{}) {
			t.Fatal(column)
		}
	}
	refs := map[ObjectRef]bool{}
	for _, dep := range bound.dependencies {
		refs[dep.ref] = true
	}
	for _, sub := range []int{2, 3} {
		column := ref
		column.Address.SubID = int32(sub)
		if !refs[column] {
			t.Fatal("missing argument dependency", strconv.Itoa(sub), bound.dependencies)
		}
	}
	reader, _ := e.Roles().Lookup("reader")
	column := ref
	column.Address.SubID = 2
	if err := e.SetObjectACL(context.Background(), column, ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: e.roles.names["postgres"], Grantee: ACLGrantee{Role: reader.Ref}, Privileges: SelectPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	result := roleExecute(t, e, op, "SELECT num_nulls(num_nonnulls(v)) FROM secret")
	if result.Count != 1 {
		t.Fatal(result.Count)
	}
	result.Rows.Close()
	roleError(t, e, op, "SELECT num_nulls(num_nonnulls(v),f) FROM secret", "42501", "permission denied for table secret", "")
	result = roleExecute(t, e, op, "SELECT num_nulls(NULL) FROM secret")
	if got := tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}}) {
		t.Fatal(got)
	}
	result.Rows.Close()
	result = roleExecute(t, e, op, "SELECT num_nulls(NULL)")
	if result.Count != 1 {
		t.Fatal(result.Count)
	}
	result.Rows.Close()
}
