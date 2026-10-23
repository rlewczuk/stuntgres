package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

type plpgsqlInvocationCase struct{ name, unicode, c string }

var plpgsqlInvocationCases = []plpgsqlInvocationCase{
	{"named", "STRASSE", "STRAßE"},
	{"positional", "STRASSE", "STRAßE"},
	{"alias", "STRASSE", "STRAßE"},
	{"ordinary", "STRASSE", "STRAßE"},
	{"literal", "STRASSE", "STRAßE"},
	{"parameter_type", "STRASSE", "STRAßE"},
	{"local_type", "STRASSE", "STRAßE"},
	{"column_unicode", "STRASSE", "STRASSE"},
	{"column_c", "STRAßE", "STRAßE"},
	{"nested", "STRASSE", "STRAßE"},
	{"nested_restore", "STRASSE/STRASSE", "STRASSE/STRAßE"},
	{"sql_to_pl", "STRASSE", "STRAßE"},
	{"pl_to_sql", "STRASSE", "STRAßE"},
	{"out_upper", "STRASSE", "STRAßE"},
}

var plpgsqlInvocationCollations = []struct{ name, suffix string }{
	{"unicode", " COLLATE pg_catalog.pg_unicode_fast"},
	{"default", ""},
	{"c", ` COLLATE pg_catalog."C"`},
}

