package enginetests_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestExpressionCompareNullTypedEval(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	boolType := b.Type(sqltype.Bool)
	intType := b.Type(sqltype.Int4)
	evalContext := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	boolInfo := b.Info(boolType.Usage(), parser.Position{Character: 8}, expr.Properties{})
	integer, _ := sqltype.Integer(intType, 1)
	for _, v := range []sqltype.Value{sqltype.Null(intType), integer, sqltype.Null(boolType), sqltype.Boolean(boolType, true)} {
		child := plan.LiteralExpr{Info: b.Info(v.Type().Usage(), parser.Position{}, expr.Properties{}), Value: v}
		for _, not := range []bool{false, true} {
			node := plan.NullTestExpr{Info: boolInfo, Child: child, Not: not}
			got, err := node.Eval(evalContext)
			want := v.IsNull() != not
			truth, truthErr := plan.TruthOf(got)
			if err != nil || truthErr != nil || got.Type() != boolType || got.IsNull() || (truth == plan.True) != want {
				t.Fatal(got, want, err, truthErr)
			}
		}
	}
	matrix := [][]bool{{true, false, false, true, false, true}, {false, true, true, false, false, true}, {false, true, false, true, true, false}}
	for i, v := range []sqltype.Value{sqltype.Boolean(boolType, true), sqltype.Boolean(boolType, false), sqltype.Null(boolType)} {
		child := plan.LiteralExpr{Info: boolInfo, Value: v}
		for j, truth := range []plan.Truth{plan.True, plan.True, plan.False, plan.False, plan.Unknown, plan.Unknown} {
			node := plan.TruthTestExpr{Info: boolInfo, Child: child, Truth: truth, Not: j%2 == 1}
			got, err := node.Eval(evalContext)
			actual, truthErr := plan.TruthOf(got)
			if err != nil || truthErr != nil || got.IsNull() || (actual == plan.True) != matrix[i][j] {
				t.Fatal(i, j, got, err, truthErr)
			}
		}
	}
	two, _ := sqltype.Integer(intType, 2)
	values := []sqltype.Value{sqltype.Null(intType), integer, two}
	equality, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, intType, intType, boolType, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range values {
		for j, c := range values {
			left := plan.LiteralExpr{Info: b.Info(intType.Usage(), parser.Position{}, expr.Properties{}), Value: a}
			right := plan.LiteralExpr{Info: b.Info(intType.Usage(), parser.Position{}, expr.Properties{}), Value: c}
			for _, not := range []bool{false, true} {
				node := plan.DistinctExpr{Info: boolInfo, Left: left, Right: right, Equality: equality, Not: not}
				got, err := node.Eval(evalContext)
				truth, truthErr := plan.TruthOf(got)
				if err != nil || truthErr != nil || got.IsNull() || (truth == plan.True) != ((i != j) != not) {
					t.Fatal(i, j, not, got, err, truthErr)
				}
			}
		}
	}
	strict, err := equality.Evaluate(evalContext.Context, sqltype.Null(intType), integer, sqltype.InputEnvironment{})
	if err != nil || strict.Type() != boolType || !strict.IsNull() {
		t.Fatal("ordinary equality lost UNKNOWN", strict, err)
	}
	column := plan.ColumnExpr{Info: b.Info(intType.Usage(), parser.Position{}, expr.Properties{RowDependent: true}), Scope: 1, Frame: 1, Slot: 0}
	parameter := plan.ParameterExpr{Info: b.Info(intType.Usage(), parser.Position{}, expr.Properties{ParameterDependent: true}), Number: 1}
	nodes := []expr.Expr{plan.NullTestExpr{Info: boolInfo, Child: column}, plan.TruthTestExpr{Info: boolInfo, Child: column, Truth: plan.True}, plan.DistinctExpr{Info: boolInfo, Left: column, Right: parameter, Equality: equality}}
	invalid := []*exec.Context{nil, {EvalStatement: exec.EvalStatement{Context: evalContext.Context}, EvalFrame: exec.EvalFrame{Scope: 2, Frame: 1, Row: []sqltype.Value{sqltype.Null(intType)}}}, {EvalStatement: exec.EvalStatement{Context: evalContext.Context, Parameters: []sqltype.Value{integer}}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{{}}}}, {EvalStatement: exec.EvalStatement{Context: evalContext.Context, Parameters: []sqltype.Value{integer}}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.Null(boolType)}}}}
	for _, node := range nodes {
		for _, frame := range invalid {
			if value, err := node.Eval(frame); err == nil || value.Type() != nil {
				t.Fatal("invalid frame became SQL NULL", value, err)
			}
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, node := range nodes {
		if _, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: canceled}}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	// A bad right frame cannot be hidden by the left NULL fast path.
	if _, err := nodes[2].Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: evalContext.Context, Parameters: []sqltype.Value{{}}}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.Null(intType)}}}); err == nil {
		t.Fatal("NULL fast path hid invalid right operand")
	}
}

