//go:build reference

package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres"
	"testing"
)

func TestReferenceViewReplaceInheritedVisibility(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			p := newReferencePair(t)
			for _, backend := range []struct {
				name   string
				db     *sql.DB
				record *referenceRecorder
			}{{"postgres_first", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) {
						s.prepared = prepared
						referenceRunStep(t, p.ctx, backend.db, backend.record, s)
					}
					for _, q := range []string{"CREATE TABLE ids(id OID)", "CREATE VIEW v AS SELECT FALSE AS visible FROM ids", "CREATE VIEW w AS SELECT visible FROM v", "CREATE VIEW z AS SELECT visible FROM w", "CREATE TABLE destination(visible BOOL)", "CREATE TABLE checked(visible BOOL CONSTRAINT known CHECK(visible IS NOT NULL))"} {
						run(referenceExec(q, 0))
					}
					run(referenceExec("INSERT INTO ids VALUES(23),(NULL)", 2))
					run(referenceExec("INSERT INTO checked VALUES(FALSE)", 1))
					query := "SELECT visible FROM z ORDER BY visible"
					held, err := backend.db.PrepareContext(p.ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					heldRows, err := held.QueryContext(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer heldRows.Close()
					heldExecutor := referenceHeldStatement{referenceExecutor: backend.db, statement: held, query: query}
					for phase, expression := range []string{"pg_type_is_visible(id)", "FALSE", "pg_type_is_visible(id)"} {
						run(referenceExec("CREATE OR REPLACE VIEW v AS SELECT "+expression+" AS visible FROM ids", 0))
						want := [][]any{{true}, {nil}}
						if expression == "FALSE" {
							want = [][]any{{false}, {false}}
						}
						for _, view := range []string{"v", "w", "z"} {
							run(referenceSelect("SELECT visible FROM "+view+" ORDER BY visible", []string{"visible"}, []string{"BOOL"}, want))
						}
						referenceRunStep(t, p.ctx, heldExecutor, backend.record, referenceSelect(query, []string{"visible"}, []string{"BOOL"}, want))
						cleared := int64(0)
						if phase != 0 {
							cleared = 2
						}
						run(referenceExec("DELETE FROM destination", cleared))
						run(referenceExec("INSERT INTO destination SELECT visible FROM z ORDER BY visible", 2))
						run(referenceSelect("SELECT visible FROM destination ORDER BY visible", []string{"visible"}, []string{"BOOL"}, want))
						if expression != "FALSE" {
							bad := referenceImplicitError("INSERT INTO checked SELECT visible FROM z ORDER BY visible", referenceDiagnostic{Code: "23514", Message: `new row for relation "checked" violates check constraint "known"`, Detail: "Failing row contains (null).", Schema: "public", Table: "checked", Constraint: "known"})
							bad.errorStage = "execute"
							run(bad)
							run(referenceSelect("SELECT visible FROM checked", []string{"visible"}, []string{"BOOL"}, [][]any{{false}}))
						}
						if phase == 0 {
							run(referenceExec("CREATE VIEW late_dependent AS SELECT visible FROM v", 0))
						}
						run(referenceSelect("SELECT visible FROM late_dependent ORDER BY visible", []string{"visible"}, []string{"BOOL"}, want))
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
				p := newReferencePair(t)
				for _, backend := range []struct {
					name   string
					db     *sql.DB
					record *referenceRecorder
				}{{"postgres_first", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
					t.Run(backend.name, func(t *testing.T) {
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
						for _, q := range []string{"CREATE TABLE t(id INT)", "CREATE INDEX ix ON t(id)", "CREATE VIEW v AS SELECT id FROM t"} {
							run(referenceExec(q, 0))
						}
						run(referenceExec("INSERT INTO t VALUES(7)", 1))
						type identity struct {
							oid, namespace, owner int64
							kind                  string
						}
						before := make(map[string]identity)
						for _, name := range []string{"t", "ix", "v"} {
							var value identity
							if err := backend.db.QueryRowContext(p.ctx, "SELECT oid,relnamespace,relowner,relkind FROM pg_class WHERE relname=$1", name).Scan(&value.oid, &value.namespace, &value.owner, &value.kind); err != nil {
								t.Fatal(err)
							}
							before[name] = value
						}
						var viewDefinition, indexDefinition string
						if err := backend.db.QueryRowContext(p.ctx, "SELECT definition FROM pg_views WHERE viewname='v'").Scan(&viewDefinition); err != nil {
							t.Fatal(err)
						}
						var dump bytes.Buffer
						if backend.name == "native" {
							if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
								t.Fatal(err)
							}
						} else if err := backend.db.QueryRowContext(p.ctx, "SELECT indexdef FROM pg_indexes WHERE indexname='ix'").Scan(&indexDefinition); err != nil {
							t.Fatal(err)
						}
						bad := referenceImplicitError(query, referenceDiagnostic{Code: "42809", Message: `"ix" is not a view`, Hint: "Use DROP INDEX to remove an index."})
						bad.prepared, bad.errorStage = prepared, "execute"
						run(bad)
						for name, expected := range before {
							var actual identity
							if err := backend.db.QueryRowContext(p.ctx, "SELECT oid,relnamespace,relowner,relkind FROM pg_class WHERE relname=$1", name).Scan(&actual.oid, &actual.namespace, &actual.owner, &actual.kind); err != nil || actual != expected {
								t.Fatal(name, actual, expected, err)
							}
						}
						var retainedDefinition string
						if err := backend.db.QueryRowContext(p.ctx, "SELECT definition FROM pg_views WHERE viewname='v'").Scan(&retainedDefinition); err != nil || retainedDefinition != viewDefinition {
							t.Fatal(retainedDefinition, viewDefinition, err)
						}
						if backend.name == "native" {
							var after bytes.Buffer
							if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
								t.Fatal(err)
							}
							if after.String() != dump.String() {
								t.Fatal("DROP changed native persistent definitions/data")
							}
						} else {
							if err := backend.db.QueryRowContext(p.ctx, "SELECT indexdef FROM pg_indexes WHERE indexname='ix'").Scan(&retainedDefinition); err != nil || retainedDefinition != indexDefinition {
								t.Fatal(retainedDefinition, indexDefinition, err)
							}
						}
						run(referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
						run(referenceSelect("SELECT id FROM v", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					})
				}
			})
		}
	}
}
