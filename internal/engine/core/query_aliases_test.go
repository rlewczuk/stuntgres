package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQueryAliasesMetadata(t *testing.T) {
	for _, populated := range []bool{false, true} {
		e := engine.New()
		enginetest.Run(t, e, "CREATE TABLE t(id INT NOT NULL,a INT,b INT)")
		if populated {
			enginetest.Run(t, e, "INSERT INTO t VALUES(1,2,1)")
		}
		op, done, err := enginetest.Operation(e, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		base, err := e.Describe(op, enginetest.Statement(t, "SELECT * FROM t"))
		if err != nil || len(base.Results) != 3 || base.Results[0].OriginTable == 0 {
			done()
			t.Fatal(base, err)
		}
		typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
		usage := sqltype.Usage{Type: typ, Typmod: -1}
		origin := base.Results[0].OriginTable
		want := []catalog.Column{
			{Name: "shown", Usage: usage, NullabilityKnown: true, OriginTable: origin, OriginAttribute: 1},
			{Name: "a", Usage: usage, Nullable: true, NullabilityKnown: true, OriginTable: origin, OriginAttribute: 2},
			{Name: "b", Usage: usage, Nullable: true, NullabilityKnown: true, OriginTable: origin, OriginAttribute: 3},
			{Name: "computed", Usage: usage},
		}
		ast := enginetest.Statement(t, "SELECT x.iid AS shown,x.a,x.b,x.iid+1 AS computed FROM t x(iid)")
		description, err := e.Describe(op, ast)
		if err != nil || !reflect.DeepEqual(description.Results, want) {
			done()
			t.Fatal(description.Results, want, err)
		}
		for _, prepared := range []bool{false, true} {
			scope, err := e.StartStatement(op)
			if err != nil {
				done()
				t.Fatal(err)
			}
			if prepared {
				scope.Expected = &description
			}
			result, err := scope.Execute(ast, 0, nil)
			err = scope.Finish(err)
			if err != nil || result.Rows == nil {
				done()
				t.Fatal(result, err)
			}
			got := result.Rows.Schema()
			result.Rows.Close()
			if !reflect.DeepEqual(got, want) {
				done()
				t.Fatal(got, want)
			}
		}
		done()
	}
}

func TestQueryAliasesCancellation(t *testing.T) {
	for _, phase := range []string{"projection", "prepublication"} {
		t.Run(phase, func(t *testing.T) {
			query, source := enginetest.QueryFixture(t, engine.New(), "SELECT x.iid AS shown FROM t x(iid) WHERE x.f")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			project := query.Root.(*plan.ProjectNode)
			calls := 0
			project.Expressions[0] = enginetest.QueryProbe(project.Schema()[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
				calls++
				if phase == "projection" || calls == 2 {
					cancel()
				}
				return c.Row[0], nil
			})
			rows, count, err := exec.Finalize(ctx, query.Root, source, nil)
			if rows != nil || count != 0 || err != context.Canceled || source.View != nil || calls == 0 {
				t.Fatal(rows, count, err, source.View, calls)
			}
		})
	}
}

func TestQueryAliasesTypmodAndCollation(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE typed(s VARCHAR(5),n NUMERIC(7,2))")
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	base, err := e.Describe(op, enginetest.Statement(t, "SELECT * FROM typed"))
	if err != nil || len(base.Results) != 2 || base.Results[0].OriginTable == 0 {
		t.Fatal(base, err)
	}
	varchar, _ := e.CatalogRead().LookupType(sqltype.VarChar)
	numeric, _ := e.CatalogRead().LookupType(sqltype.Numeric)
	origin := base.Results[0].OriginTable
	want := []catalog.Column{
		{Name: "label", Usage: sqltype.Usage{Type: varchar, Typmod: 9, Collation: sqltype.CollationRef{ID: 100, Derivation: sqltype.Default}}, Nullable: true, NullabilityKnown: true, OriginTable: origin, OriginAttribute: 1},
		{Name: "price", Usage: sqltype.Usage{Type: numeric, Typmod: (7 << 16) + 2 + 4}, Nullable: true, NullabilityKnown: true, OriginTable: origin, OriginAttribute: 2},
	}
	for _, q := range []string{"SELECT x.word AS label,x.amount AS price FROM typed x(word,amount)", "SELECT x.word AS label,x.amount AS price FROM typed x(word,amount) WHERE FALSE"} {
		description, err := e.Describe(op, enginetest.Statement(t, q))
		if err != nil || !reflect.DeepEqual(description.Results, want) {
			t.Fatal(description.Results, want, err)
		}
	}
}

func TestQueryAliasesFailure(t *testing.T) {
	query, source := enginetest.QueryFixture(t, engine.New(), "SELECT 1/(x.iid-2) AS quotient FROM t x(iid) ORDER BY x.iid")
	rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}
	if rows != nil || count != 0 || source.View != nil || !errors.As(err, &got) || *got != want {
		t.Fatal(rows, count, source.View, got, err, want)
	}
}