func TestExpressionCompareNullOccurrenceTyping(t *testing.T) {
	e := engine.New()
	for _, tc := range []struct {
		q                               string
		ids                             []sqltype.ID
		code, message, detail, position string
	}{
		{q: "SELECT $1 IS TRUE,$1 IS NULL FROM t", ids: []sqltype.ID{sqltype.Bool}},
		{q: "SELECT $1 IS DISTINCT FROM v FROM t", ids: []sqltype.ID{sqltype.Int4}},
		{q: "SELECT v IS DISTINCT FROM $1 FROM t", ids: []sqltype.ID{sqltype.Int4}},
		{q: "SELECT $1 IS DISTINCT FROM $2 FROM t", ids: []sqltype.ID{sqltype.Text, sqltype.Text}},
		{q: "SELECT $1 IS DISTINCT FROM $1 FROM t", ids: []sqltype.ID{sqltype.Text}},
		{q: "SELECT $1 IS NULL FROM t", code: "42P18", message: "could not determine data type of parameter $1"},
		{q: "SELECT $1 IS DISTINCT FROM NULL FROM t", code: "42P18", message: "could not determine data type of parameter $1"},
		{q: "SELECT $1 IS NULL,$1 IS TRUE FROM t", code: "42P08", message: "could not determine data type of parameter $1", position: "8"},
		{q: "SELECT num_nulls($1),$1 IS NULL,$1 IS TRUE FROM t", code: "42P08", message: "could not determine data type of parameter $1", position: "18"},
		{q: "SELECT $1 IS NULL,num_nulls($1),$1 IS TRUE FROM t", code: "42P08", message: "could not determine data type of parameter $1", position: "8"},
		{q: "SELECT num_nulls($2),$1 IS NULL,$1 IS TRUE,$2 IS TRUE FROM t", code: "42P08", message: "could not determine data type of parameter $2", position: "18"},
		{q: "SELECT $1 IS NULL,num_nulls($2),$1 IS TRUE,$2 IS TRUE FROM t", code: "42P08", message: "could not determine data type of parameter $1", position: "8"},
		{q: "SELECT num_nulls($2),$1 IS NULL,$1 IS TRUE FROM t", code: "42P08", message: "could not determine data type of parameter $1", position: "22"},
		{q: "SELECT $1 IS TRUE,num_nulls($1),$1 IS NULL FROM t", ids: []sqltype.ID{sqltype.Bool}},
		{q: "SELECT $1 IS NULL,$1 IS TRUE,$2 IS NULL FROM t", code: "42P08", message: "could not determine data type of parameter $1", position: "8"},
		{q: "SELECT $2 IS NULL,$2 IS TRUE FROM t", code: "42P08", message: "could not determine data type of parameter $2", position: "8"},
		{q: "SELECT $1 IS DISTINCT FROM NULL,$1 IS TRUE,$2 IS NULL FROM t", code: "42P08", message: "could not determine data type of parameter $1", position: "8"},
		{q: "SELECT $1 IS TRUE,$2 IS NULL FROM t", code: "42P18", message: "could not determine data type of parameter $2"},
		{q: "SELECT $1 IS NULL,$1 FROM t", code: "42P08", message: "could not determine data type of parameter $1", position: "8"},
		{q: "SELECT $1,$1 IS NULL FROM t", code: "42P08", message: "could not determine data type of parameter $1", position: "11"},
		{q: "SELECT $1 FROM t WHERE v IS DISTINCT FROM $1", code: "42P08", message: "inconsistent types deduced for parameter $1", detail: "integer versus text", position: "8"},
		{q: "SELECT $1 IS DISTINCT FROM v,$1 IS TRUE FROM t", code: "42804", message: "argument of IS TRUE must be type boolean, not type integer", position: "30"},
	} {
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		b.Target.Relation = &catalog.Relation{Columns: []catalog.Column{{Name: "v", Usage: b.Type(sqltype.Int4).Usage()}}, Index: map[string]int{"v": 0}, Attributes: []int32{1}}
		bindTestTargetNamespace(b)
		s := enginetest.Statement(t, tc.q).(*parser.Select)
		targets := []expr.Expr{}
		var err error
		for _, raw := range s.Targets {
			var node expr.Expr
			node, err = b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				break
			}
			targets = append(targets, node)
		}
		if err == nil && s.Where != nil {
			_, err = b.Bind(s.Where, analyze.ExpressionPolicy{Context: analyze.FilterContext, Expected: b.Type(sqltype.Bool).Usage()})
		}
		if err == nil {
			for _, node := range targets {
				if _, err = b.FinalizeProjection(node); err != nil {
					break
				}
			}
		}
		var info analyze.ParameterInfo
		if err == nil {
			info, err = b.Finish()
		}
		if tc.code != "" {
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail, Position: tc.position}
			var got *sqlstate.Error
			if !errors.As(err, &got) || *got != want {
				t.Fatal(tc.q, err, got, want)
			}
			continue
		}
		if err != nil || info.Count != len(tc.ids) {
			t.Fatal(tc.q, info, err)
		}
		for i, id := range tc.ids {
			if info.Types[i].Type.ID() != id || info.Types[i].Typmod != -1 {
				t.Fatal(info)
			}
		}
	}
}

