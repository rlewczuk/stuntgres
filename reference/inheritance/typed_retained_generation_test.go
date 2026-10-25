package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/ngrok/sqlmw"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func typedGenerationIdentities(t *testing.T, p *fixture.Pair, c *sql.Conn, definitionQuery string) [5]int64 {
	t.Helper()
	var ids [5]int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 't'::regclass::oid,'t'::regtype::oid,'pair'::regtype::oid,'sentinel'::regclass::oid").Scan(&ids[0], &ids[1], &ids[2], &ids[3]); err != nil {
		t.Fatal(err)
	}
	if err := c.QueryRowContext(p.Ctx, definitionQuery).Scan(&ids[4]); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if id <= 0 {
			t.Fatal("invalid retained generation identity", ids)
		}
	}
	if ids[1] == ids[2] {
		t.Fatal("table row type aliases standalone source", ids)
	}
	return ids
}

func typedIdentityNullDiagnostic() fixture.Diagnostic {
	d := typedTableError("23502", `null value in column "id" of relation "t" violates not-null constraint`)
	d.Detail, d.Schema, d.Table, d.Column = "Failing row contains (null).", "public", "t", "id"
	return d
}

func typedGeneratedValueDiagnostic() fixture.Diagnostic {
	d := typedTableError("428C9", `cannot insert a non-DEFAULT value into column "g"`)
	d.Detail = `Column "g" is a generated column.`
	return d
}

func typedRetainedIdentityState(t *testing.T, p *fixture.Pair, x fixture.Executor, recorder *fixture.Recorder, postgres, prepared, attached bool, rows [][]any, last int64) {
	t.Helper()
	run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, x, recorder, s) }
	run(fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
	run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, rows))
	fixture.TableDDLMetadata(t, p.Ctx, x, postgres, prepared, "SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, []bool{false}, len(rows))
	fixture.TableDDLMetadata(t, p.Ctx, x, postgres, prepared, "SELECT id FROM t WHERE id=0", []string{"id"}, []string{"INT4"}, []bool{false}, 0)
	typedConstraintAttachmentState(run, attached)
	run(fixture.Select("SELECT pg_get_serial_sequence('t','id') AS owned_sequence", []string{"owned_sequence"}, []string{"TEXT"}, [][]any{{"public.t_id_seq"}}))
	run(fixture.Select("SELECT seqtypid='integer'::regtype::oid AS integer_sequence,seqstart,seqincrement,seqmin,seqmax,seqcache,seqcycle FROM pg_sequence WHERE seqrelid='t_id_seq'::regclass::oid", []string{"integer_sequence", "seqstart", "seqincrement", "seqmin", "seqmax", "seqcache", "seqcycle"}, []string{"BOOL", "INT8", "INT8", "INT8", "INT8", "INT8", "BOOL"}, [][]any{{true, int64(7), int64(3), int64(1), int64(100), int64(1), false}}))
	run(fixture.Select("SELECT last_value,is_called FROM t_id_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{last, true}}))
	run(fixture.Select("SELECT deptype FROM pg_depend WHERE classid='pg_class'::regclass::oid AND objid='t_id_seq'::regclass::oid AND objsubid=0 AND refclassid='pg_class'::regclass::oid AND refobjid='t'::regclass::oid AND refobjsubid=1", []string{"deptype"}, []string{"CHAR"}, [][]any{{"i"}}))
	failure := fixture.ErrorDiagnostic("INSERT INTO t VALUES(NULL)", typedIdentityNullDiagnostic())
	failure.ErrorStage = "execute"
	run(failure)
	run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, rows))
	run(fixture.Select("SELECT last_value,is_called FROM t_id_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{last, true}}))
}

func typedRetainedGeneratedState(t *testing.T, p *fixture.Pair, x fixture.Executor, recorder *fixture.Recorder, postgres, prepared, attached bool, rows [][]any) {
	t.Helper()
	run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, x, recorder, s) }
	run(fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
	run(fixture.Select("SELECT a,g FROM t ORDER BY a NULLS LAST", []string{"a", "g"}, []string{"INT4", "INT4"}, rows))
	fixture.TableDDLMetadata(t, p.Ctx, x, postgres, prepared, "SELECT a,g FROM t ORDER BY a NULLS LAST", []string{"a", "g"}, []string{"INT4", "INT4"}, []bool{true, true}, len(rows))
	fixture.TableDDLMetadata(t, p.Ctx, x, postgres, prepared, "SELECT a,g FROM t WHERE a=0", []string{"a", "g"}, []string{"INT4", "INT4"}, []bool{true, true}, 0)
	typedConstraintAttachmentState(run, attached)
	run(fixture.Select("SELECT adnum,pg_get_expr(adbin,adrelid) AS definition FROM pg_attrdef WHERE adrelid='t'::regclass::oid ORDER BY adnum", []string{"adnum", "definition"}, []string{"INT2", "TEXT"}, [][]any{{int64(2), "(a * 2)"}}))
	run(fixture.Select("SELECT refobjsubid,deptype FROM pg_depend WHERE classid=2604::oid AND objid=(SELECT oid FROM pg_attrdef WHERE adrelid='t'::regclass::oid AND adnum=2) AND objsubid=0 AND refclassid='pg_class'::regclass::oid AND refobjid='t'::regclass::oid ORDER BY refobjsubid", []string{"refobjsubid", "deptype"}, []string{"INT4", "CHAR"}, [][]any{{int64(1), "n"}, {int64(2), "i"}}))
	failure := fixture.ErrorDiagnostic("INSERT INTO t(a,g) VALUES(4,99)", typedGeneratedValueDiagnostic())
	failure.ErrorStage = "execute"
	if prepared {
		failure.ErrorStage = "prepare"
	}
	run(failure)
	run(fixture.Select("SELECT a,g FROM t ORDER BY a NULLS LAST", []string{"a", "g"}, []string{"INT4", "INT4"}, rows))
}

func typedRetainedGenerationControls(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool, setup []fixture.Step, definitionQuery string, state, follow func(*testing.T, fixture.Executor, *fixture.Recorder, bool), invalidInsert string, want fixture.Diagnostic) {
	t.Helper()
	if b.PG {
		// Ordinary declarations and ALTER OF use the same admitted public route.
		// Replay consumes the real export unchanged, even during oracle discovery.
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		for _, s := range setup {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s)
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if b.PG {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted retained generation SQL replay", err)
		}
		state(t, p.Postgres, p.PostgresRecord, true)
		follow(t, p.Postgres, p.PostgresRecord, true)
		return
	}
	freshDriver := stuntgres.NewDriver()
	fresh := fixture.DriverPool(t, freshDriver, "typed-retained-generation-fresh")
	connector, err := freshDriver.OpenConnector("typed-retained-generation-fresh")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{Connector: connector}, fixture.Interceptor{})
	recordedConnector, err := wrapped.(driver.DriverContext).OpenConnector("")
	if err != nil {
		t.Fatal(err)
	}
	observeFresh := sql.OpenDB(recordedConnector)
	defer observeFresh.Close()
	if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("fresh retained generation Restore", err)
	}
	state(t, observeFresh, p.MemoryRecord, false)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("Reset/Restore retained generation", err)
	}
	state(t, b.DB, b.Recorder, false)
	for _, target := range []struct{ control, observe *sql.DB }{{fresh, observeFresh}, {p.Native, b.DB}} {
		c, err := target.observe.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		before := typedGenerationIdentities(t, p, c, definitionQuery)
		err = stuntgres.Restore(p.Ctx, target.control, strings.NewReader(dump.String()+"\n"+invalidInsert+";"))
		fixture.AssertDiagnostic(t, err, &want)
		state(t, c, p.MemoryRecord, false)
		if after := typedGenerationIdentities(t, p, c, definitionQuery); after != before {
			t.Fatal("failed import changed installed identities", before, after)
		}
		follow(t, c, p.MemoryRecord, false)
	}
}

