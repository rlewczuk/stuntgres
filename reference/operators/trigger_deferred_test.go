package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// Expectations in this family are independently derived from PostgreSQL 18's
// contract. In particular diagnostic positions/context and catalog/deparse text
// are source-derived predictions until the pinned oracle run records them.
func deferredSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE TABLE d(id integer PRIMARY KEY,v integer,u integer DEFAULT 0)",
		"CREATE TABLE audit(op text,id integer,ov integer,v integer,n bigint,label text)",
		"CREATE FUNCTION d_log() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES(TG_OP,COALESCE(NEW.id,OLD.id),OLD.v,NEW.v,(SELECT count(*) FROM d),TG_NAME); IF TG_OP<>'DELETE' THEN NEW.v:=999; END IF; RETURN NULL; END$$")
}
func deferredCreate(name, event, properties string) string {
	return "CREATE CONSTRAINT TRIGGER " + name + " AFTER " + event + " ON d " + properties + " FOR EACH ROW EXECUTE FUNCTION d_log()"
}
func deferredCount(table string, n int64) fixture.Step {
	return fixture.Select("SELECT count(*) AS n FROM "+table, []string{"n"}, []string{"INT8"}, [][]any{{n}})
}
func deferredAudit(rows ...[]any) fixture.Step {
	return fixture.Select("SELECT op,id,ov,v,n,label FROM audit ORDER BY id,op,label", []string{"op", "id", "ov", "v", "n", "label"}, []string{"TEXT", "INT4", "INT4", "INT4", "INT8", "TEXT"}, rows)
}
func deferredFailure(q, function string) fixture.Step {
	d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
	d.Context = "SQL statement \"SELECT 1/0\"\nPL/pgSQL function " + function + "() line 1 at PERFORM"
	return fixture.ErrorDiagnostic(q, d)
}
func deferredMissing(q, name string) fixture.Step {
	return fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42704", "constraint \""+name+"\" does not exist"))
}
func deferredAborted(q string) fixture.Step {
	return fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
}

