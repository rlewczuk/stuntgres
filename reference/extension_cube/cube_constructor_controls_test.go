package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const cubeConstructorControlTable = "cube_construct_control_ns.rows"
const cubeConstructorControlView = "cube_construct_control_ns.inquiries"

func cubeConstructorControlDDL() []string {
	return []string{
		"CREATE ROLE cube_construct_owner SUPERUSER",
		"CREATE SCHEMA cube_construct_control_ns AUTHORIZATION cube_construct_owner",
		"SET ROLE cube_construct_owner",
		"CREATE EXTENSION cube SCHEMA cube_construct_control_ns",
		"CREATE TABLE " + cubeConstructorControlTable + "(id integer NOT NULL,v cube_construct_control_ns.cube DEFAULT cube_construct_control_ns.cube(7),CONSTRAINT cube_construct_dim CHECK(cube_construct_control_ns.cube_dim(v)<=3))",
		"CREATE VIEW " + cubeConstructorControlView + " AS SELECT id,cube_construct_control_ns.cube(v,5,6) AS extended,cube_construct_control_ns.cube_dim(v) AS dim,cube_construct_control_ns.cube_is_point(v) AS point FROM " + cubeConstructorControlTable,
		"RESET ROLE",
	}
}

func cubeConstructorCheckConsumers(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, recorder *fixture.Recorder, table, view, cubeName string, expected [][]any) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, recorder, s) }
	run(fixture.Exec("SET extra_float_digits=3", 0))
	s := fixture.CubeSelect(b, "SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, expected)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { cubeMetadata(t, b.PG, rows, s.Columns, []bool{false, true}) }
	run(s)
	run(fixture.Prepared(s))
	viewCheck := cubeConstructorSelect(b, "SELECT extended,dim,point FROM "+view+" WHERE id=2", []string{"extended", "dim", "point"}, []string{"CUBE", "INT4", "BOOL"}, [][]any{{"(1, 5),(1, 6)", int64(1), true}})
	run(viewCheck)
	run(fixture.Prepared(viewCheck))
	// Execute rebuilt DEFAULT and CHECK expressions, rather than merely reading
	// values that happened to be constructed before the replacement.
	run(fixture.Exec("INSERT INTO "+table+"(id) VALUES (90)", 1))
	run(fixture.CubeSelect(b, "SELECT v FROM "+table+" WHERE id=90", []string{"v"}, [][]any{{"(7)"}}))
	run(fixture.Exec("DELETE FROM "+table+" WHERE id=90", 1))
	check := cubeConstructorDiagnostic("23514", `new row for relation "`+table[strings.LastIndex(table, ".")+1:]+`" violates check constraint "cube_construct_dim"`, "Failing row contains (91, (1, 2, 3, 4)).")
	check.Schema, check.Table, check.Constraint = table[:strings.LastIndex(table, ".")], table[strings.LastIndex(table, ".")+1:], "cube_construct_dim"
	bad := fixture.ErrorDiagnostic("INSERT INTO "+table+" VALUES (91,"+cubeName+"(ARRAY[1,2,3,4]))", check)
	bad.ErrorStage = "execute"
	run(bad)
	q := "SELECT " + cubeName + "($1::" + cubeName + ",$2::double precision,$3::double precision) AS v"
	stmt, err := db.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: db, Statement: stmt, SQL: q}
	fixture.RunStep(t, p.Ctx, held, recorder, cubeConstructorSelect(b, q, []string{"v"}, []string{"CUBE"}, [][]any{{"(1, 2),(1, 3)"}}, "1", 2, 3))
	if table == cubeConstructorControlTable {
		run(fixture.Select("SELECT r.rolname AS extension_owner FROM pg_roles r WHERE r.oid=(SELECT e.extowner FROM pg_extension e WHERE e.extname='cube')", []string{"extension_owner"}, []string{"NAME"}, [][]any{{"cube_construct_owner"}}))
		run(fixture.Select("SELECT r.rolname AS table_owner FROM pg_roles r WHERE r.oid=(SELECT c.relowner FROM pg_class c WHERE c.oid='cube_construct_control_ns.rows'::regclass::oid)", []string{"table_owner"}, []string{"NAME"}, [][]any{{"cube_construct_owner"}}))
	}
	for _, signature := range []string{
		"double precision", "double precision,double precision",
		"double precision[]", "double precision[],double precision[]",
		cubeName + ",double precision", cubeName + ",double precision,double precision",
	} {
		run(fixture.Select("SELECT p.prorettype="+cubeLiteral(cubeName)+"::regtype::oid AS result_identity FROM pg_proc p WHERE p.oid="+cubeLiteral(cubeName+"("+signature+")")+"::regprocedure::oid", []string{"result_identity"}, []string{"BOOL"}, [][]any{{true}}))
	}
}

