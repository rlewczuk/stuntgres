package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceStringReplaceControls(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE sr_control(id int,s text DEFAULT (overlay(replace('abc','b','X'),'Y',2)),f bool DEFAULT (starts_with('abc','a')),p bool DEFAULT ('abc' ^@ 'a'),CHECK(s ^@ 'a' AND starts_with(s,'a')))", 0),
			referenceExec("INSERT INTO sr_control(id) VALUES(1)", 1),
			referenceExec("INSERT INTO sr_control VALUES(2,translate('abc','b','Z'),starts_with('abc','a'),'abc' ^@ 'a')", 1),
			referenceExec("CREATE VIEW sr_control_view AS SELECT id,overlay(s PLACING 'X' FROM 2 FOR 1) AS o,replace(s,'a','Z') AS r,translate(s,'a','1') AS t,f,p FROM sr_control", 0),
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		want := referenceSelect("SELECT id,o,r,t,f,p FROM sr_control_view ORDER BY id", []string{"id", "o", "r", "t", "f", "p"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(1), "aXc", "ZYc", "1Yc", true, true}, {int64(2), "aXc", "ZZc", "1Zc", true, true}})
		stringReplaceBoth(t, p, b, c, want)
		if b.pg {
			return
		} // Native APIs have no PostgreSQL equivalent.
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
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		stringCoreRetainedRows(t, old, nil, want.columns, want.types, want.rows, false)
		for i, typ := range want.types {
			if meta[i].DatabaseTypeName() != typ {
				t.Fatal(meta)
			}
		}
		stringReplaceBoth(t, p, b, c, want)
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
				t.Fatal("dump SQL replay", err)
			}
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, want)
			if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
		valid := `CREATE TABLE restored(v text,f bool,p bool); INSERT INTO restored VALUES(overlay('abc','X',2,1),starts_with('abc','a'),'abc' ^@ 'a'),(replace('abc','b','Y'),starts_with('abc','z'),'abc' ^@ 'z'),(translate('abc','b','Z'),NULL,NULL);`
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(valid)); err != nil {
			t.Fatal(err)
		}
		retained, err := c.QueryContext(p.ctx, "SELECT v,f,p FROM restored ORDER BY v")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		var before bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
			t.Fatal(err)
		}
		bad := `CREATE TABLE bad(v text); INSERT INTO bad VALUES('first'); INSERT INTO bad VALUES(overlay('abc','X',0));`
		d := controlsReferenceDiagnostic("22011", "negative substring length not allowed")
		referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad)), &d)
		for _, input := range []string{
			`CREATE TABLE bad(v text); INSERT INTO bad VALUES(overlay('abc','X',2,$1));`,
			`CREATE TABLE bad(v text); INSERT INTO bad VALUES(replace('abc','b',$1));`,
			`CREATE TABLE bad(v text); INSERT INTO bad VALUES(translate('abc','b',$1));`,
			`CREATE TABLE bad(v bool); INSERT INTO bad VALUES(starts_with('abc',$1));`,
			`CREATE TABLE bad(v bool); INSERT INTO bad VALUES('abc' ^@ $1);`,
		} {
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(input)); err == nil {
				t.Fatal("Restore accepted parameter", input)
			}
		}
		var after bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
			t.Fatal(err)
		}
		if before.String() != after.String() {
			t.Fatal("failed Restore changed catalog/data")
		}
		rows := [][]any{{"aXc", true, true}, {"aYc", false, false}, {"aZc", nil, nil}}
		stringCoreRetainedRows(t, retained, nil, []string{"v", "f", "p"}, []string{"TEXT", "BOOL", "BOOL"}, rows, false)
		stringReplaceBoth(t, p, b, c, referenceSelect("SELECT v,f,p FROM restored ORDER BY v", []string{"v", "f", "p"}, []string{"TEXT", "BOOL", "BOOL"}, rows))
	})
}
