package reference_test

import (
	"bytes"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func earthControlRows(low bool) [][]any {
	precision := "(6378168, 0.10000000000000002)"
	if low {
		precision = "(6378168, 0.1)"
	}
	return [][]any{{int64(1), "(6378168)"}, {int64(2), "(6378168, 0)"}, {int64(3), "(6378168, 0, 0)"}, {int64(4), "(-6378168, -0)"}, {int64(5), "(6378168, NaN)"}, {int64(6), nil}, {int64(7), precision}}
}

const earthControlInsert = "INSERT INTO earth_controls VALUES (1,'6378168'),(2,'(6378168,0)'),(3,'(6378168,0,0)'),(4,'(-6378168,-0)'),(5,'(6378168,NaN)'),(6,NULL),(7,'(6378168,0.10000000000000002)')"

func earthControlVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("SET extra_float_digits=3", 0))
	for _, prepared := range []bool{false, true} {
		s := fixture.EarthSelect(b, "SELECT id,v FROM earth_controls ORDER BY id", []string{"id", "v"}, earthControlRows(false))
		s.Prepared = prepared
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { cubeMetadata(t, b.PG, rows, s.Columns, []bool{false, true}) }
		fixture.RunStep(t, p.Ctx, db, recorder, s)
		s = fixture.EarthSelect(b, "SELECT v FROM earth_vacant", []string{"v"}, [][]any{})
		s.Prepared = prepared
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { cubeMetadata(t, b.PG, rows, []string{"v"}, []bool{false}) }
		fixture.RunStep(t, p.Ctx, db, recorder, s)
	}
}

func earthNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET extra_float_digits=0", 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	text := dump.String()
	cubeAt, earthAt, tableAt := -1, -1, strings.Index(text, "CREATE TABLE")
	for offset, line := 0, ""; offset < len(text); offset += len(line) + 1 {
		line = strings.SplitN(text[offset:], "\n", 2)[0]
		if strings.Contains(line, "CREATE EXTENSION") {
			if strings.Contains(line, "earthdistance") {
				earthAt = offset
			} else if strings.Contains(line, "cube") {
				cubeAt = offset
			}
		}
	}
	if cubeAt < 0 || earthAt <= cubeAt || tableAt <= earthAt || strings.Contains(text, "CREATE DOMAIN") || strings.Contains(text, "CREATE FUNCTION") || !strings.Contains(text, "0.10000000000000002") {
		t.Fatal("earth dump prerequisite/exact-value contract", text)
	}
	retained, err := c.QueryContext(p.Ctx, "SELECT id,v FROM earth_controls ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	metadata := cubeMetadata(t, false, retained, []string{"id", "v"}, []bool{false, true})
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	earthAbsent(t, p, b, c)
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	earthControlVerify(t, p, b, c, b.Recorder)
	earthIdentityChecks(t, p, b, c, b.Recorder)
	cubeConsumeRetained(t, retained, earthControlRows(true))
	cubeColumnMetadata(t, false, metadata, []string{"id", "v"}, []bool{false, true})
	target := fixture.NewFixture(t, false, true)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE earth_allocator_spacer(v integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	earthControlVerify(t, target, b, target.Memory, target.MemoryRecord)
	earthIdentityChecks(t, target, b, target.Memory, target.MemoryRecord)
	old, err := target.Memory.QueryContext(target.Ctx, "SELECT id,v FROM earth_controls ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	oldMetadata := cubeMetadata(t, false, old, []string{"id", "v"}, []bool{false, true})
	bad := "CREATE EXTENSION cube; CREATE EXTENSION earthdistance; CREATE TABLE earth_staged(v earth); INSERT INTO earth_staged VALUES ('6378168'); INSERT INTO earth_staged VALUES ('(0)');"
	want := earthCheckDiagnostic("public", "on_surface", true)
	fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &want)
	malformedPrefix := "CREATE EXTENSION cube; CREATE EXTENSION earthdistance; CREATE TABLE earth_staged(v earth); INSERT INTO earth_staged VALUES ('6378168'); INSERT INTO earth_staged VALUES ("
	malformed := cubeSyntaxDiagnostic(`syntax error at or near ")"`)
	malformed.Position = strconv.Itoa(len(malformedPrefix) + 1)
	fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(malformedPrefix+"'(6378168,)');")), &malformed)
	earthControlVerify(t, target, b, target.Memory, target.MemoryRecord)
	earthIdentityChecks(t, target, b, target.Memory, target.MemoryRecord)
	cubeConsumeRetained(t, old, earthControlRows(false))
	cubeColumnMetadata(t, false, oldMetadata, []string{"id", "v"}, []bool{false, true})
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("SET extra_float_digits=0", 0))
	var after bytes.Buffer
	if err := stuntgres.Dump(target.Ctx, target.Native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), after.Bytes()) {
		t.Fatal("earth restore changed equivalent-setting deterministic dump", after.String())
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, text); err != nil {
			t.Fatal("actual emitted earth dump SQL replay", err)
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		earthControlVerify(t, p, pg, pg.DB, pg.Recorder)
		earthIdentityChecks(t, p, pg, pg.DB, pg.Recorder)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceEarthControls(t *testing.T) {
	earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE earth_controls(id integer NOT NULL,v earth)", 0))
		run(fixture.Exec("CREATE TABLE earth_vacant(v earth NOT NULL)", 0))
		run(fixture.Exec(earthControlInsert, 7))
		earthControlVerify(t, p, b, c, b.Recorder)
		for _, q := range []string{"DROP TABLE earth_controls", "DROP TABLE earth_vacant", "DROP EXTENSION earthdistance", "DROP EXTENSION cube", "CREATE EXTENSION cube", "CREATE EXTENSION earthdistance", "CREATE TABLE earth_controls(id integer NOT NULL,v earth)", "CREATE TABLE earth_vacant(v earth NOT NULL)"} {
			run(fixture.Exec(q, 0))
		}
		for _, row := range earthControlRows(false) {
			run(fixture.Exec("INSERT INTO earth_controls VALUES ($1,$2)", 1, row...))
		}
		earthControlVerify(t, p, b, c, b.Recorder)
		earthIdentityChecks(t, p, b, c, b.Recorder)
		if !b.PG {
			earthNativeControls(t, p, b, c)
		}
	})
}
