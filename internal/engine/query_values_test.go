package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryFromValuesCommonSchema(t *testing.T) {
	for _, tc := range []struct {
		query  string
		id     sqltype.ID
		typmod int32
	}{
		{"VALUES(NULL),(NULL),(1)", sqltype.Int4, -1},
		{"VALUES(NULL),(NULL)", sqltype.Text, -1},
		{"VALUES('1'),(2)", sqltype.Int4, -1},
		{"VALUES('a'::char(2)),('b'::char(2))", sqltype.BPChar, 6},
		{"VALUES(NULL),('a'::char(2))", sqltype.BPChar, -1},
		{"VALUES('a'::varchar(2)),('b'::varchar(3))", sqltype.VarChar, -1},
	} {
		t.Run(tc.query, func(t *testing.T) {
			b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
			query, err := b.BindSelect(statement(t, tc.query).(*parser.Select), false)
			if err != nil {
				t.Fatal(err)
			}
			column := query.Root.Schema()[0]
			if column.Name != "column1" || column.Usage.Type.ID() != tc.id || column.Usage.Typmod != tc.typmod || column.NullabilityKnown {
				t.Fatal(column)
			}
			output := query.Root.(*plan.ProjectNode).Expressions[0].(plan.ColumnExpr)
			if output.Origin != (ids.ObjectRef{}) || len(b.Query.Sources) != 0 || len(b.Target.Uses) != 0 {
				t.Fatal("synthetic source acquired physical identity", output, b.Query.Sources)
			}
			for _, dependency := range b.Dependencies {
				if dependency.Ref.Address.ClassID == ids.RelationClassID {
					t.Fatal("synthetic source acquired relation dependency", dependency)
				}
			}
		})
	}
}

func TestQueryFromValuesCellPrecedenceAndStarts(t *testing.T) {
	for _, tc := range []struct{ query, code, message, position string }{
		{"VALUES(1),(2,DEFAULT)", "42601", "DEFAULT is not allowed in this context", "14"},
		{"VALUES(1),(missing,2)", "42703", `column "missing" does not exist`, "12"},
		{"VALUES(1),(2+3,4)", "42601", "VALUES lists must all be the same length", "12"},
		{"VALUES(1),(2::integer,4)", "42601", "VALUES lists must all be the same length", "12"},
		{"VALUES(1),(TRUE)", "42804", "VALUES types integer and boolean cannot be matched", "12"},
	} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		_, err := b.BindSelect(statement(t, tc.query).(*parser.Select), false)
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
		if !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatal(tc.query, err, diagnostic, want)
		}
		if b.Query.Namespace != nil || b.Query.NonLateral {
			t.Fatal("VALUES leaked cell namespace")
		}
	}
}

func TestQueryFromValuesMixedDemandAndCancellation(t *testing.T) {
	for _, mode := range []string{"complete", "limit0", "cancel", "error"} {
		t.Run(mode, func(t *testing.T) {
			e, query, source := queryCoreFixture(t, "SELECT id FROM t")
			project := query.Root.(*plan.ProjectNode)
			usage := project.Schema()[0].Usage
			calls := []int{0, 0, 0}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("VALUES cell failure")
			cells := make([][]expr.Expr, 3)
			for i := range cells {
				index := i
				cells[i] = []expr.Expr{queryProbe(usage, func(c *exec.Context) (sqltype.Value, error) {
					calls[index]++
					if index == 1 && mode == "cancel" {
						cancel()
					}
					if index == 1 && mode == "error" {
						return sqltype.Value{}, failure
					}
					return sqltype.Integer(usage.Type, 7)
				})}
			}
			values := &plan.ValuesNode{QuerySchema: plan.QuerySchema{{Name: "column1", Usage: usage}}, Rows: cells}
			width := len(project.Child.Schema())
			schema := append(append(plan.QuerySchema(nil), project.Child.Schema()...), values.Schema()...)
			join := &plan.JoinNode{QuerySchema: schema, Left: project.Child, Right: values, LeftWidth: width, RightWidth: 1}
			project.Child = join
			project.QuerySchema = plan.QuerySchema{{Name: "n", Usage: usage}}
			project.Expressions = []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: width}}
			if mode == "limit0" {
				typ, _ := e.CatalogRead().LookupType(sqltype.Int8)
				zero, err := sqltype.Integer(typ, 0)
				if err != nil {
					t.Fatal(err)
				}
				project.Child = &plan.LimitNode{QuerySchema: schema, Child: join, Count: plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: zero}}
			}
			rows, count, err := exec.Finalize(ctx, project, source, nil)
			if mode == "cancel" || mode == "error" {
				want := failure
				if mode == "cancel" {
					want = context.Canceled
				}
				if rows != nil || count != 0 || !errors.Is(err, want) || source.View != nil || !reflect.DeepEqual(calls, []int{1, 1, 0}) {
					t.Fatal(rows, count, err, source.View, calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			wantCount, wantCalls := 12, []int{1, 1, 1}
			if mode == "limit0" {
				wantCount, wantCalls = 0, []int{0, 0, 0}
			}
			if count != wantCount || source.View != nil || !reflect.DeepEqual(calls, wantCalls) {
				t.Fatal(count, source.View, calls)
			}
		})
	}
}

func TestQueryFromValuesLockRecheckKeepsSyntheticOccurrence(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
	usage := typ.Usage()
	calls := 0
	values := &plan.ValuesNode{QuerySchema: plan.QuerySchema{{Name: "column1", Usage: usage}}, Rows: [][]expr.Expr{{queryProbe(usage, func(*exec.Context) (sqltype.Value, error) { calls++; return sqltype.Integer(typ, 99) })}}}
	physical := &plan.SourceNode{QuerySchema: plan.QuerySchema{{Name: "n", Usage: usage}}, Occurrence: 0}
	join := &plan.JoinNode{QuerySchema: append(append(plan.QuerySchema(nil), physical.Schema()...), values.Schema()...), Left: physical, Right: values, LeftWidth: 1, RightWidth: 1}
	integer := func(n int64) sqltype.Value {
		value, err := sqltype.Integer(typ, n)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	frozen := []sqltype.Value{integer(1), integer(7)}
	current := [][]sqltype.Value{{integer(2)}}
	inputs := &exec.Source{Occurrence: 0}
	scope := &StatementScope{queryLocks: &exec.LockContext{Leaves: []exec.QueryLockLeaf{{}}}}
	scratch := make([]sqltype.Value, exec.QueryLockRecheckWidth(join, inputs))
	offset := 0
	row, present, err := exec.RecheckLockedQuery(join, inputs, current, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: hostOf(scope)}}, scratch, &offset, frozen)
	if err != nil || !present || calls != 0 {
		t.Fatal(row, present, err, calls)
	}
	left, _ := row[0].Integer()
	right, _ := row[1].Integer()
	if left != 2 || right != 7 {
		t.Fatal("synthetic cell reevaluated during physical recheck", row)
	}
}
