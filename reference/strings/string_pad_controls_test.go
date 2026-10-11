package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringPadControls(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE sp_control(id int,s text DEFAULT (lpad('a',3,'x')),v bytea,CHECK(reverse(s)='axx'))", 0),
			fixture.Exec(`INSERT INTO sp_control(id,v) VALUES(1,'\x00ff'::bytea)`, 1),
			fixture.Exec("INSERT INTO sp_control VALUES(2,NULL,NULL)", 1),
			fixture.Exec("CREATE VIEW sp_control_view AS SELECT id,lpad(s,4,'y') AS l,rpad(s,4,'y') AS r,repeat(s,2) AS t,reverse(s) AS v,reverse(v) AS b FROM sp_control", 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		want := fixture.Select("SELECT id,l,r,t,v,b FROM sp_control_view ORDER BY id", []string{"id", "l", "r", "t", "v", "b"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BYTEA"}, [][]any{{int64(1), "yxxa", "xxay", "xxaxxa", "axx", string([]byte{0xff, 0})}, {int64(2), nil, nil, nil, nil, nil}})
		stringPadBoth(t, p, b, c, want)
		if b.PG {
			return
		} // Native controls have no PostgreSQL API equivalent.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		old, err := c.QueryContext(p.Ctx, want.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		meta, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.StringCoreRetainedRows(t, old, nil, want.Columns, want.Types, want.Rows, false)
		for i, typ := range want.Types {
			if meta[i].Name() != want.Columns[i] || meta[i].DatabaseTypeName() != typ {
				t.Fatal(meta)
			}
		}
		stringPadBoth(t, p, b, c, want)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if dump.String() != redump.String() {
			t.Fatal("canonical redump changed", dump.String(), redump.String())
		}
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, redump.String()); err != nil {
				t.Fatal("actual emitted SQL replay", err)
			}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, want)
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
		valid := `CREATE TABLE restored(s text,v bytea); INSERT INTO restored VALUES(lpad(reverse('ab'),3,'x'),reverse('\x00ff'::bytea)),(rpad('a',3,'x'),'AB'::bytea),(repeat('a',2),NULL);`
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(valid)); err != nil {
			t.Fatal(err)
		}
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		bad := `CREATE TABLE bad(s text); INSERT INTO bad VALUES('first'); INSERT INTO bad VALUES(repeat('x',1073741820));`
		d := fixture.ControlsReferenceDiagnostic("54000", "requested length too large")
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &d)
		for _, expr := range []string{"lpad('a',$1,'x')", "rpad('a',3,$1)", "repeat('a',$1)", "reverse($1)", "lpad(reverse($1),3,'x')"} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE bad(s text); INSERT INTO bad VALUES("+expr+");")); err == nil {
				t.Fatal("Restore accepted parameter", expr)
			}
		}
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if before.String() != after.String() {
			t.Fatal("failed Restore changed committed state")
		}
		stringPadBoth(t, p, b, c, fixture.Select("SELECT s,v FROM restored ORDER BY s", []string{"s", "v"}, []string{"TEXT", "BYTEA"}, [][]any{{"aa", nil}, {"axx", "AB"}, {"xba", string([]byte{0xff, 0})}}))
	})
}
