package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQueryFromValuesCommonSchema(t *testing.T) {
	for _, tc := range []struct { query string; id sqltype.ID; typmod int32 }{
		{"VALUES(NULL),(NULL),(1)", sqltype.Int4, -1},
		{"VALUES(NULL),(NULL)", sqltype.Text, -1},
		{"VALUES('1'),(2)", sqltype.Int4, -1},
		{"VALUES('a'::char(2)),('b'::char(2))", sqltype.BPChar, 6},
		{"VALUES(NULL),('a'::char(2))", sqltype.BPChar, -1},
		{"VALUES('a'::varchar(2)),('b'::varchar(3))", sqltype.VarChar, -1},
	} {
		t.Run(tc.query, func(t *testing.T) {
			b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
			query, err := b.bindSelect(statement(t, tc.query).(*parser.Select), false)
			if err != nil { t.Fatal(err) }
			column := query.Root.Schema()[0]
			if column.Name != "column1" || column.Usage.Type.ID() != tc.id || column.Usage.Typmod != tc.typmod || column.NullabilityKnown { t.Fatal(column) }
			output := query.Root.(*projectNode).expressions[0].(columnExpression)
			if output.origin != (ObjectRef{}) || len(b.querySources) != 0 || len(b.uses) != 0 { t.Fatal("synthetic source acquired physical identity", output, b.querySources) }
			for _, dependency := range b.dependencies {
				if dependency.ref.Address.ClassID == RelationClassID { t.Fatal("synthetic source acquired relation dependency", dependency) }
			}
		})
	}
}

func TestQueryFromValuesCellPrecedenceAndStarts(t *testing.T) {
	for _, tc := range []struct { query, code, message, position string }{
		{"VALUES(1),(2,DEFAULT)", "42601", "DEFAULT is not allowed in this context", "14"},
		{"VALUES(1),(missing,2)", "42703", `column "missing" does not exist`, "12"},
		{"VALUES(1),(2+3,4)", "42601", "VALUES lists must all be the same length", "12"},
		{"VALUES(1),(2::integer,4)", "42601", "VALUES lists must all be the same length", "12"},
		{"VALUES(1),(TRUE)", "42804", "VALUES types integer and boolean cannot be matched", "12"},
	} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		_, err := b.bindSelect(statement(t, tc.query).(*parser.Select), false)
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
		if !errors.As(err, &diagnostic) || *diagnostic != want { t.Fatal(tc.query, err, diagnostic, want) }
		if b.queryNamespace != nil || b.queryNonLateral { t.Fatal("VALUES leaked cell namespace") }
	}
}

func TestQueryFromValuesMixedDemandAndCancellation(t *testing.T) {
	for _, mode := range []string{"complete", "limit0", "cancel", "error"} {
		t.Run(mode, func(t *testing.T) {
			e, query, source := queryCoreFixture(t, "SELECT id FROM t")
			project := query.Root.(*projectNode)
			usage := project.Schema()[0].Usage
			calls := []int{0, 0, 0}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("VALUES cell failure")
			cells := make([][]BoundExpr, 3)
			for i := range cells {
				index := i
				cells[i] = []BoundExpr{queryProbe(usage, func(c *EvalContext) (sqltype.Value, error) {
					calls[index]++
					if index == 1 && mode == "cancel" { cancel() }
					if index == 1 && mode == "error" { return sqltype.Value{}, failure }
					return sqltype.Integer(usage.Type, 7)
				})}
			}
			values := &valuesNode{querySchema: querySchema{{Name: "column1", Usage: usage}}, rows: cells}
			width := len(project.child.Schema())
			schema := append(append(querySchema(nil), project.child.Schema()...), values.Schema()...)
			join := &joinNode{querySchema: schema, left: project.child, right: values, leftWidth: width, rightWidth: 1}
			project.child = join
			project.querySchema = querySchema{{Name: "n", Usage: usage}}
			project.expressions = []BoundExpr{columnExpression{expressionInfo: expressionInfo{usage: usage, properties: ExprProperties{RowDependent: true}}, scope: 1, frame: 1, slot: width}}
			if mode == "limit0" {
				typ, _ := e.CatalogRead().LookupType(sqltype.Int8)
				zero, err := sqltype.Integer(typ, 0)
				if err != nil { t.Fatal(err) }
				project.child = &limitNode{querySchema: schema, child: join, count: literalExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, value: zero}}
			}
			rows, count, err := finalizeQuery(ctx, project, source, nil)
			if mode == "cancel" || mode == "error" {
				want := failure
				if mode == "cancel" { want = context.Canceled }
				if rows != nil || count != 0 || !errors.Is(err, want) || source.view != nil || !reflect.DeepEqual(calls, []int{1, 1, 0}) { t.Fatal(rows, count, err, source.view, calls) }
				return
			}
			if err != nil { t.Fatal(err) }
			defer rows.Close()
			wantCount, wantCalls := 12, []int{1, 1, 1}
			if mode == "limit0" { wantCount, wantCalls = 0, []int{0, 0, 0} }
			if count != wantCount || source.view != nil || !reflect.DeepEqual(calls, wantCalls) { t.Fatal(count, source.view, calls) }
		})
	}
}

func TestQueryFromValuesLockRecheckKeepsSyntheticOccurrence(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
	usage := typ.Usage()
	calls := 0
	values := &valuesNode{querySchema: querySchema{{Name: "column1", Usage: usage}}, rows: [][]BoundExpr{{queryProbe(usage, func(*EvalContext) (sqltype.Value, error) { calls++; return sqltype.Integer(typ, 99) })}}}
	physical := &sourceNode{querySchema: querySchema{{Name: "n", Usage: usage}}, occurrence: 0}
	join := &joinNode{querySchema: append(append(querySchema(nil), physical.Schema()...), values.Schema()...), left: physical, right: values, leftWidth: 1, rightWidth: 1}
	integer := func(n int64) sqltype.Value {
		value, err := sqltype.Integer(typ, n)
		if err != nil { t.Fatal(err) }
		return value
	}
	frozen := []sqltype.Value{integer(1), integer(7)}
	current := [][]sqltype.Value{{integer(2)}}
	scratch := make([]sqltype.Value, recheckScratchWidth(join))
	offset := 0
	row, present, err := recheckQueryRow(join, current, &EvalContext{Context: context.Background()}, scratch, &offset, frozen)
	if err != nil || !present || calls != 0 { t.Fatal(row, present, err, calls) }
	left, _ := row[0].Integer()
	right, _ := row[1].Integer()
	if left != 2 || right != 7 { t.Fatal("synthetic cell reevaluated during physical recheck", row) }
}
