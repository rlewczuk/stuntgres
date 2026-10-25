package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strconv"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
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
		_, err := b.Bind(nullCountRaw(name), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42883", Message: "function " + name + "() does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."})
		args := make([]parser.Expr, 100)
		for i := range args {
			args[i] = parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
		}
		bound, err := b.Bind(nullCountRaw(name, args...), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		v, err := bound.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
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
		_, err = b.Bind(nullCountRaw(name, args...), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42P02", Message: "there is no parameter $0", Position: "18"})
		args[0] = parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
		_, err = b.Bind(nullCountRaw(name, args...), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"})
	}
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	arg := parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue, Pos: parser.Position{Character: 27}}}
	raw := nullCountRaw("num_nulls", arg)
	raw.Variadic = true
	_, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42804", Message: "VARIADIC argument must be an array", Position: "27"})
	raw.Args = append(raw.Args, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}})
	_, err = b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42883", Message: "function num_nulls(unknown, integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."})
	_, err = b.Bind(nullCountRaw("num_nulls", parser.ParameterExpr{Number: 0, Pos: parser.Position{Character: 18}}), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42P02", Message: "there is no parameter $0", Position: "18"})
}

func TestNullCountAnyOccurrences(t *testing.T) {
	e := New()
	ctx := context.Background()
	for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Text, sqltype.Bool} {
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		parameter := parser.ParameterExpr{Number: 1, Pos: parser.Position{Character: 18}}
		bound, err := b.Bind(nullCountRaw("num_nulls", parameter), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if bound.ResultUsage() != b.Type(sqltype.Int4).Usage() || !bound.Properties().ParameterDependent {
			t.Fatal(bound)
		}
		if _, err := b.Finish(); err == nil {
			t.Fatal("unresolved ANY escaped analysis")
		} else {
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42P18", Message: "could not determine data type of parameter $1"})
		}
		if err := b.Constrain(1, b.Type(id).Usage(), parameter.Pos); err != nil {
			t.Fatal(err)
		}
		_, err = b.Finish()
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "18"})
		// A concrete type known before call transformation is usable unchanged.
		known := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		if err := known.Constrain(1, known.Type(id).Usage(), parameter.Pos); err != nil {
			t.Fatal(err)
		}
		call, err := known.Bind(nullCountRaw("num_nonnulls", parameter), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if info, err := known.Finish(); err != nil || info.Count != 1 || info.Types[0] != known.Type(id).Usage() {
			t.Fatal(info, err)
		}
		v, err := call.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Parameters: []sqltype.Value{sqltype.Null(known.Type(id))}}})
		n, _ := v.Integer()
		if err != nil || n != 0 {
			t.Fatal(v, err)
		}
	}
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	_, err := b.Bind(nullCountRaw("num_nulls", parser.ParameterExpr{Number: 2147483647}), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Params.Parameters) != 1 || len(b.Params.AnyParameters) != 1 {
		t.Fatal("sparse analysis expanded parameter index")
	}
	_, err = b.Finish()
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42P18", Message: "could not determine data type of parameter $1"})
}

func TestNullCountDependenciesAndAuthorization(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER reader")
	roleExecute(t, e, op, "CREATE TABLE secret(id INT,v INT,f BOOL)")
	roleExecute(t, e, op, "INSERT INTO secret VALUES(1,NULL,TRUE)")
	ref := roleTableRef(t, e, "secret")
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	ast := statement(t, "SELECT num_nulls(num_nonnulls(v),f),num_nonnulls(v) FROM secret").(*parser.Select)
	bound, err := Describe(op.Ctx, e.AnalysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(bound.QuerySources) != 1 || bound.QuerySources[0].Ref != ref || !reflect.DeepEqual(bound.QuerySources[0].Uses, []int{1, 2}) {
		t.Fatal(bound.QuerySources)
	}
	project := bound.Query.Root.(*plan.ProjectNode)
	for _, expression := range project.Expressions {
		if p := expression.Properties(); p.Volatility != expr.Immutable || !p.RowDependent || p.ParameterDependent || p.StageDependent {
			t.Fatal(p)
		}
	}
	for _, column := range project.Schema() {
		if column.OriginTable != 0 || column.OriginAttribute != 0 || column.NullabilityKnown || column.Name == "?column?" || column.Usage.Type.ID() != sqltype.Int4 || column.Usage.Typmod != -1 || column.Usage.Collation != (sqltype.CollationRef{}) {
			t.Fatal(column)
		}
	}
	refs := map[ids.ObjectRef]bool{}
	for _, dep := range bound.Dependencies {
		refs[dep.Ref] = true
	}
	for _, sub := range []int{2, 3} {
		column := ref
		column.Address.SubID = int32(sub)
		if !refs[column] {
			t.Fatal("missing argument dependency", strconv.Itoa(sub), bound.Dependencies)
		}
	}
	reader, _ := e.Roles().Lookup("reader")
	column := ref
	column.Address.SubID = 2
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
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
