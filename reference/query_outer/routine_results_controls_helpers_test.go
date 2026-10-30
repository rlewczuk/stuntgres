package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const resultsNoMatchHint = "No function matches the given name and argument types. You might need to add explicit type casts."

// Local adapters retain the shared fixture's setup/recording/control contracts;
// they do not import another external test package or own a second fixture.
func resultsExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, queries ...string) {
	t.Helper()
	for _, q := range queries {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
}

func resultsNativeReplayRound(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check func(*sql.Conn, fixture.Backend)) []byte {
	t.Helper()
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
	check(c, b)
	return dump.Bytes()
}

// Deferred output validation belongs to invocation for quoted and standard SQL;
// standard declarations still retain genuine frozen actions. Native controls use
// an isolated destination, leaving the persistence scenario's incumbent intact.
func resultsDeferredOutputReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string, procedure, standard bool) {
	t.Helper()
	kind, command, query := "f", "FUNCTION", "SELECT * FROM "+name+"()"
	if procedure {
		kind, command, query = "p", "PROCEDURE", "CALL "+name+"(NULL,NULL)"
	}
	source, body := "SELECT 1", "AS 'SELECT 1'"
	if standard {
		source, body = "", "BEGIN ATOMIC SELECT 1; END"
	}
	declaration := command + " " + name + "(OUT a integer,OUT b integer) LANGUAGE SQL " + body
	stream := "SET check_function_bodies = false; CREATE " + declaration + ";"
	var replayTarget *sql.DB
	if b.PG {
		resultsExec(t, p, b, c, "SET check_function_bodies=false")
		defer c.ExecContext(p.Ctx, "SET check_function_bodies=true")
		resultsExec(t, p, b, c, "CREATE "+declaration)
		defer c.ExecContext(p.Ctx, "DROP "+command+" "+name+"()")
		resultsExec(t, p, b, c, "SET check_function_bodies=true")
	} else {
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/deferred-output")
		if err != nil {
			t.Fatal(err)
		}
		raw := sql.OpenDB(connector)
		defer raw.Close()
		replayTarget = raw
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(stream)); err != nil {
			t.Fatal("quoted output declaration replay", err)
		}
		c, err = recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		b = fixture.Backend{Recorder: recorder}
	}
	facts := fixture.Select(
		"SELECT prosrc,prokind::text AS kind,proargnames::text AS names,proargmodes::text AS modes,proallargtypes::text AS types,pronargs::integer AS inputs,prorettype=2249 AS record,prosqlbody IS NOT NULL AS standard FROM pg_proc WHERE oid='"+name+"()'::regprocedure",
		[]string{"prosrc", "kind", "names", "modes", "types", "inputs", "record", "standard"},
		[]string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT4", "BOOL", "BOOL"},
		[][]any{{source, kind, "{a,b}", "{o,o}", "{23,23}", int64(0), true, standard}})
	fixture.RunStep(t, p.Ctx, c, b.Recorder, facts)
	if replayTarget != nil {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, replayTarget, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, replayTarget, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal("deferred output roundtrip", err)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, facts)
	}
	want := fixture.ControlsReferenceDiagnostic("42P13", "return type mismatch in function declared to return record")
	want.Detail, want.Context = "Final statement returns too few columns.", `SQL function "`+name+`" statement 1`
	s := fixture.ErrorDiagnostic(query, want)
	s.SelectRows, s.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	held, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, b.Recorder, s)
	if !procedure {
		resultsExec(t, p, b, c, "CREATE TABLE "+name+"_reached(v integer DEFAULT ("+name+"()).a)")
		defer c.ExecContext(p.Ctx, "DROP TABLE "+name+"_reached")
		insert := fixture.ErrorDiagnostic("INSERT INTO "+name+"_reached DEFAULT VALUES", want)
		insert.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, insert)
		resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM "+name+"_reached", 0)
	}
	resultsExec(t, p, b, c, "CREATE OR REPLACE "+command+" "+name+"(OUT a integer,OUT b integer) LANGUAGE SQL AS 'SELECT 1,2'")
	recovered := resultsStep(b, query, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}})
	if procedure {
		recovered = proceduresStep(b, query, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}})
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, recovered)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(recovered))
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, b.Recorder, recovered)
}
