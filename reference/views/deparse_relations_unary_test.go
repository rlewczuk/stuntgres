package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func deparseRelationsUnaryGroupScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func TestReferenceDeparseRelationsUnaryReplay(t *testing.T) {
	deparseRelationsUnaryGroupScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.DeparseRelationsSeed(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("INSERT INTO t VALUES(NULL,NULL,true)", 1))
		run(fixture.Exec("CREATE VIEW unary_view AS SELECT id,-(id+1) AS answer FROM t WHERE visible", 0))
		calls := []string{"pg_get_viewdef('unary_view'::text)", "pg_get_viewdef('unary_view'::text,false)", "pg_get_viewdef('unary_view'::text,true)", "pg_get_viewdef('unary_view'::regclass,1)", "pg_get_viewdef('unary_view'::regclass,20)", "pg_get_viewdef('unary_view'::regclass,80)", "pg_get_viewdef('unary_view'::regclass,-1)"}
		names := []string{"unary_view"}
		var defaultDefinition string
		for i, call := range calls {
			d := fixture.DeparseRelationsRead(t, p, b, c, "SELECT "+call+" AS definition")
			if i == 0 {
				defaultDefinition = d
			}
			if i == 1 && d != defaultDefinition {
				t.Fatalf("unary omitted pretty differs from false: %q versus %q", defaultDefinition, d)
			}
			name := fmt.Sprintf("unary_copy_%d", i)
			names = append(names, name)
			run(fixture.Exec("CREATE VIEW "+name+" AS "+d, 0))
		}
		for _, name := range names {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT id,answer FROM "+name, []string{"id", "answer"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(-2)}, {int64(3), int64(-4)}, {int64(3), int64(-4)}, {nil, nil}})
				s.Bag = true
				s.Prepared = prepared
				run(s)
			}
		}
	})
}

func TestReferenceDeparseRelationsSharedUnaryConsumers(t *testing.T) {
	deparseRelationsUnaryGroupScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE unary_source(id INT,CONSTRAINT unary_original CHECK(-(id+1)<-1))", 0))
		for _, pretty := range []bool{false, true} {
			expression := fixture.DeparseRelationsRead(t, p, b, c, fmt.Sprintf("SELECT pg_get_expr(conbin,conrelid,%v) AS definition FROM pg_catalog.pg_constraint WHERE conname='unary_original'", pretty))
			constraint := fixture.DeparseRelationsRead(t, p, b, c, fmt.Sprintf("SELECT pg_get_constraintdef(oid,%v) AS definition FROM pg_catalog.pg_constraint WHERE conname='unary_original'", pretty))
			for _, tc := range []struct{ kind, definition string }{{"expr", "CHECK(" + expression + ")"}, {"constraint", constraint}} {
				for _, prepared := range []bool{false, true} {
					name := fmt.Sprintf("unary_%s_%v_%v", tc.kind, pretty, prepared)
					run(fixture.Exec("CREATE TABLE "+name+"(id INT,CONSTRAINT unary_copy_check "+tc.definition+")", 0))
					accepted := fixture.Exec("INSERT INTO "+name+" VALUES(1),(3),(3),(NULL)", 4)
					accepted.Prepared = prepared
					run(accepted)
					want := fixture.ControlsReferenceDiagnostic("23514", fmt.Sprintf("new row for relation %q violates check constraint %q", name, "unary_copy_check"))
					want.Detail = "Failing row contains (0)."
					want.Schema = "public"
					want.Table = name
					want.Constraint = "unary_copy_check"
					failure := fixture.ErrorDiagnostic("INSERT INTO "+name+" VALUES(0)", want)
					failure.Prepared = prepared
					run(failure)
					s := fixture.Select("SELECT id FROM "+name, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}, {int64(3)}, {nil}})
					s.Bag = true
					s.Prepared = prepared
					run(s)
				}
			}
		}
	})
}
