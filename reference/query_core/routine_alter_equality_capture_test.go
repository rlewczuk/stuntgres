package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Each query owns an equality occurrence checked before the selected declaration
// changes. Separate views prevent one successful grouping from masking another.
func routineEqualityCaptureCases() []routineBuiltinCaptureCase {
	return []routineBuiltinCaptureCase{
		{name: "distinct", source: "SELECT DISTINCT x AS v FROM (VALUES(1),(1),(2)) s(x)", typ: "INT4", rows: [][]any{{int64(1)}, {int64(2)}}},
		{name: "grouped", source: "SELECT x AS v FROM (VALUES(1),(1),(2)) s(x) GROUP BY x", typ: "INT4", rows: [][]any{{int64(1)}, {int64(2)}}},
		{name: "unioned", source: "SELECT 1 AS v UNION SELECT 1 UNION SELECT 2", typ: "INT4", rows: [][]any{{int64(1)}, {int64(2)}}},
		{name: "intersected", source: "SELECT x AS v FROM (VALUES(1),(1),(2)) s(x) INTERSECT SELECT x FROM (VALUES(1),(3)) s(x)", typ: "INT4", rows: [][]any{{int64(1)}}},
		{name: "excepted", source: "SELECT x AS v FROM (VALUES(1),(1),(2)) s(x) EXCEPT SELECT 2", typ: "INT4", rows: [][]any{{int64(1)}}},
		{name: "partitioned", source: "SELECT sum(y) OVER(PARTITION BY x) AS v FROM (VALUES(1,10),(1,20),(2,7)) s(x,y)", typ: "INT8", rows: [][]any{{int64(7)}, {int64(30)}, {int64(30)}}},
		{name: "peers", source: "SELECT rank() OVER(ORDER BY x) AS v FROM (VALUES(1),(1),(2)) s(x)", typ: "INT8", rows: [][]any{{int64(1)}, {int64(1)}, {int64(3)}}},
	}
}

func routineEqualityCaptureCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range routineEqualityCaptureCases() {
		routineExec(t, p, b, c, "CREATE VIEW ra_eq_"+tc.name+" AS "+tc.source)
	}
	// Operator binding exposes the selected equality provider without requiring
	// an unsupported ordinary scalar gateway for the builtin implementation.
	// Its original lifetime differs from a freshly recreated same-name function.
	routineExec(t, p, b, c, "CREATE VIEW ra_eq_original AS SELECT x=y AS v FROM (VALUES(1,1)) s(x,y)")
	routineExec(t, p, b, c,
		"CREATE TABLE ra_eq_rule_source(v integer)",
		"CREATE TABLE ra_eq_rule_input(x integer)",
		"CREATE TABLE ra_eq_rule_log(v integer)",
		"CREATE RULE ra_eq_action AS ON INSERT TO ra_eq_rule_source DO ALSO INSERT INTO ra_eq_rule_log SELECT DISTINCT NEW.v+x FROM ra_eq_rule_input",
	)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_eq_rule_input VALUES(1),(1),(2)", 3))
}

func routineEqualityCaptureCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range routineEqualityCaptureCases() {
		routineBuiltinCaptureCheck(t, p, b, c, "SELECT v FROM ra_eq_"+tc.name+" ORDER BY v", tc)
	}
	routineAlterRows(t, p, b, c, "SELECT v FROM ra_eq_original", []string{"v"}, []string{"BOOL"}, [][]any{{true}})
	routineEqualityRuleCheck(t, p, b, c, "INSERT INTO ra_eq_rule_source VALUES(10)", [][]any{{int64(11)}, {int64(12)}})
	// This fresh operator uses the SAME selected routine address at its current
	// source revision; the rule's grouping still owns its original older stamp.
	routineEqualityRuleCheck(t, p, b, c, "INSERT INTO ra_eq_rule_source SELECT CASE WHEN x=x THEN 20 ELSE 10 END FROM (VALUES(1)) s(x)", [][]any{{int64(21)}, {int64(22)}})
}

func routineEqualityRuleCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, rows [][]any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		insert := fixture.Exec(query, 1)
		insert.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, b.Recorder, insert)
		routineAlterRows(t, p, b, c, "SELECT v FROM ra_eq_rule_log ORDER BY v", []string{"v"}, []string{"INT4"}, rows)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ra_eq_rule_log", 2))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ra_eq_rule_source", 1))
	}
}