func TestReferenceTriggerDeferredDefinition(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		deferredSetup(t, p, b, c)
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		for _, tc := range []struct {
			name, event, properties string
			def, initial            bool
		}{
			{"plain", "INSERT OR UPDATE OR DELETE", "", false, false},
			{"def", "UPDATE OF v", "DEFERRABLE", true, false},
			{"initial", "INSERT", "INITIALLY DEFERRED", true, true},
			{"redundant", "DELETE", "DEFERRABLE DEFERRABLE INITIALLY DEFERRED INITIALLY DEFERRED", true, true},
			{"notdef", "INSERT", "NOT DEFERRABLE NOT DEFERRABLE INITIALLY IMMEDIATE INITIALLY IMMEDIATE", false, false},
		} {
			q := deferredCreate(tc.name, tc.event, tc.properties)
			h, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT count(*) AS n FROM pg_trigger WHERE tgrelid='d'::regclass AND tgname='"+tc.name+"'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: h, SQL: q}, b.Recorder, fixture.Exec(q, 0))
			if err := h.Close(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT tgdeferrable,tginitdeferred FROM pg_trigger WHERE tgrelid='d'::regclass AND tgname='"+tc.name+"'", []string{"tgdeferrable", "tginitdeferred"}, []string{"BOOL", "BOOL"}, [][]any{{tc.def, tc.initial}}))
		}
		for _, tc := range []struct{ q, token string }{
			{"CREATE CONSTRAINT TRIGGER bad BEFORE INSERT ON d FOR EACH ROW EXECUTE FUNCTION d_log()", "BEFORE"},
			{"CREATE CONSTRAINT TRIGGER bad INSTEAD OF INSERT ON d FOR EACH ROW EXECUTE FUNCTION d_log()", "INSTEAD"},
			{"CREATE CONSTRAINT TRIGGER bad AFTER INSERT ON d FOR ROW EXECUTE FUNCTION d_log()", "ROW"},
			{"CREATE CONSTRAINT TRIGGER bad AFTER INSERT ON d FOR EACH STATEMENT EXECUTE FUNCTION d_log()", "STATEMENT"},
			{"CREATE CONSTRAINT TRIGGER bad AFTER INSERT ON d EXECUTE FUNCTION d_log()", "EXECUTE"},
			{"CREATE CONSTRAINT TRIGGER bad AFTER INSERT ON d FOR EACH", ""},
			{deferredCreate("bad", "INSERT", "DEFERRABLE") + " garbage", "garbage"},
			{"CREATE CONSTRAINT TRIGGER bad \"AFTER\" INSERT ON d FOR EACH ROW EXECUTE FUNCTION d_log()", "\"AFTER\""},
			{"CREATE CONSTRAINT TRIGGER bad AFTER INSERT ON d FOR \"EACH\" ROW EXECUTE FUNCTION d_log()", "\"EACH\""},
			{"CREATE CONSTRAINT TRIGGER bad AFTER INSERT ON d FOR EACH \"ROW\" EXECUTE FUNCTION d_log()", "\"ROW\""},
		} {
			q := "/* é */ " + tc.q
			message := "syntax error at end of input"
			pos := utf8.RuneCountInString(q) + 1
			if tc.token != "" {
				message = "syntax error at or near \"" + tc.token + "\""
				pos = utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1
			}
			d := fixture.ControlsReferenceDiagnostic("42601", message)
			d.Position = fixture.Position(pos)
			run(fixture.ErrorDiagnostic(q, d))
		}
		for _, tc := range []struct{ properties, message, token string }{
			{"DEFERRABLE NOT DEFERRABLE", "conflicting constraint properties", "NOT"},
			{"INITIALLY IMMEDIATE INITIALLY DEFERRED", "conflicting constraint properties", "INITIALLY DEFERRED"},
			{"NOT DEFERRABLE INITIALLY DEFERRED", "constraint declared INITIALLY DEFERRED must be DEFERRABLE", "INITIALLY"},
		} {
			q := deferredCreate("bad", "INSERT", tc.properties)
			d := fixture.ControlsReferenceDiagnostic("42601", tc.message)
			d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1)
			run(fixture.ErrorDiagnostic(q, d))
		}
		replacement := fixture.ControlsReferenceDiagnostic("0A000", "CREATE OR REPLACE CONSTRAINT TRIGGER is not supported")
		replacement.Position = "1" // Observed on pinned PostgreSQL18.6.
		run(fixture.ErrorDiagnostic("CREATE OR REPLACE CONSTRAINT TRIGGER bad AFTER INSERT ON d FOR EACH ROW EXECUTE FUNCTION d_log()", replacement))
		run(fixture.ErrorDiagnostic("CREATE OR REPLACE TRIGGER plain AFTER INSERT ON d FOR EACH ROW EXECUTE FUNCTION d_log()", fixture.ControlsReferenceDiagnostic("42710", `trigger "plain" for relation "d" is a constraint trigger`)))
		run(fixture.ErrorDiagnostic(deferredCreate("bad", "TRUNCATE", ""), fixture.ControlsReferenceDiagnostic("0A000", "TRUNCATE FOR EACH ROW triggers are not supported")))
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS d_fdw CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		userOperatorExec(t, p, b, c, "CREATE VIEW d_view AS SELECT id,v FROM d", "CREATE FOREIGN DATA WRAPPER d_fdw", "CREATE SERVER d_server FOREIGN DATA WRAPPER d_fdw", "CREATE FOREIGN TABLE d_foreign(id integer,v integer) SERVER d_server")
		for _, tc := range []struct{ table, kind, detail string }{
			{"d_view", "view", "Views cannot have row-level BEFORE or AFTER triggers."},
			{"d_foreign", "foreign table", "Foreign tables cannot have constraint triggers."},
		} {
			d := fixture.ControlsReferenceDiagnostic("42809", `"`+tc.table+`" is a `+tc.kind)
			d.Detail = tc.detail
			run(fixture.ErrorDiagnostic(strings.Replace(deferredCreate("bad", "INSERT", ""), " ON d ", " ON "+tc.table+" ", 1), d))
		}
		run(fixture.Select("SELECT count(*) AS n FROM pg_trigger WHERE tgrelid='d'::regclass AND tgname='bad'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
	t.Run("constraint-name-collisions", deferredConstraintNameCollisions)
}

func TestReferenceTriggerDeferredTiming(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		deferredSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, deferredCreate("c", "INSERT OR UPDATE OR DELETE", "DEFERRABLE INITIALLY DEFERRED"))
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		q := "INSERT INTO d(id,v) VALUES($1,$2) RETURNING id,v"
		h, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer h.Close()
		run(deferredCount("audit", 0))
		run(deferredCount("d", 0))
		run(fixture.Exec("BEGIN", 0))
		for _, id := range []int64{1, 2} {
			s := triggerRows(q, []any{id, id * 10})
			s.Args = []any{id, id * 10}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: h, SQL: q}, b.Recorder, s)
			run(deferredCount("audit", 0))
		}
		run(fixture.Exec("COMMIT", 0))
		run(deferredAudit([]any{"INSERT", int64(1), nil, int64(10), int64(2), "c"}, []any{"INSERT", int64(2), nil, int64(20), int64(2), "c"}))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(3,30)", 1))
		run(deferredCount("audit", 3))
		run(triggerRows("UPDATE d SET v=v WHERE id=999 RETURNING id,v"))
		run(fixture.Exec("DELETE FROM d WHERE id=999", 0))
		run(deferredCount("audit", 3))
		userOperatorExec(t, p, b, c, "DROP TRIGGER c ON d", "TRUNCATE audit,d", deferredCreate("c", "INSERT", "DEFERRABLE INITIALLY IMMEDIATE"))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(4,40),(5,50)", 2))
		run(deferredAudit([]any{"INSERT", int64(4), nil, int64(40), int64(2), "c"}, []any{"INSERT", int64(5), nil, int64(50), int64(2), "c"}))
		run(fixture.Exec("ROLLBACK", 0))
		userOperatorExec(t, p, b, c, "DROP TRIGGER c ON d", "CREATE TABLE ordering(seq integer GENERATED ALWAYS AS IDENTITY,label text)", "CREATE FUNCTION d_order() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO ordering(label) VALUES(TG_NAME); RETURN NULL; END$$", "CREATE CONSTRAINT TRIGGER a AFTER INSERT ON d FOR EACH ROW EXECUTE FUNCTION d_order()", "CREATE TRIGGER z AFTER INSERT ON d FOR EACH ROW EXECUTE FUNCTION d_order()", "CREATE TRIGGER zz AFTER INSERT ON d EXECUTE FUNCTION d_order()")
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(6,60)", 1))
		run(fixture.Select("SELECT label FROM ordering ORDER BY seq", []string{"label"}, []string{"TEXT"}, [][]any{{"a"}, {"z"}, {"zz"}}))
	})
	deferredTerminalControls(t)
}

