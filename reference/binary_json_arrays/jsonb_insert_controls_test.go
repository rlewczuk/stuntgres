package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func jsonbInsertFuture(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO jbi_owned(id,src) VALUES(3,'[1]')", 1))
	jsonbSetBoth(t, p, b, c, fixture.Select("SELECT src,d,g FROM jbi_owned WHERE id=3", []string{"src", "d", "g"}, []string{"JSONB", "JSONB", "JSONB"}, [][]any{{"[1]", "[9, 0]", "[1, 9]"}}))
	jsonbInsertViews(t, p, b, c, true)
}

func jsonbInsertControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	t.Helper()
	if b.PG {
		return
	} // Native controls have no PostgreSQL API equivalent.
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	old, err := c.QueryContext(p.Ctx, "SELECT jsonb_insert(src,'{0}','9') AS changed FROM jbi_owned ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	meta, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	fixture.JSONBObjectMetadata(t, old, false, "changed")
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jbi_replacement(v int)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO jbi_replacement VALUES(99)", 1))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.StringCoreRetainedRows(t, old, nil, []string{"changed"}, []string{"JSONB"}, [][]any{{"[9, 0]"}, {nil}}, false)
	if len(meta) != 1 || meta[0].DatabaseTypeName() != "JSONB" {
		t.Fatal(meta)
	}
	unchanged := func() {
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != dump.String() {
			t.Fatalf("persistent dump changed\nbefore:\n%s\nafter:\n%s", dump.String(), after.String())
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		jsonbInsertViews(t, p, b, c, false)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT id FROM jbi_owned WHERE jsonb_insert(src,'{0}','9')='[9,0]' ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	}
	unchanged()
	for _, tc := range []struct {
		expr string
		d    fixture.Diagnostic
	}{
		{`jsonb_insert('{"a":1}','{a}','9')`, jsonbInsertCollision()},
		{`jsonb_insert('[0]','{bad}','9')`, fixture.ControlsReferenceDiagnostic("22P02", `path element at position 1 is not an integer: "bad"`)},
	} {
		script := "CREATE TABLE jbi_bad(v jsonb); INSERT INTO jbi_bad VALUES(jsonb_insert('{}','{a}','1')); INSERT INTO jbi_bad VALUES(" + tc.expr + ");"
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &tc.d)
		unchanged()
	}
	for slot := range 4 {
		args := []string{`'[0]'`, `'{0}'`, `'9'`, `false`}
		args[slot] = "$1"
		if slot == 3 {
			args[slot] = "CASE WHEN TRUE THEN $1 ELSE false END"
		}
		script := "/*é*/ CREATE TABLE jbi_bad(v jsonb); INSERT INTO jbi_bad VALUES(jsonb_insert(" + strings.Join(args, ",") + "));"
		d := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
		d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "$1")])) + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
		unchanged()
	}
	if p.Postgres != nil {
		pg, err := p.Postgres.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer pg.Close()
		if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted dump replay", err)
		}
		pb := fixture.Backend{Name: "postgres", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
		fixture.RunStep(t, p.Ctx, pg, pb.Recorder, check)
		jsonbInsertViews(t, p, pb, pg, false)
		jsonbInsertFuture(t, p, pb, pg)
		if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceJSONBInsertControls(t *testing.T) {
	jsonbInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonbInsertStored(t, p, b, c)
		jsonbInsertControls(t, p, b, c, check)
		jsonbInsertFuture(t, p, b, c)
	})
}