func TestReferenceRoutineAlterEqualityCaptures(t *testing.T) {
	for _, transition := range []string{"rename", "move", "owner"} {
		t.Run(transition, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"ra_eq_owner", "ra_eq_reader"})
			fixture.SchemaReferenceCleanup(t, "ra_eq_space")
			routineBuiltinCaptureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				restore := routineAlterBuiltinCleanup(t, p, c, "pg_catalog.int4eq(integer,integer)")
				defer restore()
				routineExec(t, p, b, c, "CREATE ROLE ra_eq_owner", "CREATE ROLE ra_eq_reader", "CREATE SCHEMA ra_eq_space AUTHORIZATION ra_eq_owner", "GRANT USAGE ON SCHEMA public,ra_eq_space TO ra_eq_reader")
				routineEqualityCaptureCreate(t, p, b, c)
				routineEqualityCaptureCheck(t, p, b, c)
				current := "pg_catalog.int4eq(integer,integer)"
				label := "int4eq"
				switch transition {
				case "rename":
					routineExec(t, p, b, c, "ALTER FUNCTION "+current+" RENAME TO ra_eq_selected")
					current, label = "pg_catalog.ra_eq_selected(integer,integer)", "ra_eq_selected"
				case "move":
					routineExec(t, p, b, c, "ALTER FUNCTION "+current+" SET SCHEMA ra_eq_space")
					current = "ra_eq_space.int4eq(integer,integer)"
				case "owner":
					routineExec(t, p, b, c, "ALTER FUNCTION "+current+" OWNER TO ra_eq_owner")
				}
				routineEqualityCaptureCheck(t, p, b, c)
				if transition != "owner" {
					routineExec(t, p, b, c, "CREATE FUNCTION pg_catalog.int4eq(integer,integer) RETURNS boolean RETURN false")
					defer func() {
						if _, err := c.ExecContext(p.Ctx, "DROP FUNCTION pg_catalog.int4eq(integer,integer)"); err != nil {
							t.Error("drop fresh same-name declaration", err)
						}
					}()
					routineAlterRows(t, p, b, c, "SELECT pg_catalog.int4eq(1,1) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}})
					routineEqualityRuleCheck(t, p, b, c, "INSERT INTO ra_eq_rule_source VALUES(CASE WHEN pg_catalog.int4eq(1,1) THEN 10 ELSE 20 END)", [][]any{{int64(21)}, {int64(22)}})
					routineEqualityCaptureCheck(t, p, b, c)
				}
				// Implicit grouping helpers need not implement PostgreSQL's explicit
				// expression ACL gateway. Probe the actual selected declaration through
				// a nonconstant operator expression, not its unsupported scalar name.
				query := "SELECT x=y AS v FROM (VALUES(1,1)) s(x,y)"
				defer func() {
					for _, q := range []string{"RESET ROLE", "GRANT EXECUTE ON FUNCTION " + current + " TO PUBLIC", "REVOKE EXECUTE ON FUNCTION " + current + " FROM ra_eq_reader"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error("restore equality EXECUTE", err)
						}
					}
				}()
				routineExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION "+current+" FROM PUBLIC", "SET ROLE ra_eq_reader")
				denied := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+label))
				denied.SelectRows, denied.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, denied)
				routineExec(t, p, b, c, "RESET ROLE", "GRANT EXECUTE ON FUNCTION "+current+" TO ra_eq_reader", "SET ROLE ra_eq_reader")
				routineAlterRows(t, p, b, c, query, []string{"v"}, []string{"BOOL"}, [][]any{{true}})
				routineExec(t, p, b, c, "RESET ROLE")
				routineEqualityCaptureCheck(t, p, b, c)
			})
		})
	}
}

func TestReferenceRoutineAlterEqualityCapturePersistence(t *testing.T) {
	routineBuiltinCaptureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		restore := routineAlterBuiltinCleanup(t, p, c, "pg_catalog.int4eq(integer,integer)")
		defer restore()
		routineEqualityCaptureCreate(t, p, b, c)
		routineExec(t, p, b, c, "ALTER FUNCTION pg_catalog.int4eq(integer,integer) RENAME TO ra_eq_selected")
		routineEqualityCaptureCheck(t, p, b, c)
		if b.PG {
			return
		}
		for range 2 {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			routineEqualityCaptureCheck(t, p, b, c)
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/fresh")
			if err != nil {
				t.Fatal(err)
			}
			func() {
				fresh := sql.OpenDB(connector)
				defer fresh.Close()
				if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				recorded := sql.OpenDB(fixture.MigrationRecordedConnector(t, connector))
				defer recorded.Close()
				fc, err := recorded.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer fc.Close()
				routineEqualityCaptureCheck(t, p, fixture.Backend{Recorder: &fixture.Recorder{RawBackend: b.Recorder.RawBackend}}, fc)
			}()
			if p.Postgres != nil {
				func() {
					pg, err := p.Postgres.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer pg.Close()
					cleanup := routineAlterBuiltinCleanup(t, p, pg, "pg_catalog.int4eq(integer,integer)")
					defer cleanup()
					defer func() {
						for _, tc := range routineEqualityCaptureCases() {
							if _, err := pg.ExecContext(p.Ctx, "DROP VIEW IF EXISTS ra_eq_"+tc.name); err != nil {
								t.Error(err)
							}
						}
						if _, err := pg.ExecContext(p.Ctx, "DROP VIEW IF EXISTS ra_eq_original"); err != nil {
							t.Error(err)
						}
						if _, err := pg.ExecContext(p.Ctx, "DROP TABLE IF EXISTS ra_eq_rule_source,ra_eq_rule_input,ra_eq_rule_log CASCADE"); err != nil {
							t.Error(err)
						}
					}()
					if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
						t.Fatal("actual equality capture stream replay", err)
					}
					routineEqualityCaptureCheck(t, p, fixture.Backend{PG: true, Recorder: p.PostgresRecord}, pg)
				}()
			}
		}
	})
}