func TestExpressionCompareNullDependenciesAndAuthorization(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER reader")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE secret(id INT,v INT,f BOOL)")
	enginetest.RoleExecute(t, e, op, "INSERT INTO secret VALUES(1,1,TRUE)")
	ref := enginetest.RoleTableRef(t, e, "secret")
	reader, _ := e.Roles().Lookup("reader")
	column := ref
	column.Address.SubID = 2
	if err := enginetest.SetObjectACL(e, context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	for _, q := range []string{"SELECT v IS NULL FROM secret", "SELECT 1 IS DISTINCT FROM v FROM secret", "SELECT v IS DISTINCT FROM v FROM secret", "SELECT 7 FROM secret WHERE v IS NOT NULL"} {
		r := enginetest.RoleExecute(t, e, op, q)
		if r.Count != 1 {
			t.Fatal(q, r.Count)
		}
		r.Rows.Close()
	}
	for _, q := range []string{"SELECT f IS TRUE FROM secret", "SELECT v IS DISTINCT FROM id FROM secret", "SELECT id IS DISTINCT FROM v FROM secret"} {
		enginetest.RoleError(t, e, op, q, "42501", "permission denied for table secret", "")
	}
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	ast := enginetest.Statement(t, "SELECT v IS DISTINCT FROM id FROM secret")
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	b, err := core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	node := b.Query.Root.(*plan.ProjectNode).Expressions[0]
	if !node.Properties().RowDependent || node.Properties().ParameterDependent || node.ResultUsage().Type.ID() != sqltype.Bool || node.ResultUsage().Typmod != -1 {
		t.Fatal(node)
	}
	schema := b.Query.Root.Schema()
	if len(schema) != 1 || schema[0].Name != "?column?" || schema[0].OriginTable != 0 || schema[0].OriginAttribute != 0 || schema[0].NullabilityKnown || schema[0].Usage.Type.ID() != 16 || schema[0].Usage.Typmod != -1 {
		t.Fatal("computed BOOL descriptor", schema)
	}
	seen := map[int32]bool{}
	for _, d := range b.Dependencies {
		if d.Ref.Address.ClassID == ids.RelationClassID && d.Ref.Address.ObjectID == ref.Address.ObjectID {
			seen[d.Ref.Address.SubID] = true
		}
	}
	if !seen[1] || !seen[2] {
		t.Fatal("both operands must retain column dependencies", b.Dependencies)
	}
	r := enginetest.RoleExecute(t, e, op, "SELECT v IS DISTINCT FROM id FROM secret")
	if !reflect.DeepEqual(enginetest.Tuples(r), [][]driver.Value{{false}}) {
		t.Fatal(r)
	}
	r.Rows.Close()
}

