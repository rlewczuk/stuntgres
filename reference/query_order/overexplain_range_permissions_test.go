package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOverexplainRangePermissions(t *testing.T) {
	t.Run("source-column-whole-presence-rowmark", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"range_reader"})
		overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			overexplainRangeSeed(t, p, b, c)
			for _, q := range []string{"CREATE ROLE range_reader", "GRANT USAGE ON SCHEMA public TO range_reader", "GRANT SELECT(id) ON range_src TO range_reader", "LOAD 'pg_overexplain'", "SET ROLE range_reader"} {
				overexplainExec(t, p, b, c, q)
			}
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			capture := func(child string) map[string]any {
				r := overexplainQuery(t, p, c, overexplainSQL("JSON", "RANGE_TABLE, COSTS FALSE", child), "JSON", true)
				entries := overexplainRange(t, b, r, "JSON", true)
				e := overexplainRangeNamed(t, b, entries, "relation", "range_src")
				if b.PG && overexplainInteger(t, e["Permission Info Index"], false) <= 0 {
					t.Fatal("missing PG permission reference", e)
				}
				return e
			}
			e := capture("SELECT id FROM range_src")
			if !b.PG {
				overexplainRangeDemands(t, e, "range_reader", []overexplainRangeDemand{{"SELECT", []string{"id"}, false}})
			}
			for _, child := range []string{"SELECT v FROM range_src", "SELECT tableoid FROM range_src", "SELECT id FROM range_src FOR UPDATE"} {
				q := overexplainSQL("JSON", "RANGE_TABLE", child)
				overexplainError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table range_src"), "execute", true)
			}
			for _, child := range []string{"SELECT count(*) FROM range_src", "SELECT EXISTS(SELECT 1 FROM range_src)"} {
				e := capture(child)
				if !b.PG {
					overexplainRangeDemands(t, e, "range_reader", []overexplainRangeDemand{{"SELECT", []string{}, false}})
				}
			}
			overexplainExec(t, p, b, c, "RESET ROLE")
			overexplainExec(t, p, b, c, "GRANT SELECT ON range_src TO range_reader")
			overexplainExec(t, p, b, c, "GRANT UPDATE(v) ON range_src TO range_reader")
			overexplainExec(t, p, b, c, "SET ROLE range_reader")
			e = capture("SELECT tableoid FROM range_src")
			if !b.PG {
				overexplainRangeDemands(t, e, "range_reader", []overexplainRangeDemand{{"SELECT", []string{}, true}, {"SELECT", []string{}, false}})
			}
			e = capture("SELECT id FROM range_src FOR UPDATE")
			if !b.PG {
				overexplainRangeDemands(t, e, "range_reader", []overexplainRangeDemand{{"SELECT", []string{"id"}, false}, {"UPDATE", []string{}, false}})
			}
			// Keep the explaining transaction open: ordinary EXPLAIN must not
			// acquire tuple locks even though its permission demand includes UPDATE.
			overexplainExec(t, p, b, c, "BEGIN")
			capture("SELECT id FROM range_src FOR UPDATE")
			worker, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, worker, b.Recorder, fixture.Select("SELECT id FROM range_src WHERE id=1 FOR UPDATE NOWAIT", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			if err := worker.Close(); err != nil {
				t.Fatal(err)
			}
			overexplainExec(t, p, b, c, "ROLLBACK")
			q := overexplainSQL("JSON", "RANGE_TABLE", "SELECT v FROM range_src")
			held, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			executor := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
			overexplainRange(t, b, overexplainQuery(t, p, executor, q, "JSON", false), "JSON", true)
			overexplainExec(t, p, b, c, "RESET ROLE")
			overexplainExec(t, p, b, c, "REVOKE SELECT ON range_src FROM range_reader")
			overexplainExec(t, p, b, c, "SET ROLE range_reader")
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table range_src"))
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
			overexplainExec(t, p, b, c, "RESET ROLE")
			overexplainExec(t, p, b, c, "GRANT SELECT(v) ON range_src TO range_reader")
			overexplainExec(t, p, b, c, "SET ROLE range_reader")
			overexplainRange(t, b, overexplainQuery(t, p, executor, q, "JSON", false), "JSON", true)
			overexplainExec(t, p, b, c, "RESET ROLE")
			overexplainRangeValues(t, p, b, c)
		})
	})
	t.Run("command-requirements-and-denials", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"range_writer"})
		overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			overexplainRangeSeed(t, p, b, c)
			for _, q := range []string{"ALTER TABLE range_src ADD PRIMARY KEY(id)", "CREATE TABLE range_input(id integer,v text)", "CREATE ROLE range_writer", "GRANT USAGE ON SCHEMA public TO range_writer", "GRANT SELECT ON range_input TO range_writer", "GRANT SELECT,INSERT,UPDATE,DELETE ON range_src TO range_writer", "LOAD 'pg_overexplain'"} {
				overexplainExec(t, p, b, c, q)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO range_input VALUES(1,'input'),(4,NULL)", 2))
			cases := []struct {
				name, child, revoke string
				wants               []overexplainRangeDemand
			}{
				{"insert-returning", "INSERT INTO range_src AS t(id,v) VALUES(4,'new') RETURNING t.id", "INSERT", []overexplainRangeDemand{{"INSERT", []string{"id", "v"}, false}, {"SELECT", []string{"id"}, false}}},
				{"insert-source", "INSERT INTO range_src AS t(id,v) SELECT s.id,s.v FROM range_input s RETURNING t.id", "INSERT", []overexplainRangeDemand{{"INSERT", []string{"id", "v"}, false}, {"SELECT", []string{"id"}, false}}},
				{"conflict", "INSERT INTO range_src AS t(id,v) VALUES(1,'new') ON CONFLICT(id) DO UPDATE SET v=excluded.v WHERE t.v IS NULL RETURNING t.id", "UPDATE", []overexplainRangeDemand{{"INSERT", []string{"id", "v"}, false}, {"SELECT", []string{"id"}, false}, {"UPDATE", []string{"v"}, false}, {"SELECT", []string{"v"}, false}, {"SELECT", []string{"id"}, false}}},
				{"update-from", "UPDATE range_src t SET v=s.v FROM range_input s WHERE t.id=s.id RETURNING t.id", "UPDATE", []overexplainRangeDemand{{"UPDATE", []string{"v"}, false}, {"SELECT", []string{"id"}, false}}},
				{"delete-using", "DELETE FROM range_src t USING range_input s WHERE t.id=s.id RETURNING t.id", "DELETE", []overexplainRangeDemand{{"DELETE", []string{}, true}, {"SELECT", []string{"id"}, false}}},
				{"merge", "MERGE INTO range_src t USING range_input s ON t.id=s.id WHEN MATCHED AND s.v IS NULL THEN DELETE WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT(id,v) VALUES(s.id,s.v)", "DELETE", []overexplainRangeDemand{{"DELETE", []string{}, true}, {"INSERT", []string{"id", "v"}, false}, {"UPDATE", []string{"v"}, false}, {"SELECT", []string{"id"}, false}}},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					overexplainExec(t, p, b, c, "SET ROLE range_writer")
					q := overexplainSQL("JSON", "RANGE_TABLE, VERBOSE, COSTS FALSE", tc.child)
					for _, prepared := range []bool{false, true} {
						r := overexplainQuery(t, p, c, q, "JSON", prepared)
						entries := overexplainRange(t, b, r, "JSON", true)
						e := overexplainRangeNamed(t, b, entries, "relation", "t")
						if !b.PG {
							overexplainRangeRelation(t, p, c, e, "range_src", "JSON")
							if e["Role"] != "target" && e["Role"] != "target/source" {
								t.Fatal("lost real target role", e)
							}
							overexplainRangeDemands(t, e, "range_writer", tc.wants)
						} else if overexplainInteger(t, e["Permission Info Index"], false) <= 0 {
							t.Fatal("missing PG target authority reference", e)
						}
						if tc.name != "insert-returning" && tc.name != "conflict" {
							source := overexplainRangeNamed(t, b, entries, "relation", "s")
							if !b.PG {
								columns := []string{"id", "v"}
								if tc.name == "delete-using" {
									columns = []string{"id"}
								}
								overexplainRangeDemands(t, source, "range_writer", []overexplainRangeDemand{{"SELECT", columns, false}})
							}
						}
					}
					overexplainExec(t, p, b, c, "RESET ROLE")
					overexplainExec(t, p, b, c, "REVOKE "+tc.revoke+" ON range_src FROM range_writer")
					overexplainExec(t, p, b, c, "SET ROLE range_writer")
					overexplainError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table range_src"), "execute", true)
					overexplainExec(t, p, b, c, "RESET ROLE")
					overexplainExec(t, p, b, c, "GRANT "+tc.revoke+" ON range_src TO range_writer")
					overexplainRangeValues(t, p, b, c)
				})
			}
		})
	})
	t.Run("view-effective-principals", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"range_view_owner", "range_view_reader"})
		overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			overexplainRangeSeed(t, p, b, c)
			for _, q := range []string{"CREATE ROLE range_view_owner", "CREATE ROLE range_view_reader", "GRANT USAGE,CREATE ON SCHEMA public TO range_view_owner", "GRANT USAGE ON SCHEMA public TO range_view_reader", "GRANT SELECT,UPDATE ON range_src TO range_view_owner", "CREATE VIEW range_definer AS SELECT id,v FROM range_src", "ALTER VIEW range_definer OWNER TO range_view_owner", "CREATE VIEW range_invoker WITH(security_invoker=true) AS SELECT id,v FROM range_src", "ALTER VIEW range_invoker OWNER TO range_view_owner", "GRANT SELECT,UPDATE ON range_definer,range_invoker TO range_view_reader", "LOAD 'pg_overexplain'", "SET ROLE range_view_reader"} {
				overexplainExec(t, p, b, c, q)
			}
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			check := func(child, principal, privilege string, columns []string) {
				r := overexplainQuery(t, p, c, overexplainSQL("JSON", "RANGE_TABLE, VERBOSE", child), "JSON", true)
				entries := overexplainRange(t, b, r, "JSON", true)
				if b.PG {
					found := false
					for _, e := range entries {
						if e["Relation"] == "public.range_src" && e["Permission Info Index"] != nil {
							found = true
						}
					}
					if !found {
						t.Fatal("PG view expansion lost base permission reference", entries)
					}
					return
				}
				found := false
				for _, e := range entries {
					if e["Kind"] == "relation" && e["Relation Name"] == "range_src" {
						overexplainRangePermission(t, e, principal, privilege, columns, false)
						found = true
					}
				}
				if !found {
					t.Fatal("missing actual view base expansion", entries)
				}
				if privilege == "UPDATE" {
					logical := overexplainRangeNamed(t, b, entries, "relation", "range_definer")
					overexplainRangePermission(t, logical, "range_view_reader", "UPDATE", []string{"v"}, false)
					overexplainRangePermission(t, logical, "range_view_reader", "SELECT", []string{"id"}, false)
				}
			}
			check("SELECT id FROM range_definer", "range_view_owner", "SELECT", []string{"id", "v"})
			check("UPDATE range_definer SET v='changed' WHERE id=1 RETURNING id", "range_view_owner", "UPDATE", []string{"v"})
			q := overexplainSQL("JSON", "RANGE_TABLE", "SELECT id FROM range_invoker")
			overexplainError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table range_src"), "execute", true)
			overexplainExec(t, p, b, c, "RESET ROLE")
			overexplainExec(t, p, b, c, "GRANT SELECT(id) ON range_src TO range_view_reader")
			overexplainExec(t, p, b, c, "SET ROLE range_view_reader")
			// The stored view references both base columns even when its
			// caller projects only id. An id-only base grant is insufficient.
			overexplainError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table range_src"), "execute", true)
			overexplainExec(t, p, b, c, "RESET ROLE")
			overexplainExec(t, p, b, c, "GRANT SELECT(v) ON range_src TO range_view_reader")
			overexplainExec(t, p, b, c, "SET ROLE range_view_reader")
			check("SELECT id FROM range_invoker", "range_view_reader", "SELECT", []string{"id", "v"})
			s := fixture.Select("SELECT id FROM range_invoker ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			overexplainExec(t, p, b, c, "RESET ROLE")
			overexplainRangeValues(t, p, b, c)
		})
	})
}