func TestReferenceTriggerDeferredModes(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		deferredSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, deferredCreate("c", "INSERT", "DEFERRABLE INITIALLY DEFERRED"))
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(1,10)", 1))
		run(deferredCount("audit", 0))
		run(fixture.Exec("SET CONSTRAINTS c IMMEDIATE", 0))
		run(deferredCount("audit", 1))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(2,20)", 1))
		run(deferredCount("audit", 2))
		run(fixture.Exec("SET CONSTRAINTS c DEFERRED", 0))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(3,30)", 1))
		run(deferredCount("audit", 2))
		run(fixture.Exec("COMMIT", 0))
		run(deferredAudit([]any{"INSERT", int64(1), nil, int64(10), int64(1), "c"}, []any{"INSERT", int64(2), nil, int64(20), int64(2), "c"}, []any{"INSERT", int64(3), nil, int64(30), int64(3), "c"}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(4,40)", 1))
		run(deferredCount("audit", 3))
		run(fixture.Exec("SET CONSTRAINTS c IMMEDIATE", 0))
		run(deferredCount("audit", 4))
		run(fixture.Exec("SET CONSTRAINTS ALL DEFERRED", 0))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(5,50)", 1))
		run(deferredCount("audit", 4))
		run(fixture.Exec("SET CONSTRAINTS c IMMEDIATE", 0))
		run(deferredCount("audit", 5))
		run(fixture.Exec("SET CONSTRAINTS ALL DEFERRED", 0))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(6,60)", 1))
		run(deferredCount("audit", 5))
		run(fixture.Exec("ROLLBACK", 0))
		userOperatorExec(t, p, b, c, deferredCreate("fixed", "INSERT", "NOT DEFERRABLE"))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET CONSTRAINTS fixed IMMEDIATE", 0))
		run(fixture.Exec("SAVEPOINT s", 0))
		run(fixture.ErrorDiagnostic("SET CONSTRAINTS fixed DEFERRED", fixture.ControlsReferenceDiagnostic("42809", `constraint "fixed" is not deferrable`)))
		run(deferredAborted("SELECT 1"))
		run(fixture.Exec("ROLLBACK TO s", 0))
		run(fixture.Exec("ROLLBACK", 0))
	})
	t.Run("outside-block-warning", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
			notices := new([]fixture.Diagnostic)
			_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, notices)
			deferredSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, deferredCreate("c", "INSERT", "DEFERRABLE INITIALLY DEFERRED"))
			for _, prepared := range []bool{false, true} {
				*notices = nil
				s := fixture.Exec("SET CONSTRAINTS c IMMEDIATE", 0)
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, c, rec, s)
				want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET CONSTRAINTS can only be used in transaction blocks"}
				if len(*notices) != 1 || (*notices)[0] != want {
					t.Fatalf("SET warning got %#v want %#v", *notices, want)
				}
			}
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("INSERT INTO d(id,v) VALUES(1,10)", 1))
			fixture.RunStep(t, p.Ctx, c, rec, deferredCount("audit", 0))
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("ROLLBACK", 0))
		})
	})
}