func TestExpressionCompareNullConstantAndParameterReuse(t *testing.T) {
	e := engine.New()
	ctx := context.Background()
	for _, q := range []string{"SELECT NULL IS NULL FROM t", "SELECT TRUE IS NOT FALSE FROM t", "SELECT 1 IS DISTINCT FROM 2 FROM t"} {
		b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		node, err := b.Bind(enginetest.Statement(t, q).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if node.Properties() != (expr.Properties{Volatility: expr.Immutable}) {
			t.Fatal("constant dependency", node.Properties())
		}
	}
	b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	raw := enginetest.Statement(t, "SELECT $1 IS TRUE FROM t").(*parser.Select).Targets[0]
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	if node.Properties().RowDependent || !node.Properties().ParameterDependent || node.Properties().Volatility != expr.Immutable {
		t.Fatal(node.Properties())
	}
	boolType := b.Type(sqltype.Bool)
	for i, value := range []sqltype.Value{sqltype.Boolean(boolType, true), sqltype.Boolean(boolType, false), sqltype.Null(boolType), sqltype.Boolean(boolType, true)} {
		got, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Parameters: []sqltype.Value{value}}})
		truth, truthErr := plan.TruthOf(got)
		if err != nil || truthErr != nil || got.IsNull() || (truth == plan.True) != (i == 0 || i == 3) {
			t.Fatal("immutable node leaked frame", got, err, truthErr)
		}
	}
}