// Shared only by the new controls and feature-smoke scenarios. SQL checks share
// one body across backends; native controls remain an explicit native phase.
func cubeConstructorControlCycle(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table, view, cubeName string, expected, lowDisplay [][]any) {
	t.Helper()
	verify := func(db fixture.Executor, recorder *fixture.Recorder, backend fixture.Backend) {
		cubeConstructorCheckConsumers(t, p, backend, db, recorder, table, view, cubeName, expected)
	}
	verify(c, b.Recorder, b)
	if b.PG {
		// Real SQL recreation, not a fictitious PostgreSQL Reset/Restore API.
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("DROP VIEW "+view, 0))
		run(fixture.Exec("DROP TABLE "+table, 0))
		run(fixture.Exec("DROP EXTENSION cube", 0))
		owned := table == cubeConstructorControlTable
		if owned {
			run(fixture.Exec("SET ROLE cube_construct_owner", 0))
		}
		run(fixture.Exec("CREATE EXTENSION cube SCHEMA "+strings.TrimSuffix(cubeName, ".cube"), 0))
		run(fixture.Exec("CREATE TABLE "+table+"(id integer NOT NULL,v "+cubeName+" DEFAULT "+cubeName+"(7),CONSTRAINT cube_construct_dim CHECK("+strings.Replace(cubeName, ".cube", ".cube_dim", 1)+"(v)<=3))", 0))
		run(fixture.Exec("CREATE VIEW "+view+" AS SELECT id,"+cubeName+"(v,5,6) AS extended,"+strings.Replace(cubeName, ".cube", ".cube_dim", 1)+"(v) AS dim,"+strings.Replace(cubeName, ".cube", ".cube_is_point", 1)+"(v) AS point FROM "+table, 0))
		if owned {
			run(fixture.Exec("RESET ROLE", 0))
		}
		for _, row := range expected {
			run(fixture.Exec("INSERT INTO "+table+" VALUES ($1,$2)", 1, row...))
		}
		verify(c, b.Recorder, b)
		return
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET extra_float_digits=0", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO "+table+" VALUES (99,"+cubeName+"(987654321))", 1))
	var dump bytes.Buffer
	err := stuntgres.Dump(p.Ctx, p.Native, &dump)
	_, rollbackErr := c.ExecContext(p.Ctx, "ROLLBACK")
	if err != nil || rollbackErr != nil {
		t.Fatalf("committed-only Dump: %v, rollback: %v", err, rollbackErr)
	}
	if strings.Contains(dump.String(), "987654321") {
		t.Fatal("Dump leaked private transaction")
	}
	ext, consumer := strings.Index(dump.String(), "CREATE EXTENSION"), strings.Index(dump.String(), "CREATE TABLE")
	if ext < 0 || consumer < ext || strings.Contains(dump.String(), "CREATE FUNCTION") || strings.Contains(dump.String(), "CREATE TYPE") {
		t.Fatal("extension must precede consumers without standalone members", dump.String())
	}
	retained, err := c.QueryContext(p.Ctx, "SELECT id,v FROM "+table+" ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	metadata := cubeMetadata(t, false, retained, []string{"id", "v"}, []bool{false, true})
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	verify(c, b.Recorder, b)
	cubeConsumeRetained(t, retained, lowDisplay)
	cubeColumnMetadata(t, false, metadata, []string{"id", "v"}, []bool{false, true})
	target := fixture.NewFixture(t, false, true)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE cube_construct_allocator_spacer", 0))
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE cube_construct_owner SUPERUSER", 0))
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE cube_construct_allocator_spacer(v integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	verify(target.Memory, target.MemoryRecord, b)
	old, err := target.Memory.QueryContext(target.Ctx, "SELECT id,v FROM "+table+" ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	oldMetadata := cubeMetadata(t, false, old, []string{"id", "v"}, []bool{false, true})
	// A valid earlier staged constructor write must not escape failed Restore.
	bad := "CREATE EXTENSION cube; CREATE TABLE staged_cube(v cube DEFAULT cube(7)); INSERT INTO staged_cube VALUES(cube(1)); INSERT INTO staged_cube VALUES(cube(ARRAY[1,2],ARRAY[3]));"
	want := cubeConstructorMismatch()
	fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &want)
	verify(target.Memory, target.MemoryRecord, b)
	cubeConsumeRetained(t, old, expected)
	cubeColumnMetadata(t, false, oldMetadata, []string{"id", "v"}, []bool{false, true})
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT relname FROM pg_class WHERE relname='staged_cube'", []string{"relname"}, []string{"NAME"}, [][]any{}))
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("SET extra_float_digits=0", 0))
	var redump bytes.Buffer
	if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("restored/failed Restore changed deterministic dump", redump.String())
	}
	if p.Postgres != nil {
		replay, err := p.Postgres.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		ownerCreated, replayStarted := false, false
		defer func() {
			// A failed assertion must also restore this physical session and
			// remove replay-owned objects before the PostgreSQL backend runs.
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			queries := []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET ROLE"}
			if replayStarted {
				queries = append(queries,
					"DROP EXTENSION IF EXISTS cube CASCADE",
					"DROP SCHEMA IF EXISTS cube_construct_control_ns CASCADE",
					"DROP SCHEMA public CASCADE",
					"CREATE SCHEMA public")
			}
			if ownerCreated {
				queries = append(queries, "DROP ROLE IF EXISTS cube_construct_owner")
			}
			queries = append(queries, "DISCARD ALL")
			for _, query := range queries {
				if _, err := replay.ExecContext(cleanupCtx, query); err != nil {
					t.Errorf("constructor replay cleanup %s: %v", query, err)
				}
			}
			if err := replay.Close(); err != nil {
				t.Error("close constructor replay session", err)
			}
		}()
		// Replay the actual emitted native SQL, never a hand-written substitute.
		if table == cubeConstructorControlTable {
			// Native dumps name logical owners but do not create cluster
			// roles. Match the independent-target prerequisite in PostgreSQL.
			// This replay's deferred cleanup also covers failed assertions.
			if _, err := replay.ExecContext(p.Ctx, "CREATE ROLE cube_construct_owner SUPERUSER"); err != nil {
				t.Fatal("create logical dump replay owner", err)
			}
			ownerCreated = true
		}
		replayStarted = true
		if _, err := replay.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual constructor Dump SQL oracle replay", err)
		}
		for _, query := range []string{"RESET SESSION AUTHORIZATION", "RESET ROLE"} {
			if _, err := replay.ExecContext(p.Ctx, query); err != nil {
				t.Fatal("reset constructor replay authorization", err)
			}
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		verify(replay, pg.Recorder, pg)
	}
}

func TestReferenceCubeConstructorControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"cube_construct_owner"})
	fixture.SchemaReferenceCleanup(t, "cube_construct_control_ns")
	cubeConstructorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range cubeConstructorControlDDL() {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO "+cubeConstructorControlTable+" VALUES (1,cube_construct_control_ns.cube(ARRAY[1,2],ARRAY[3,4])),(2,cube_construct_control_ns.cube(1,1)),(3,NULL),(4,cube_construct_control_ns.cube(0.10000000000000002::double precision)),(5,cube_construct_control_ns.cube('-0'::double precision)),(6,cube_construct_control_ns.cube('5e-324'::double precision)),(7,cube_construct_control_ns.cube('NaN'::double precision))", 7))
		rows := [][]any{{int64(1), "(1, 2),(3, 4)"}, {int64(2), "(1)"}, {int64(3), nil}, {int64(4), "(0.10000000000000002)"}, {int64(5), "(-0)"}, {int64(6), "(5e-324)"}, {int64(7), "(NaN)"}}
		low := [][]any{{int64(1), "(1, 2),(3, 4)"}, {int64(2), "(1)"}, {int64(3), nil}, {int64(4), "(0.1)"}, {int64(5), "(-0)"}, {int64(6), "(4.94065645841247e-324)"}, {int64(7), "(NaN)"}}
		cubeConstructorControlCycle(t, p, b, c, cubeConstructorControlTable, cubeConstructorControlView, "cube_construct_control_ns.cube", rows, low)
	})
}