func TestReferenceTriggerDeferredWhen(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		deferredSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE TABLE qualification(id integer)", "CREATE TABLE switch(enabled boolean)", "CREATE FUNCTION d_when(integer) RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO qualification VALUES($1); IF NOT (SELECT enabled FROM switch) THEN RETURN false; END IF; IF $1=1 THEN RETURN true; ELSIF $1=2 THEN RETURN false; END IF; RETURN NULL; END$$", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON d DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN(d_when(NEW.id)) EXECUTE FUNCTION d_log()")
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		run(fixture.Exec("INSERT INTO switch VALUES(true)", 1))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(1,10),(2,20),(3,30)", 3))
		run(fixture.Select("SELECT id FROM qualification ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
		run(deferredCount("audit", 0))
		run(fixture.Exec("UPDATE switch SET enabled=false", 1))
		run(fixture.Exec("UPDATE d SET v=77", 3))
		run(fixture.Exec("COMMIT", 0))
		run(deferredAudit([]any{"INSERT", int64(1), nil, int64(10), int64(3), "c"}))
		run(deferredCount("qualification", 3))
		userOperatorExec(t, p, b, c, "DROP TRIGGER c ON d", "TRUNCATE d,audit", deferredCreate("c", "INSERT OR UPDATE OR DELETE", "DEFERRABLE INITIALLY DEFERRED"))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(8,80)", 1))
		run(fixture.Exec("UPDATE d SET v=81 WHERE id=8", 1))
		run(fixture.Exec("DELETE FROM d WHERE id=8", 1))
		run(deferredCount("audit", 0))
		run(fixture.Exec("COMMIT", 0))
		run(deferredAudit([]any{"DELETE", int64(8), int64(81), nil, int64(0), "c"}, []any{"INSERT", int64(8), nil, int64(80), int64(0), "c"}, []any{"UPDATE", int64(8), int64(80), int64(81), int64(0), "c"}))
		userOperatorExec(t, p, b, c, "DROP TRIGGER c ON d", "TRUNCATE audit", deferredCreate("c", "UPDATE OF v", "DEFERRABLE INITIALLY DEFERRED"))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(9,90)", 1))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("UPDATE d SET u=u", 1))
		run(fixture.Exec("UPDATE d SET v=v", 1))
		run(deferredCount("audit", 0))
		run(fixture.Exec("COMMIT", 0))
		run(deferredAudit([]any{"UPDATE", int64(9), int64(90), int64(90), int64(1), "c"}))
		userOperatorExec(t, p, b, c, "DROP TRIGGER c ON d", "CREATE FUNCTION d_when_fail() RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO qualification VALUES(99); PERFORM 1/0; RETURN true; END$$", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON d DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN(d_when_fail()) EXECUTE FUNCTION d_log()")
		run(deferredFailure("INSERT INTO d(id,v) VALUES(10,100)", "d_when_fail"))
		run(deferredCount("d", 1))
		run(deferredCount("qualification", 3))
		run(deferredCount("audit", 1))
	})
}

// OID metadata and raw carrier checks remain intact. As specified by the
// fixture's client contract, pq exposes OID as decimal bytes (normalized to
// string), while native exposes int64. Only the expected zero's Go carrier
// differs; neither SQL values nor independently asserted OID types change.
func deferredOIDCarriers(b fixture.Backend, s fixture.Step) fixture.Step {
	if b.PG {
		return s
	}
	for _, row := range s.Rows {
		for i, typ := range s.Types {
			if typ == "OID" {
				if value, ok := row[i].(string); ok && value == "0" {
					row[i] = int64(0)
				}
			}
		}
	}
	return s
}
