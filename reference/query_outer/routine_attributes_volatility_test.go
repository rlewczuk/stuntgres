package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAttributesVolatilitySnapshots(t *testing.T) {
	for _, form := range []string{"quoted", "standard", "plpgsql"} {
		t.Run("snapshots_"+form, func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c, "CREATE TABLE ra_snapshot(v integer)")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_snapshot VALUES(0)", 1))
				for _, attr := range []string{"STABLE", "IMMUTABLE", "VOLATILE"} {
					name := map[string]string{"STABLE": "ra_stable", "IMMUTABLE": "ra_immutable", "VOLATILE": "ra_volatile"}[attr]
					ddl := "CREATE FUNCTION " + name + "(integer) RETURNS integer LANGUAGE SQL " + attr + " AS 'SELECT v FROM ra_snapshot'"
					if form == "standard" {
						ddl = "CREATE FUNCTION " + name + "(integer) RETURNS integer LANGUAGE SQL " + attr + " BEGIN ATOMIC SELECT v FROM ra_snapshot;END"
					}
					if form == "plpgsql" {
						ddl = "CREATE FUNCTION " + name + "(integer) RETURNS integer LANGUAGE plpgsql " + attr + " AS $$BEGIN RETURN (SELECT v FROM ra_snapshot);END$$"
					}
					resultsExec(t, p, b, c, ddl)
					attributesIntBoth(t, p, b, c, "SELECT "+name+"(3) AS v", 0)
				}
				q := "WITH changed AS (UPDATE ra_snapshot SET v=1 RETURNING v) SELECT ra_stable(changed.v) AS s,ra_immutable(changed.v) AS i,ra_volatile(changed.v) AS v FROM changed"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, route := range []string{"direct", "fresh", "held", "held"} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE ra_snapshot SET v=0", 1))
					s := attributesStep(b, q, []string{"s", "i", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(0), int64(0), int64(1)}})
					target := fixture.Executor(c)
					if route == "fresh" {
						s = fixture.Prepared(s)
					}
					if route == "held" {
						target = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
					fixture.RunStep(t, p.Ctx, target, b.Recorder, s)
					attributesIntBoth(t, p, b, c, "SELECT v+0 AS v FROM ra_snapshot", 1)
				}
				resultsExec(t, p, b, c,
					"CREATE FUNCTION ra_frontier(OUT s integer,OUT v integer) RETURNS record LANGUAGE plpgsql VOLATILE AS $$BEGIN UPDATE ra_snapshot SET v=2;SELECT ra_stable(1),ra_volatile(1) INTO s,v;END$$",
					"CREATE FUNCTION ra_mutate() RETURNS integer LANGUAGE SQL VOLATILE AS $$UPDATE ra_snapshot SET v=v+1;SELECT v FROM ra_snapshot$$",
					"CREATE FUNCTION ra_stable_nested(OUT changed integer,OUT retained integer) RETURNS record LANGUAGE plpgsql STABLE AS $$BEGIN changed:=ra_mutate();SELECT v INTO retained FROM ra_snapshot;END$$")
				attributesBoth(t, p, b, c, "SELECT * FROM ra_frontier()", []string{"s", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(2)}})
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE ra_snapshot SET v=0", 1))
					s := attributesStep(b, "SELECT * FROM ra_stable_nested()", []string{"changed", "retained"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(0)}})
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					attributesIntBoth(t, p, b, c, "SELECT v+0 AS v FROM ra_snapshot", 1)
				}
			})
		})
	}
	commands := []struct{ name, sql, tag string }{
		{"insert", "INSERT INTO ra_readonly VALUES(2)", "INSERT"},
		{"update", "UPDATE ra_readonly SET v=2", "UPDATE"},
		{"delete", "DELETE FROM ra_readonly", "DELETE"},
		{"merge", "MERGE INTO ra_readonly d USING (SELECT 1 AS v) s ON d.v=s.v WHEN MATCHED THEN UPDATE SET v=2", "MERGE"},
		{"cte", "WITH changed AS (INSERT INTO ra_readonly VALUES(2) RETURNING v) SELECT v FROM changed", "SELECT"},
		{"for_update", "SELECT v FROM ra_readonly FOR UPDATE", "SELECT FOR UPDATE"},
		{"for_share", "SELECT v FROM ra_readonly FOR SHARE", "SELECT FOR SHARE"},
	}
	for _, form := range []string{"quoted", "standard", "plpgsql"} {
		for _, command := range commands {
			t.Run("readonly_"+form+"_"+command.name, func(t *testing.T) {
				proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					resultsExec(t, p, b, c, "CREATE TABLE ra_readonly(v integer)")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_readonly VALUES(1)", 1))
					body := command.sql + ";SELECT 7"
					ddl := "CREATE FUNCTION ra_readonly_fn() RETURNS integer LANGUAGE SQL STABLE AS $$" + body + "$$"
					contextText := `SQL function "ra_readonly_fn" statement 1`
					if form == "standard" {
						ddl = "CREATE FUNCTION ra_readonly_fn() RETURNS integer LANGUAGE SQL STABLE BEGIN ATOMIC " + body + ";END"
					}
					if form == "plpgsql" {
						plCommand := command.sql
						if command.name == "cte" || command.name == "for_update" || command.name == "for_share" {
							plCommand = "FOR item IN " + command.sql + " LOOP PERFORM 1;END LOOP"
						}
						ddl = "CREATE FUNCTION ra_readonly_fn() RETURNS integer LANGUAGE plpgsql STABLE AS $$DECLARE item record;BEGIN " + plCommand + ";RETURN 7;END$$"
						contextText = "SQL statement \"" + command.sql + "\"\nPL/pgSQL function ra_readonly_fn() line 1 at SQL statement"
						if command.name == "cte" || command.name == "for_update" || command.name == "for_share" {
							contextText = "SQL statement \"" + command.sql + "\"\nPL/pgSQL function ra_readonly_fn() line 1 at FOR over SELECT rows"
						}
					}
					resultsExec(t, p, b, c, ddl)
					q := "SELECT ra_readonly_fn() AS v"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					for _, attr := range []string{"STABLE", "IMMUTABLE"} {
						resultsExec(t, p, b, c, "ALTER FUNCTION ra_readonly_fn() "+attr)
						want := proceduresDiagnostic("0A000", command.tag+" is not allowed in a non-volatile function", 0)
						want.Context = contextText
						attributesQueryError(t, p, b, c, q, want)
						s := fixture.ErrorDiagnostic(q, want)
						s.SelectRows, s.ErrorStage = true, "execute"
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
						attributesIntBoth(t, p, b, c, "SELECT v+0 AS v FROM ra_readonly", 1)
					}
					resultsExec(t, p, b, c, "ALTER FUNCTION ra_readonly_fn() VOLATILE")
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, attributesStep(b, q, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
					wantRows := [][]any{{int64(2)}}
					if command.name == "insert" || command.name == "cte" {
						wantRows = [][]any{{int64(1)}, {int64(2)}}
					}
					if command.name == "delete" {
						wantRows = [][]any{}
					}
					if command.name == "for_update" || command.name == "for_share" {
						wantRows = [][]any{{int64(1)}}
					}
					attributesBoth(t, p, b, c, "SELECT v+0 AS v FROM ra_readonly ORDER BY v", []string{"v"}, []string{"INT4"}, wantRows)
				})
			})
		}
	}
}
