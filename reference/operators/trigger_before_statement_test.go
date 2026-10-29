package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// The prepared route retains each handle until the scenario ends. Setup and
// observations stay on the same session, but only the intended SQL is routed.
func beforeStatementRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step))) {
	t.Helper()
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		held := make(map[string]*sql.Stmt)
		defer func() {
			for _, s := range held {
				s.Close()
			}
		}()
		run := func(step fixture.Step) {
			var x fixture.Executor = c
			if prepared {
				s := held[step.Query]
				if s == nil {
					var err error
					s, err = c.PrepareContext(p.Ctx, step.Query)
					if err != nil {
						t.Fatal(err)
					}
					held[step.Query] = s
				}
				x = fixture.HeldStatement{Executor: c, Statement: s, SQL: step.Query}
			}
			fixture.RunStep(t, p.Ctx, x, b.Recorder, step)
		}
		body(t, p, b, c, run)
	})
}

func beforeStatementSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE TABLE bst(id integer PRIMARY KEY,v integer,u integer DEFAULT 0,g integer GENERATED ALWAYS AS(v*2) STORED)",
		"CREATE TABLE bst_log(seq integer GENERATED ALWAYS AS IDENTITY,name text,op text,level text,timing text,rel text,ns text,rel_ok boolean,nargs integer,arg0 text,arg1 text,old_id integer,new_id integer,n bigint)",
		"CREATE FUNCTION bst_log() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO bst_log(name,op,level,timing,rel,ns,rel_ok,nargs,arg0,arg1,old_id,new_id,n) VALUES(TG_NAME,TG_OP,TG_LEVEL,TG_WHEN,TG_TABLE_NAME,TG_TABLE_SCHEMA,TG_RELID=(TG_TABLE_SCHEMA||'.'||TG_TABLE_NAME)::regclass::oid,TG_NARGS,TG_ARGV[0],TG_ARGV[1],OLD.id,NEW.id,(SELECT count(*) FROM bst)); RETURN NULL; END$$")
}
func beforeStatementCount(table string, n int64) fixture.Step {
	return fixture.Select("SELECT count(*) AS n FROM "+table, []string{"n"}, []string{"INT8"}, [][]any{{n}})
}
func beforeStatementLog(rows ...[]any) fixture.Step {
	return fixture.Select("SELECT name,op,n FROM bst_log ORDER BY seq", []string{"name", "op", "n"}, []string{"TEXT", "TEXT", "INT8"}, rows)
}
func beforeStatementChain(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, events string) {
	t.Helper()
	for _, name := range []string{"z", "a"} {
		userOperatorExec(t, p, b, c, "CREATE TRIGGER "+name+" BEFORE "+events+" ON bst FOR EACH STATEMENT EXECUTE FUNCTION bst_log('x''y','')")
	}
}
func TestReferenceTriggerBeforeStatementDefinitions(t *testing.T) {
	beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		beforeStatementSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE TRIGGER changed BEFORE INSERT ON bst EXECUTE FUNCTION bst_log('old')")
		run(fixture.Exec("INSERT INTO bst(id,v) VALUES($1,$2)", 1, 1, 10))
		userOperatorExec(t, p, b, c, "BEGIN", "CREATE OR REPLACE TRIGGER changed BEFORE UPDATE OF v ON bst WHEN(true) EXECUTE FUNCTION bst_log('private')")
		run(fixture.Exec("UPDATE bst SET v=v WHERE id=$1", 0, 999))
		userOperatorExec(t, p, b, c, "ROLLBACK")
		run(fixture.Exec("INSERT INTO bst(id,v) VALUES($1,$2)", 1, 2, 20))
		run(fixture.Select("SELECT op,arg0 FROM bst_log ORDER BY seq", []string{"op", "arg0"}, []string{"TEXT", "TEXT"}, [][]any{{"INSERT", "old"}, {"INSERT", "old"}}))
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER changed BEFORE UPDATE OF v ON bst WHEN(true) EXECUTE FUNCTION bst_log('new')")
		run(fixture.Exec("UPDATE bst SET v=v WHERE id=$1", 0, 998))
		run(fixture.Select("SELECT op,arg0 FROM bst_log ORDER BY seq", []string{"op", "arg0"}, []string{"TEXT", "TEXT"}, [][]any{{"INSERT", "old"}, {"INSERT", "old"}, {"UPDATE", "new"}}))
	})
}
func TestReferenceTriggerBeforeStatementDiagnostics(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		beforeStatementSetup(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.ErrorDiagnostic("CREATE TRIGGER bad BEFORE TRUNCATE ON bst FOR EACH ROW EXECUTE FUNCTION bst_log()", fixture.ControlsReferenceDiagnostic("0A000", "TRUNCATE FOR EACH ROW triggers are not supported")))
		userOperatorExec(t, p, b, c, "CREATE VIEW bst_view AS SELECT id,v FROM bst")
		d := fixture.ControlsReferenceDiagnostic("42809", `"bst_view" is a view`)
		d.Detail = "Views cannot have TRUNCATE triggers."
		run(fixture.ErrorDiagnostic("CREATE TRIGGER bad BEFORE TRUNCATE ON bst_view EXECUTE FUNCTION bst_log()", d))
		for _, ref := range []string{"OLD.id>0", "NEW.id>0", "NEW IS NULL"} {
			q := "/* é */ CREATE TRIGGER bad BEFORE UPDATE ON bst WHEN(" + ref + ") EXECUTE FUNCTION bst_log()"
			d := fixture.ControlsReferenceDiagnostic("42P17", "statement trigger's WHEN condition cannot reference column values")
			i := strings.Index(q, "NEW")
			if i < 0 {
				i = strings.Index(q, "OLD")
			}
			d.Position = fixture.Position(utf8.RuneCountInString(q[:i]) + 1)
			run(fixture.ErrorDiagnostic(q, d))
		}
		q := "CREATE TRIGGER bad BEFORE INSERT ON bst WHEN((SELECT true)) EXECUTE FUNCTION bst_log()"
		d = fixture.ControlsReferenceDiagnostic("0A000", "cannot use subquery in trigger WHEN condition")
		d.Position = fixture.Position(strings.Index(q, "(SELECT") + 1)
		run(fixture.ErrorDiagnostic(q, d))
		d = fixture.ControlsReferenceDiagnostic("42809", `"bst_view" is a view`)
		d.Detail = "Views cannot have row-level BEFORE or AFTER triggers."
		run(fixture.ErrorDiagnostic("CREATE TRIGGER bad BEFORE INSERT ON bst_view FOR EACH ROW EXECUTE FUNCTION bst_log()", d))
	})
}
func TestReferenceTriggerBeforeStatementFrameOrder(t *testing.T) {
	t.Run("row_boundary", beforeStatementRowBoundary)
	beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		beforeStatementSetup(t, p, b, c)
		run(fixture.Exec("INSERT INTO bst(id,v) VALUES(1,10),(2,20)", 2))
		beforeStatementChain(t, p, b, c, "INSERT OR UPDATE OR DELETE")
		q := "UPDATE bst SET v=v WHERE id=$1"
		h, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer h.Close()
		run(beforeStatementCount("bst_log", 0))
		for _, id := range []int{998, 999} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: h, SQL: q}, b.Recorder, fixture.Exec(q, 0, id))
		}
		userOperatorExec(t, p, b, c, "TRUNCATE bst_log")
		run(fixture.Exec("INSERT INTO bst(id,v) SELECT 9,90 WHERE false", 0))
		run(fixture.Exec("UPDATE bst SET v=v WHERE false", 0))
		run(fixture.Exec("DELETE FROM bst WHERE false", 0))
		run(fixture.Exec("INSERT INTO bst(id,v) VALUES(3,30),(4,40)", 2))
		run(fixture.Exec("UPDATE bst SET v=v+1 WHERE id<=2", 2))
		run(fixture.Exec("DELETE FROM bst WHERE id<=2", 2))
		var want [][]any
		for i, op := range []string{"INSERT", "UPDATE", "DELETE", "INSERT", "UPDATE", "DELETE"} {
			n := int64(2)
			if i >= 4 {
				n = 4
			}
			for _, name := range []string{"a", "z"} {
				want = append(want, []any{name, op, n})
			}
		}
		run(beforeStatementLog(want...))
		run(fixture.Select("SELECT DISTINCT level,timing,rel,ns,rel_ok,nargs,arg0,arg1,old_id,new_id FROM bst_log", []string{"level", "timing", "rel", "ns", "rel_ok", "nargs", "arg0", "arg1", "old_id", "new_id"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "INT4", "TEXT", "TEXT", "INT4", "INT4"}, [][]any{{"STATEMENT", "BEFORE", "bst", "public", true, int64(2), "x'y", "", nil, nil}}))
	})
}
func TestReferenceTriggerBeforeStatementEligibility(t *testing.T) {
	beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		beforeStatementSetup(t, p, b, c)
		userOperatorExec(t, p, b, c,
			"CREATE TABLE bst_eval(n bigint)",
			"CREATE FUNCTION bst_condition() RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO bst_eval SELECT count(*) FROM bst_log; RETURN true; END$$",
			"CREATE FUNCTION bst_condition_fail() RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$BEGIN PERFORM 1/0; RETURN true; END$$",
			"CREATE TRIGGER a BEFORE INSERT ON bst WHEN(true) EXECUTE FUNCTION bst_log()",
			"CREATE TRIGGER b BEFORE INSERT ON bst WHEN(bst_condition()) EXECUTE FUNCTION bst_log()",
			"CREATE TRIGGER f BEFORE INSERT ON bst WHEN(false) EXECUTE FUNCTION bst_log()",
			"CREATE TRIGGER n BEFORE INSERT ON bst WHEN(NULL) EXECUTE FUNCTION bst_log()",
			"CREATE TRIGGER disabled BEFORE INSERT ON bst WHEN(bst_condition_fail()) EXECUTE FUNCTION bst_log()",
			"ALTER TABLE bst DISABLE TRIGGER disabled",
			"CREATE TRIGGER mismatch BEFORE UPDATE OF u ON bst WHEN(bst_condition_fail()) EXECUTE FUNCTION bst_log()")
		run(fixture.Exec("INSERT INTO bst(id,v) VALUES($1,10)", 1, 1))
		run(fixture.Exec("INSERT INTO bst(id,v) VALUES($1,10)", 1, 2))
		run(fixture.Select("SELECT n FROM bst_eval ORDER BY n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(3)}}))
		run(beforeStatementLog([]any{"a", "INSERT", int64(0)}, []any{"b", "INSERT", int64(0)}, []any{"a", "INSERT", int64(1)}, []any{"b", "INSERT", int64(1)}))
		run(fixture.Exec("UPDATE bst SET v=v", 2))
		userOperatorExec(t, p, b, c, "ALTER TABLE bst ENABLE TRIGGER disabled")
		d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
		d.Context = "SQL statement \"SELECT 1/0\"\nPL/pgSQL function bst_condition_fail() line 1 at PERFORM"
		run(fixture.ErrorDiagnostic("INSERT INTO bst(id,v) VALUES(3,30)", d))
		run(beforeStatementCount("bst", 2))
		run(beforeStatementCount("bst_log", 4))
		run(beforeStatementCount("bst_eval", 2))
	})
}
func TestReferenceTriggerBeforeStatementUpdateOf(t *testing.T) {
	beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		beforeStatementSetup(t, p, b, c)
		run(fixture.Exec("INSERT INTO bst(id,v) VALUES(1,10)", 1))
		userOperatorExec(t, p, b, c, "CREATE TRIGGER v BEFORE UPDATE OF v ON bst EXECUTE FUNCTION bst_log()", "CREATE TRIGGER g BEFORE UPDATE OF g ON bst EXECUTE FUNCTION bst_log()")
		run(fixture.Exec("UPDATE bst SET u=u WHERE id=$1", 0, 99))
		run(fixture.Exec("UPDATE bst SET v=v WHERE id=$1", 0, 99))
		run(fixture.Exec("UPDATE bst SET v=v WHERE id=$1", 1, 1))
		run(beforeStatementLog([]any{"g", "UPDATE", int64(1)}, []any{"v", "UPDATE", int64(1)}, []any{"g", "UPDATE", int64(1)}, []any{"v", "UPDATE", int64(1)}))
	})
}
func TestReferenceTriggerBeforeStatementModesAuthority(t *testing.T) {
	t.Run("authority", beforeStatementAuthority)
	beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		beforeStatementSetup(t, p, b, c)
		for _, name := range []string{"disabled", "origin", "replica", "always"} {
			userOperatorExec(t, p, b, c, "CREATE TRIGGER "+name+" BEFORE INSERT ON bst EXECUTE FUNCTION bst_log()")
		}
		userOperatorExec(t, p, b, c, "ALTER TABLE bst DISABLE TRIGGER disabled", "ALTER TABLE bst ENABLE REPLICA TRIGGER replica", "ALTER TABLE bst ENABLE ALWAYS TRIGGER always")
		defer c.ExecContext(p.Ctx, "SET session_replication_role=origin")
		for i, role := range []string{"origin", "local", "replica"} {
			userOperatorExec(t, p, b, c, "SET session_replication_role="+role)
			run(fixture.Exec("INSERT INTO bst(id,v) VALUES($1,10)", 1, i+1))
		}
		run(beforeStatementLog([]any{"always", "INSERT", int64(0)}, []any{"origin", "INSERT", int64(0)}, []any{"always", "INSERT", int64(1)}, []any{"origin", "INSERT", int64(1)}, []any{"always", "INSERT", int64(2)}, []any{"replica", "INSERT", int64(2)}))
	})
}
