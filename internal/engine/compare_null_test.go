package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestExpressionCompareNullTypedEval(t *testing.T) {
	e := New()
	b := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
	boolType := b.typ(sqltype.Bool)
	intType := b.typ(sqltype.Int4)
	evalContext := &EvalContext{Context: context.Background(), Scope: 1, Frame: 1}
	boolInfo := b.info(boolType.Usage(), parser.Position{Character: 8}, ExprProperties{})
	integer, _ := sqltype.Integer(intType, 1)
	for _, v := range []sqltype.Value{sqltype.Null(intType), integer, sqltype.Null(boolType), sqltype.Boolean(boolType, true)} {
		child := literalExpression{b.info(v.Type().Usage(), parser.Position{}, ExprProperties{}), v}
		for _, not := range []bool{false, true} {
			node := nullTestExpression{boolInfo, child, not}
			got, err := node.Eval(evalContext)
			want := v.IsNull() != not
			truth, truthErr := TruthOf(got)
			if err != nil || truthErr != nil || got.Type() != boolType || got.IsNull() || (truth == True) != want {
				t.Fatal(got, want, err, truthErr)
			}
		}
	}
	matrix := [][]bool{{true, false, false, true, false, true}, {false, true, true, false, false, true}, {false, true, false, true, true, false}}
	for i, v := range []sqltype.Value{sqltype.Boolean(boolType, true), sqltype.Boolean(boolType, false), sqltype.Null(boolType)} {
		child := literalExpression{boolInfo, v}
		for j, truth := range []Truth{True, True, False, False, Unknown, Unknown} {
			node := truthTestExpression{boolInfo, child, truth, j%2 == 1}
			got, err := node.Eval(evalContext)
			actual, truthErr := TruthOf(got)
			if err != nil || truthErr != nil || got.IsNull() || (actual == True) != matrix[i][j] {
				t.Fatal(i, j, got, err, truthErr)
			}
		}
	}
	two, _ := sqltype.Integer(intType, 2)
	values := []sqltype.Value{sqltype.Null(intType), integer, two}
	equality, err := sqltype.BindEquality(intType, intType, boolType)
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range values {
		for j, c := range values {
			left := literalExpression{b.info(intType.Usage(), parser.Position{}, ExprProperties{}), a}
			right := literalExpression{b.info(intType.Usage(), parser.Position{}, ExprProperties{}), c}
			for _, not := range []bool{false, true} {
				node := distinctExpression{boolInfo, left, right, equality, not}
				got, err := node.Eval(evalContext)
				truth, truthErr := TruthOf(got)
				if err != nil || truthErr != nil || got.IsNull() || (truth == True) != ((i != j) != not) {
					t.Fatal(i, j, not, got, err, truthErr)
				}
			}
		}
	}
	strict, err := equality.Evaluate(evalContext.Context, sqltype.Null(intType), integer)
	if err != nil || strict.Type() != boolType || !strict.IsNull() {
		t.Fatal("ordinary equality lost UNKNOWN", strict, err)
	}
	column := columnExpression{expressionInfo: b.info(intType.Usage(), parser.Position{}, ExprProperties{RowDependent: true}), scope: 1, frame: 1, slot: 0}
	parameter := parameterExpression{b.info(intType.Usage(), parser.Position{}, ExprProperties{ParameterDependent: true}), 1}
	nodes := []BoundExpr{nullTestExpression{boolInfo, column, false}, truthTestExpression{boolInfo, column, True, false}, distinctExpression{boolInfo, column, parameter, equality, false}}
	invalid := []*EvalContext{nil, {Context: evalContext.Context, Scope: 2, Frame: 1, Row: []sqltype.Value{sqltype.Null(intType)}}, {Context: evalContext.Context, Scope: 1, Frame: 1, Row: []sqltype.Value{{}}, Parameters: []sqltype.Value{integer}}, {Context: evalContext.Context, Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.Null(boolType)}, Parameters: []sqltype.Value{integer}}}
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
		if _, err := node.Eval(&EvalContext{Context: canceled}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	// A bad right frame cannot be hidden by the left NULL fast path.
	if _, err := nodes[2].Eval(&EvalContext{Context: evalContext.Context, Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.Null(intType)}, Parameters: []sqltype.Value{{}}}); err == nil {
		t.Fatal("NULL fast path hid invalid right operand")
	}
}

