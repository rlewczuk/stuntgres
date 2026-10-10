package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceStringPadControls(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE sp_control(id int,s text DEFAULT (lpad('a',3,'x')),v bytea,CHECK(reverse(s)='axx'))", 0),
			referenceExec(`INSERT INTO sp_control(id,v) VALUES(1,'\x00ff'::bytea)`, 1),
			referenceExec("INSERT INTO sp_control VALUES(2,NULL,NULL)", 1),
			referenceExec("CREATE VIEW sp_control_view AS SELECT id,lpad(s,4,'y') AS l,rpad(s,4,'y') AS r,repeat(s,2) AS t,reverse(s) AS v,reverse(v) AS b FROM sp_control", 0),
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		want := referenceSelect("SELECT id,l,r,t,v,b FROM sp_control_view ORDER BY id", []string{"id", "l", "r", "t", "v", "b"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BYTEA"}, [][]any{{int64(1), "yxxa", "xxay", "xxaxxa", "axx", string([]byte{0xff, 0})}, {int64(2), nil, nil, nil, nil, nil}})
		stringPadBoth(t, p, b, c, want)
		if b.pg {
			return
		} // Native controls have no PostgreSQL API equivalent.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		old, err := c.QueryContext(p.ctx, want.query)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		meta, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		stringCoreRetainedRows(t, old, nil, want.columns, want.types, want.rows, false)
		for i, typ := range want.types {
			if meta[i].Name() != want.columns[i] || meta[i].DatabaseTypeName() != typ {
				t.Fatal(meta)
			}
		}
		stringPadBoth(t, p, b, c, want)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil {
			t.Fatal(err)
		}
		if dump.String() != redump.String() {
			t.Fatal("canonical redump changed", dump.String(), redump.String())
		}
		if p.postgres != nil {
			if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.postgres.ExecContext(p.ctx, redump.String()); err != nil {
				t.Fatal("actual emitted SQL replay", err)
			}
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, want)
			if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
		valid := `CREATE TABLE restored(s text,v bytea); INSERT INTO restored VALUES(lpad(reverse('ab'),3,'x'),reverse('\x00ff'::bytea)),(rpad('a',3,'x'),'AB'::bytea),(repeat('a',2),NULL);`
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(valid)); err != nil {
			t.Fatal(err)
		}
		var before bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
			t.Fatal(err)
		}
		bad := `CREATE TABLE bad(s text); INSERT INTO bad VALUES('first'); INSERT INTO bad VALUES(repeat('x',1073741820));`
		d := controlsReferenceDiagnostic("54000", "requested length too large")
		referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad)), &d)
		for _, expr := range []string{"lpad('a',$1,'x')", "rpad('a',3,$1)", "repeat('a',$1)", "reverse($1)", "lpad(reverse($1),3,'x')"} {
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE bad(s text); INSERT INTO bad VALUES("+expr+");")); err == nil {
				t.Fatal("Restore accepted parameter", expr)
			}
		}
		var after bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
			t.Fatal(err)
		}
		if before.String() != after.String() {
			t.Fatal("failed Restore changed committed state")
		}
		stringPadBoth(t, p, b, c, referenceSelect("SELECT s,v FROM restored ORDER BY s", []string{"s", "v"}, []string{"TEXT", "BYTEA"}, [][]any{{"aa", nil}, {"axx", "AB"}, {"xba", string([]byte{0xff, 0})}}))
	})
}