func TestExpressionCompareNullBoundComposition(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(id INT,v INT,f BOOL)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(1,1,TRUE),(2,NULL,NULL),(3,2,FALSE),(4,1,TRUE)")
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	describe := func(q string) (analyze.Binding, ids.ObjectRef) {
		t.Helper()
		ast := enginetest.Statement(t, q)
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		ref, err := e.ResolveReference(auth, ast.(*parser.Select).From.(*parser.TableReference).Name)
		if err != nil {
			t.Fatal(err)
		}
		b, err := core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
		if err != nil {
			t.Fatal(q, err)
		}
		return b, ref
	}
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	intValue := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(integer, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	rows := [][]sqltype.Value{
		{intValue(1), intValue(1), sqltype.Boolean(boolean, true)},
		{intValue(2), sqltype.Null(integer), sqltype.Null(boolean)},
		{intValue(3), intValue(2), sqltype.Boolean(boolean, false)},
		{intValue(4), intValue(1), sqltype.Boolean(boolean, true)},
	}
	for _, tc := range []struct {
		expr    string
		want    [4]bool
		columns []int32
	}{
		{"(v=1) IS TRUE", [4]bool{true, false, false, true}, []int32{2}},
		{"(v=NULL) IS UNKNOWN", [4]bool{true, true, true, true}, []int32{2}},
		{"(v=v) IS TRUE", [4]bool{true, false, true, true}, []int32{2}},
		{"v IS NULL AND TRUE", [4]bool{false, true, false, false}, []int32{2}},
		{"NOT v IS NULL", [4]bool{true, false, true, true}, []int32{2}},
		{"v IS NULL OR f IS TRUE", [4]bool{true, true, false, true}, []int32{2, 3}},
		{"(f AND TRUE) IS UNKNOWN", [4]bool{false, true, false, false}, []int32{3}},
		{"(f OR FALSE) IS NOT TRUE", [4]bool{false, true, true, false}, []int32{3}},
		{"(NOT f) IS DISTINCT FROM FALSE", [4]bool{false, true, true, false}, []int32{3}},
		{"v<2 IS TRUE OR NOT f IS UNKNOWN AND v IS NOT NULL", [4]bool{true, false, true, true}, []int32{2, 3}},
	} {
		b, ref := describe("SELECT " + tc.expr + " FROM t WHERE " + tc.expr)
		project := b.Query.Root.(*plan.ProjectNode)
		filter := project.Child.(*plan.FilterNode)
		if b.Parameters.Count != 0 {
			t.Fatal(b.Parameters)
		}
		for _, node := range []expr.Expr{project.Expressions[0], filter.Predicate} {
			p := node.Properties()
			if !p.RowDependent || p.ParameterDependent || p.StageDependent || p.Volatility != expr.Immutable || node.ResultUsage() != boolean.Usage() {
				t.Fatal(tc.expr, p, node.ResultUsage())
			}
			for i, row := range rows {
				got, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: op.Ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: row}})
				value, valueErr := got.Boolean()
				if err != nil || valueErr != nil || got.IsNull() || value != tc.want[i] {
					t.Fatal(tc.expr, i, got, err, valueErr)
				}
			}
		}
		seen := map[int32]bool{}
		for _, d := range b.Dependencies {
			if d.Ref.Domain() != e.Catalog.Domain || d.Revision == 0 {
				t.Fatal(d)
			}
			if d.Ref.Address.ClassID == ids.RelationClassID && d.Ref.Address.ObjectID == ref.Address.ObjectID {
				seen[d.Ref.Address.SubID] = true
			}
		}
		for _, column := range tc.columns {
			if !seen[column] {
				t.Fatal("composed child dependency missing", tc.expr, column, b.Dependencies)
			}
		}
	}
	b, ref := describe("SELECT id FROM t WHERE v IS NOT DISTINCT FROM $1 AND (v=$1) IS NOT FALSE ORDER BY id")
	if b.Parameters.Count != 1 || !reflect.DeepEqual(b.Parameters.Types, []sqltype.Usage{integer.Usage()}) {
		t.Fatal("held mixed comparison must infer INT4", b.Parameters)
	}
	filter := b.Query.Root.(*plan.ProjectNode).Child.(*plan.SortNode).Child.(*plan.FilterNode)
	p := filter.Predicate.Properties()
	if !p.RowDependent || !p.ParameterDependent || p.StageDependent || p.Volatility != expr.Immutable || filter.Predicate.ResultUsage() != boolean.Usage() {
		t.Fatal(p, filter.Predicate.ResultUsage())
	}
	column := ref
	column.Address.SubID = 2
	found := false
	for _, d := range b.Dependencies {
		found = found || d.Ref == column
	}
	if !found {
		t.Fatal("held predicate must depend on v", b.Dependencies)
	}
	for n, parameter := range []sqltype.Value{intValue(1), intValue(2), sqltype.Null(integer), intValue(1)} {
		want := [][4]bool{{true, false, false, true}, {false, false, true, false}, {false, true, false, false}, {true, false, false, true}}[n]
		for i, row := range rows {
			got, err := filter.Predicate.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: op.Ctx, Parameters: []sqltype.Value{parameter}}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: row}})
			value, valueErr := got.Boolean()
			if err != nil || valueErr != nil || got.IsNull() || value != want[i] {
				t.Fatal(n, i, got, err, valueErr)
			}
		}
	}
	if !reflect.DeepEqual(b.Parameters.Types, []sqltype.Usage{integer.Usage()}) {
		t.Fatal("held frames changed inferred usage", b.Parameters)
	}
}