func TestExpressionCompareNullOccurrenceTyping(t *testing.T) {
	e := New()
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
		b := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
		b.relation = &relationDescriptor{columns: []ColumnDescriptor{{Name: "v", Usage: b.typ(sqltype.Int4).Usage()}}, index: map[string]int{"v": 0}, attributes: []int32{1}}
		s := statement(t, tc.q).(*parser.Select)
		targets := []BoundExpr{}
		var err error
		for _, raw := range s.Targets {
			var node BoundExpr
			node, err = b.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				break
			}
			targets = append(targets, node)
		}
		if err == nil && s.Where != nil {
			_, err = b.bind(s.Where, expressionPolicy{context: filterContext, expected: b.typ(sqltype.Bool).Usage()})
		}
		if err == nil {
			for _, node := range targets {
				if _, err = b.finalizeProjection(node); err != nil {
					break
				}
			}
		}
		var info ParameterInfo
		if err == nil {
			info, err = b.finish()
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
	if err := e.SetObjectACL(context.Background(), column, ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: e.roles.names["postgres"], Grantee: ACLGrantee{Role: reader.Ref}, Privileges: SelectPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	for _, q := range []string{"SELECT v IS NULL FROM secret", "SELECT 1 IS DISTINCT FROM v FROM secret", "SELECT v IS DISTINCT FROM v FROM secret", "SELECT 7 FROM secret WHERE v IS NOT NULL"} {
		r := roleExecute(t, e, op, q)
		if r.Count != 1 {
			t.Fatal(q, r.Count)
		}
		r.Rows.Close()
	}
	for _, q := range []string{"SELECT f IS TRUE FROM secret", "SELECT v IS DISTINCT FROM id FROM secret", "SELECT id IS DISTINCT FROM v FROM secret"} {
		roleError(t, e, op, q, "42501", "permission denied for table secret", "")
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	ast := statement(t, "SELECT v IS DISTINCT FROM id FROM secret")
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	b, err := e.describe(op.ctx, ast, auth, ref, sqltype.InputEnvironment{}, op, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	node := b.query.Root.(*projectNode).expressions[0]
	if !node.Properties().RowDependent || node.Properties().ParameterDependent || node.ResultUsage().Type.ID() != sqltype.Bool || node.ResultUsage().Typmod != -1 {
		t.Fatal(node)
	}
	schema := b.query.Root.Schema()
	if len(schema) != 1 || schema[0].Name != "?column?" || schema[0].OriginTable != 0 || schema[0].OriginAttribute != 0 || schema[0].NullabilityKnown || schema[0].Usage.Type.ID() != 16 || schema[0].Usage.Typmod != -1 {
		t.Fatal("computed BOOL descriptor", schema)
	}
	seen := map[int32]bool{}
	for _, d := range b.dependencies {
		if d.ref.Address.ClassID == RelationClassID && d.ref.Address.ObjectID == ref.Address.ObjectID {
			seen[d.ref.Address.SubID] = true
		}
	}
	if !seen[1] || !seen[2] {
		t.Fatal("both operands must retain column dependencies", b.dependencies)
	}
	r := roleExecute(t, e, op, "SELECT v IS DISTINCT FROM id FROM secret")
	if !reflect.DeepEqual(tuples(r), [][]driver.Value{{false}}) {
		t.Fatal(r)
	}
	r.Rows.Close()
}

func TestExpressionCompareNullConstantAndParameterReuse(t *testing.T) {
	e := New()
	ctx := context.Background()
	for _, q := range []string{"SELECT NULL IS NULL FROM t", "SELECT TRUE IS NOT FALSE FROM t", "SELECT 1 IS DISTINCT FROM 2 FROM t"} {
		b := newExpressionBinder(ctx, e, sqltype.InputEnvironment{}, nil, nil)
		node, err := b.bind(statement(t, q).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if node.Properties() != (ExprProperties{Volatility: Immutable}) {
			t.Fatal("constant dependency", node.Properties())
		}
	}
	b := newExpressionBinder(ctx, e, sqltype.InputEnvironment{}, nil, nil)
	raw := statement(t, "SELECT $1 IS TRUE FROM t").(*parser.Select).Targets[0]
	node, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	if node.Properties().RowDependent || !node.Properties().ParameterDependent || node.Properties().Volatility != Immutable {
		t.Fatal(node.Properties())
	}
	boolType := b.typ(sqltype.Bool)
	for i, value := range []sqltype.Value{sqltype.Boolean(boolType, true), sqltype.Boolean(boolType, false), sqltype.Null(boolType), sqltype.Boolean(boolType, true)} {
		got, err := node.Eval(&EvalContext{Context: ctx, Parameters: []sqltype.Value{value}})
		truth, truthErr := TruthOf(got)
		if err != nil || truthErr != nil || got.IsNull() || (truth == True) != (i == 0 || i == 3) {
			t.Fatal("immutable node leaked frame", got, err, truthErr)
		}
	}
}