func TestReferencePLpgSQLCoreInvocationCollation(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pl_inv_saved")
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA pl_inv_saved",
			`CREATE TABLE pl_inv_saved.source(u text COLLATE pg_catalog.pg_unicode_fast,c text COLLATE pg_catalog."C")`)
		for _, declaration := range []struct{ name, body string }{
			{"named", "BEGIN RETURN upper(x); END"},
			{"positional", "BEGIN RETURN upper($1); END"},
			{"alias", "DECLARE a ALIAS FOR $1; BEGIN RETURN upper(a); END"},
			{"ordinary", "DECLARE y text := x; BEGIN RETURN upper(y); END"},
			{"literal", "DECLARE y text := 'Straße'; BEGIN RETURN upper(y); END"},
			{"parameter_type", "DECLARE y x%TYPE := x; BEGIN RETURN upper(y); END"},
			{"local_type", "DECLARE y text := x; z y%TYPE := y; BEGIN RETURN upper(z); END"},
			{"column_unicode", "DECLARE y pl_inv_saved.source.u%TYPE := 'Straße'; BEGIN RETURN upper(y); END"},
			{"column_c", "DECLARE y pl_inv_saved.source.c%TYPE := 'Straße'; BEGIN RETURN upper(y); END"},
			{"nested", "BEGIN RETURN pl_inv_saved.named(x); END"},
			{"nested_restore", "BEGIN RETURN pl_inv_saved.named(x COLLATE pg_catalog.pg_unicode_fast)||'/'||upper(x); END"},
			{"pl_to_sql", "BEGIN RETURN pl_inv_saved.sql_leaf(x); END"},
		} {
			if !t.Run("declare/"+declaration.name, func(t *testing.T) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION pl_inv_saved."+declaration.name+"(x text) RETURNS text LANGUAGE plpgsql AS $$"+declaration.body+"$$")
			}) {
				t.FailNow()
			}
		}
		for _, declaration := range []struct{ name, query string }{
			{"sql_leaf", "CREATE FUNCTION pl_inv_saved.sql_leaf(x text) RETURNS text LANGUAGE SQL AS 'SELECT upper($1)'"},
			{"sql_to_pl", "CREATE FUNCTION pl_inv_saved.sql_to_pl(x text) RETURNS text LANGUAGE SQL AS 'SELECT pl_inv_saved.named($1)'"},
			{"out_upper", "CREATE FUNCTION pl_inv_saved.out_upper(OUT y text, IN x text) LANGUAGE plpgsql AS $$BEGIN y := upper($2); RETURN; END$$"},
			{"column_zero", "CREATE FUNCTION pl_inv_saved.column_zero() RETURNS text LANGUAGE plpgsql AS $$DECLARE y pl_inv_saved.source.u%TYPE := 'Straße'; BEGIN RETURN upper(y); END$$"},
			{"stored_view", "CREATE VIEW pl_inv_saved.upper_view AS SELECT pl_inv_saved.named('Straße'::text COLLATE pg_catalog.pg_unicode_fast) AS v"},
			{"stored_default", "CREATE TABLE pl_inv_saved.saved(id integer,v text DEFAULT pl_inv_saved.named('Straße'::text COLLATE pg_catalog.pg_unicode_fast))"},
		} {
			if !t.Run("declare/"+declaration.name, func(t *testing.T) { userOperatorExec(t, p, b, c, declaration.query) }) {
				t.FailNow()
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO pl_inv_saved.saved(id) VALUES(1)", 1))
		held := plpgsqlPrepareInvocationCalls(t, p, c)
		defer plpgsqlCloseInvocationCalls(held)
		sourceRound, postgresReplay := 0, 0
		check := func(conn *sql.Conn, backend fixture.Backend) {
			phase := ""
			id := 0
			if conn == c {
				phase = fmt.Sprintf("source_round_%d", sourceRound)
				id = sourceRound + 2
				sourceRound++
			} else {
				postgresReplay++
				phase = fmt.Sprintf("postgres_replay_%d", postgresReplay)
				id = 100 + postgresReplay
			}
			if !t.Run(phase, func(t *testing.T) {
				for _, tc := range plpgsqlInvocationCases {
					for _, collation := range plpgsqlInvocationCollations {
						t.Run(tc.name+"/"+collation.name, func(t *testing.T) {
							want := tc.c
							if collation.name == "unicode" {
								want = tc.unicode
							}
							q := "SELECT pl_inv_saved." + tc.name + "(x=>'Straße'::text" + collation.suffix + ") AS v"
							plpgsqlValue(t, p, backend, conn, q, "TEXT", want)
						})
					}
				}
				calls := held
				if conn != c {
					calls = plpgsqlPrepareInvocationCalls(t, p, conn)
					defer plpgsqlCloseInvocationCalls(calls)
				}
				// The same function/program sees Unicode, default and C call sites in
				// alternating order, then revisits Unicode after nested invocations.
				for _, order := range [][]int{{0, 1, 2}, {2, 0, 1}, {1, 2, 0}} {
					for i := range plpgsqlInvocationCases {
						for _, collation := range order {
							call := calls[i*len(plpgsqlInvocationCollations)+collation]
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: call.statement, SQL: call.query}, backend.Recorder, fixture.Select(call.query, []string{"v"}, []string{"TEXT"}, [][]any{{call.want}}, "Straße"))
						}
					}
				}
				plpgsqlValue(t, p, backend, conn, "SELECT pl_inv_saved.column_zero() AS v", "TEXT", "STRASSE")
				plpgsqlValue(t, p, backend, conn, "SELECT v FROM pl_inv_saved.upper_view", "TEXT", "STRASSE")
				plpgsqlValue(t, p, backend, conn, "SELECT v FROM pl_inv_saved.saved WHERE id=1", "TEXT", "STRASSE")
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec(fmt.Sprintf("INSERT INTO pl_inv_saved.saved(id) VALUES(%d)", id), 1))
				plpgsqlValue(t, p, backend, conn, fmt.Sprintf("SELECT v FROM pl_inv_saved.saved WHERE id=%d", id), "TEXT", "STRASSE")
			}) {
				t.FailNow()
			}
		}
		parametersReplay(t, p, b, c, "pl_inv_saved", check)
	})
}

type plpgsqlInvocationHeld struct {
	statement   *sql.Stmt
	query, want string
}

func plpgsqlPrepareInvocationCalls(t *testing.T, p *fixture.Pair, c *sql.Conn) []plpgsqlInvocationHeld {
	t.Helper()
	var calls []plpgsqlInvocationHeld
	for _, tc := range plpgsqlInvocationCases {
		for _, collation := range plpgsqlInvocationCollations {
			query := "SELECT pl_inv_saved." + tc.name + "($1::text" + collation.suffix + ") AS v"
			statement, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				plpgsqlCloseInvocationCalls(calls)
				t.Fatal(err)
			}
			want := tc.c
			if collation.name == "unicode" {
				want = tc.unicode
			}
			calls = append(calls, plpgsqlInvocationHeld{statement: statement, query: query, want: want})
		}
	}
	return calls
}

func plpgsqlCloseInvocationCalls(calls []plpgsqlInvocationHeld) {
	for _, call := range calls {
		call.statement.Close()
	}
}