func TestReferenceTypedTableIdentityAttachmentControls(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		setup := []fixture.Step{
			fixture.Exec("CREATE TYPE pair AS (id integer)", 0),
			fixture.Exec("CREATE TABLE sentinel(v integer)", 0),
			fixture.Exec("INSERT INTO sentinel VALUES(91)", 1),
			fixture.Exec("CREATE TABLE t(id integer GENERATED BY DEFAULT AS IDENTITY(START 7 INCREMENT 3 MINVALUE 1 MAXVALUE 100 CACHE 1 NO CYCLE))", 0),
			fixture.Exec("INSERT INTO t DEFAULT VALUES", 1),
			fixture.Exec("INSERT INTO t DEFAULT VALUES", 1),
			fixture.Exec("INSERT INTO t VALUES($1)", 1, int64(41)),
		}
		for _, s := range setup {
			run(s)
		}
		seed := [][]any{{int64(7)}, {int64(10)}, {int64(41)}}
		typedRetainedIdentityState(t, p, c, b.Recorder, b.PG, prepared, false, seed, 10)
		definitionQuery := "SELECT 't_id_seq'::regclass::oid"
		before := typedGenerationIdentities(t, p, c, definitionQuery)
		attach := fixture.Exec("ALTER TABLE t OF pair", 0)
		typedConstraintUtility(t, p, c, prepared, run, attach.Query, fixture.TypedTableAttachment("t", "pair", false))
		setup = append(setup, attach)
		typedRetainedIdentityState(t, p, c, b.Recorder, b.PG, prepared, true, seed, 10)
		if after := typedGenerationIdentities(t, p, c, definitionQuery); after != before {
			t.Fatal("attachment changed identity providers", before, after)
		}
		advance := fixture.Exec("INSERT INTO t DEFAULT VALUES", 1)
		run(advance)
		setup = append(setup, advance)
		captured := [][]any{{int64(7)}, {int64(10)}, {int64(13)}, {int64(41)}}
		state := func(t *testing.T, x fixture.Executor, recorder *fixture.Recorder, postgres bool) {
			typedRetainedIdentityState(t, p, x, recorder, postgres, prepared, true, captured, 13)
		}
		follow := func(t *testing.T, x fixture.Executor, recorder *fixture.Recorder, postgres bool) {
			s := fixture.Exec("INSERT INTO t DEFAULT VALUES", 1)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, x, recorder, s)
			typedRetainedIdentityState(t, p, x, recorder, postgres, prepared, true, [][]any{{int64(7)}, {int64(10)}, {int64(13)}, {int64(16)}, {int64(41)}}, 16)
		}
		t.Run("attachment", func(t *testing.T) {
			typedRetainedIdentityState(t, p, c, b.Recorder, b.PG, prepared, true, captured, 13)
			boundary := fixture.ErrorDiagnostic("CREATE TABLE blocked OF pair(id WITH OPTIONS GENERATED ALWAYS AS IDENTITY)", typedTableError("0A000", "identity columns are not supported on typed tables"))
			boundary.ErrorStage = "execute"
			boundary.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, boundary)
		})
		t.Run("controls", func(t *testing.T) {
			typedRetainedGenerationControls(t, p, b, prepared, setup, definitionQuery, state, follow, "INSERT INTO t OVERRIDING SYSTEM VALUE VALUES(NULL)", typedIdentityNullDiagnostic())
		})
	})
}

