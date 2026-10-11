package reference_test

import (
	"bytes"
	"fmt"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewReplaceInheritedVisibility(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
					for _, q := range []string{"CREATE TABLE ids(id OID)", "CREATE VIEW v AS SELECT FALSE AS visible FROM ids", "CREATE VIEW w AS SELECT visible FROM v", "CREATE VIEW z AS SELECT visible FROM w", "CREATE TABLE destination(visible BOOL)", "CREATE TABLE checked(visible BOOL CONSTRAINT known CHECK(visible IS NOT NULL))"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO ids VALUES(23),(NULL)", 2))
					run(fixture.Exec("INSERT INTO checked VALUES(FALSE)", 1))
					query := "SELECT visible FROM z ORDER BY visible"
					held, err := backend.DB.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					heldRows, err := held.QueryContext(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer heldRows.Close()
					heldExecutor := fixture.HeldStatement{Executor: backend.DB, Statement: held, SQL: query}
					for phase, expression := range []string{"pg_type_is_visible(id)", "FALSE", "pg_type_is_visible(id)"} {
						run(fixture.Exec("CREATE OR REPLACE VIEW v AS SELECT "+expression+" AS visible FROM ids", 0))
						want := [][]any{{true}, {nil}}
						if expression == "FALSE" {
							want = [][]any{{false}, {false}}
						}
						for _, view := range []string{"v", "w", "z"} {
							run(fixture.Select("SELECT visible FROM "+view+" ORDER BY visible", []string{"visible"}, []string{"BOOL"}, want))
						}
						fixture.RunStep(t, p.Ctx, heldExecutor, backend.Recorder, fixture.Select(query, []string{"visible"}, []string{"BOOL"}, want))
						cleared := int64(0)
						if phase != 0 {
							cleared = 2
						}
						run(fixture.Exec("DELETE FROM destination", cleared))
						run(fixture.Exec("INSERT INTO destination SELECT visible FROM z ORDER BY visible", 2))
						run(fixture.Select("SELECT visible FROM destination ORDER BY visible", []string{"visible"}, []string{"BOOL"}, want))
						if expression != "FALSE" {
							bad := fixture.ImplicitError("INSERT INTO checked SELECT visible FROM z ORDER BY visible", fixture.Diagnostic{Code: "23514", Message: `new row for relation "checked" violates check constraint "known"`, Detail: "Failing row contains (null).", Schema: "public", Table: "checked", Constraint: "known"})
							bad.ErrorStage = "execute"
							run(bad)
							run(fixture.Select("SELECT visible FROM checked", []string{"visible"}, []string{"BOOL"}, [][]any{{false}}))
						}
						if phase == 0 {
							run(fixture.Exec("CREATE VIEW late_dependent AS SELECT visible FROM v", 0))
						}
						run(fixture.Select("SELECT visible FROM late_dependent ORDER BY visible", []string{"visible"}, []string{"BOOL"}, want))
					}
					columns, err := heldRows.Columns()
					if err != nil || !reflect.DeepEqual(columns, []string{"visible"}) {
						t.Fatal(columns, err)
					}
					types, err := heldRows.ColumnTypes()
					if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "BOOL" {
						t.Fatal(types, err)
					}
					count := 0
					for heldRows.Next() {
						var visible bool
						if err := heldRows.Scan(&visible); err != nil || visible {
							t.Fatal("held rows changed after replacement", visible, err)
						}
						count++
					}
					if heldRows.Err() != nil || count != 2 {
						t.Fatal(count, heldRows.Err())
					}
				})
			}
		})
	}
}

func TestReferenceDropViewIndexWrongKind(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, query := range []string{"DROP VIEW ix", "DROP VIEW IF EXISTS ix", "DROP VIEW v,ix"} {
			t.Run(fmt.Sprintf("%s/prepared=%v", query, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
						for _, q := range []string{"CREATE TABLE t(id INT)", "CREATE INDEX ix ON t(id)", "CREATE VIEW v AS SELECT id FROM t"} {
							run(fixture.Exec(q, 0))
						}
						run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
						type identity struct {
							oid, namespace, owner int64
							kind                  string
						}
						before := make(map[string]identity)
						for _, name := range []string{"t", "ix", "v"} {
							var value identity
							if err := backend.DB.QueryRowContext(p.Ctx, "SELECT oid,relnamespace,relowner,relkind FROM pg_class WHERE relname=$1", name).Scan(&value.oid, &value.namespace, &value.owner, &value.kind); err != nil {
								t.Fatal(err)
							}
							before[name] = value
						}
						var viewDefinition string
						if err := backend.DB.QueryRowContext(p.Ctx, "SELECT definition FROM pg_views WHERE viewname='v'").Scan(&viewDefinition); err != nil {
							t.Fatal(err)
						}
						var dump bytes.Buffer
						if !backend.PG {
							if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
								t.Fatal(err)
							}
						}
						indexRow := fixture.PgIndexesRow("public", "t", "ix", "CREATE INDEX ix ON public.t USING btree (id)")
						fixture.PgIndexesCheck(t, p, backend, backend.DB, "ix", indexRow)
						bad := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42809", Message: `"ix" is not a view`, Hint: "Use DROP INDEX to remove an index."})
						bad.Prepared, bad.ErrorStage = prepared, "execute"
						run(bad)
						for name, expected := range before {
							var actual identity
							if err := backend.DB.QueryRowContext(p.Ctx, "SELECT oid,relnamespace,relowner,relkind FROM pg_class WHERE relname=$1", name).Scan(&actual.oid, &actual.namespace, &actual.owner, &actual.kind); err != nil || actual != expected {
								t.Fatal(name, actual, expected, err)
							}
						}
						var retainedDefinition string
						if err := backend.DB.QueryRowContext(p.Ctx, "SELECT definition FROM pg_views WHERE viewname='v'").Scan(&retainedDefinition); err != nil || retainedDefinition != viewDefinition {
							t.Fatal(retainedDefinition, viewDefinition, err)
						}
						if !backend.PG {
							var after bytes.Buffer
							if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
								t.Fatal(err)
							}
							if after.String() != dump.String() {
								t.Fatal("DROP changed native persistent definitions/data")
							}
						}
						fixture.PgIndexesCheck(t, p, backend, backend.DB, "ix", indexRow)
						run(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
						run(fixture.Select("SELECT id FROM v", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					})
				}
			})
		}
	}
}
