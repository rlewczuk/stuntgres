package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePreparedFoldPointInvalidation(t *testing.T) {
	for _, route := range []string{"go", "sql"} {
		for _, nested := range []bool{false, true} {
			label, query := "text", preparedFoldPointSQL
			if nested {
				label, query = "nested", preparedFoldLengthSQL
			}
			for _, cause := range []string{"discard", "relation", "path"} {
				t.Run(route+"/"+label+"/"+cause, func(t *testing.T) {
					preparedFoldScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := func(s fixture.Step) { sqlPreparedStep(t, p, b, c, s) }
						source := query
						if cause == "relation" {
							run(fixture.Exec("CREATE TABLE fold_dep(v INT)", 0))
							run(fixture.Exec("INSERT INTO fold_dep VALUES (1)", 1))
							source += " FROM fold_dep"
						}
						if cause == "path" {
							// All namespace creation precedes warming: callbacks cannot
							// masquerade as effective-path invalidation.
							run(fixture.Exec("CREATE SCHEMA fold_path_a", 0))
							run(fixture.Exec("CREATE SCHEMA fold_path_b", 0))
							run(fixture.Exec("SET search_path TO fold_path_a, public", 0))
						}
						run(fixture.Exec("SET extra_float_digits TO 3", 0))
						r := preparedFoldHold(t, p, b, c, route, "fold_invalidation", source)
						preparedFoldRead(t, p, b, r, nested, true)
						// Keep independently checked returned values and metadata alive
						// across retirement. Drain pq before issuing more session traffic.
						var oldNames []string
						var oldTypes []*sql.ColumnType
						var oldValue any
						oldRows, err := r.QueryContext(p.Ctx, r.SQL)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { oldRows.Close() })
						oldNames, err = oldRows.Columns()
						if err != nil {
							t.Fatal(err)
						}
						oldTypes, err = oldRows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						if !oldRows.Next() {
							t.Fatalf("missing old row: %v", oldRows.Err())
						}
						if err := oldRows.Scan(&oldValue); err != nil {
							t.Fatal(err)
						}
						if oldRows.Next() || oldRows.Err() != nil {
							t.Fatalf("old rows count/error: %v", oldRows.Err())
						}
						if err := oldRows.Close(); err != nil {
							t.Fatal(err)
						}
						checkOld := func() {
							t.Helper()
							name, typ, value := "p", "TEXT", any("(0.10000000000000002,1)")
							if nested {
								name, typ, value = "n", "INT4", int64(23)
							}
							if !reflect.DeepEqual(oldNames, []string{name}) || len(oldTypes) != 1 || oldTypes[0].Name() != name || oldTypes[0].DatabaseTypeName() != typ || !reflect.DeepEqual(fixture.Normalize(oldValue), value) {
								t.Fatalf("old result changed: names=%v types=%v value=%v", oldNames, oldTypes, oldValue)
							}
						}
						checkOld()
						run(fixture.Exec("SET extra_float_digits TO 0", 0))
						switch cause {
						case "discard":
							run(fixture.Exec("DISCARD PLANS", 0))
						case "relation":
							run(fixture.Exec("UPDATE fold_dep SET v = 2", 1))
							preparedFoldRead(t, p, b, r, nested, true)
							run(fixture.Exec("CREATE TABLE fold_unrelated(v INT)", 0))
							preparedFoldRead(t, p, b, r, nested, true)
							run(fixture.Exec("ALTER TABLE fold_dep ADD COLUMN unused INT", 0))
						case "path":
							run(fixture.Exec("SET search_path TO fold_path_a, fold_path_a, fold_path_missing, public", 0))
							preparedFoldRead(t, p, b, r, nested, true)
							run(fixture.Exec("SET search_path TO fold_path_b, public", 0))
						}
						preparedFoldRead(t, p, b, r, nested, false)
						preparedFoldRead(t, p, b, r, nested, false)
						checkOld()
					})
				})
			}
		}
	}
	t.Run("sql/deallocate_recreate", func(t *testing.T) {
		preparedFoldScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { sqlPreparedStep(t, p, b, c, s) }
			run(fixture.Exec("SET extra_float_digits TO 3", 0))
			r := preparedFoldHold(t, p, b, c, "sql", "fold_replaced", preparedFoldPointSQL)
			preparedFoldRead(t, p, b, r, false, true)
			run(fixture.Exec("SET extra_float_digits TO 0", 0))
			run(fixture.Exec("DEALLOCATE fold_replaced", 0))
			run(fixture.ErrorDiagnostic("EXECUTE fold_replaced", sqlPreparedMissing("fold_replaced")))
			r = preparedFoldHold(t, p, b, c, "sql", "fold_replaced", preparedFoldPointSQL)
			preparedFoldRead(t, p, b, r, false, false)
		})
	})
}