func TestReferenceTypedTableGeneratedAttachmentControls(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		setup := []fixture.Step{
			fixture.Exec("CREATE TYPE pair AS (a integer,g integer)", 0),
			fixture.Exec("CREATE TABLE sentinel(v integer)", 0),
			fixture.Exec("INSERT INTO sentinel VALUES(91)", 1),
			fixture.Exec("CREATE TABLE t(a integer,g integer GENERATED ALWAYS AS(a*2) STORED)", 0),
			fixture.Exec("INSERT INTO t(a) VALUES($1),($2)", 2, int64(4), nil),
		}
		for _, s := range setup {
			run(s)
		}
		seed := [][]any{{int64(4), int64(8)}, {nil, nil}}
		typedRetainedGeneratedState(t, p, c, b.Recorder, b.PG, prepared, false, seed)
		definitionQuery := "SELECT oid::bigint FROM pg_attrdef WHERE adrelid='t'::regclass::oid AND adnum=2"
		before := typedGenerationIdentities(t, p, c, definitionQuery)
		attach := fixture.Exec("ALTER TABLE t OF pair", 0)
		typedConstraintUtility(t, p, c, prepared, run, attach.Query, fixture.TypedTableAttachment("t", "pair", false))
		setup = append(setup, attach)
		typedRetainedGeneratedState(t, p, c, b.Recorder, b.PG, prepared, true, seed)
		if after := typedGenerationIdentities(t, p, c, definitionQuery); after != before {
			t.Fatal("attachment changed stored-generation identity", before, after)
		}
		changes := []fixture.Step{fixture.Exec("INSERT INTO t(a) VALUES($1)", 1, int64(5)), fixture.Exec("UPDATE t SET a=$1 WHERE a=4", 1, int64(7))}
		for _, s := range changes {
			run(s)
		}
		setup = append(setup, changes...)
		captured := [][]any{{int64(5), int64(10)}, {int64(7), int64(14)}, {nil, nil}}
		state := func(t *testing.T, x fixture.Executor, recorder *fixture.Recorder, postgres bool) {
			typedRetainedGeneratedState(t, p, x, recorder, postgres, prepared, true, captured)
		}
		follow := func(t *testing.T, x fixture.Executor, recorder *fixture.Recorder, postgres bool) {
			s := fixture.Exec("INSERT INTO t(a) VALUES($1)", 1, int64(9))
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, x, recorder, s)
			typedRetainedGeneratedState(t, p, x, recorder, postgres, prepared, true, [][]any{{int64(5), int64(10)}, {int64(7), int64(14)}, {int64(9), int64(18)}, {nil, nil}})
		}
		t.Run("attachment", func(t *testing.T) {
			typedRetainedGeneratedState(t, p, c, b.Recorder, b.PG, prepared, true, captured)
			boundary := fixture.ErrorDiagnostic("CREATE TABLE blocked OF pair(g WITH OPTIONS GENERATED ALWAYS AS(1) STORED)", typedTableError("0A000", "generated columns are not supported on typed tables"))
			boundary.ErrorStage = "execute"
			boundary.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, boundary)
		})
		t.Run("controls", func(t *testing.T) {
			typedRetainedGenerationControls(t, p, b, prepared, setup, definitionQuery, state, follow, "INSERT INTO t(a,g) VALUES(4,99)", typedGeneratedValueDiagnostic())
		})
	})
}
