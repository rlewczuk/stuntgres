package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringSliceControls(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE slice_control(id int,s text DEFAULT substr('abcde',2,3),g text GENERATED ALWAYS AS (right(s,2)) STORED,CHECK(left(s,1)='b'))", 0),
			fixture.Exec("INSERT INTO slice_control(id) VALUES(1)", 1),
			fixture.Exec("INSERT INTO slice_control(id,s) VALUES(2,NULL)", 1),
			fixture.Exec("CREATE INDEX slice_control_index ON slice_control ((substr(s,2,1)))", 0),
			fixture.Exec(`CREATE VIEW slice_control_view AS SELECT id,left(s,2) AS l,right(s,2) AS r,substr(s,2) AS a,substring(s FROM 2 FOR 1) AS n,substring(s FROM 'c(.)') AS p,substring(s SIMILAR '%#"c_#"' ESCAPE '#') AS q FROM slice_control`, 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		want := fixture.Select("SELECT id,l,r,a,n,p,q FROM slice_control_view ORDER BY id", []string{"id", "l", "r", "a", "n", "p", "q"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "bc", "cd", "cd", "c", "d", "cd"}, {int64(2), nil, nil, nil, nil, nil, nil}})
		stringPadBoth(t, p, b, c, want)
		textWant := fixture.Select("SELECT l,r,a,n,p,q FROM slice_control_view ORDER BY id", []string{"l", "r", "a", "n", "p", "q"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"bc", "cd", "cd", "c", "d", "cd"}, {nil, nil, nil, nil, nil, nil}})
		textWant.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			fixture.FormatReferenceMetadata(t, b.PG, rows, true)
		}
		stringPadBoth(t, p, b, c, textWant)
		if b.PG {
			return
		} // PostgreSQL has no native control API.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		old, err := c.QueryContext(p.Ctx, textWant.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		fixture.FormatReferenceMetadata(t, false, old, true)
		metadata, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.FormatReferenceMetadata(t, false, old, true)
		fixture.StringCoreRetainedRows(t, old, nil, textWant.Columns, textWant.Types, textWant.Rows, false)
		for i, typ := range textWant.Types {
			if metadata[i].Name() != textWant.Columns[i] || metadata[i].DatabaseTypeName() != typ {
				t.Fatalf("retained metadata[%d]=%v", i, metadata[i])
			}
		}
		stringPadBoth(t, p, b, c, want)
		stringPadBoth(t, p, b, c, textWant)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO slice_control(id) VALUES(3)", 1))
		stringPadBoth(t, p, b, c, fixture.Select("SELECT s,g FROM slice_control WHERE id=3", []string{"s", "g"}, []string{"TEXT", "TEXT"}, [][]any{{"bcd", "cd"}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM slice_control WHERE id=3", 1))
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if dump.String() != redump.String() {
			t.Fatalf("canonical slicing redump changed\nbefore=%s\nafter=%s", dump.String(), redump.String())
		}
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, redump.String()); err != nil {
				t.Fatal("emitted slicing SQL replay:", err)
			}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, want)
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
		valid := `CREATE TABLE slice_replay(s text); INSERT INTO slice_replay VALUES(left('abc',2)),(right('abc',2)),(substr('abc',2)),(substring('abc' FROM 2 FOR 1));`
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(valid)); err != nil {
			t.Fatal(err)
		}
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ sql, code, message, context string }{
			{`CREATE TABLE slice_bad(s text DEFAULT substr('abc',1,-1)); INSERT INTO slice_bad VALUES('safe'); INSERT INTO slice_bad DEFAULT VALUES;`, "22011", "negative substring length not allowed", ""},
			{`CREATE TABLE slice_bad(s text); INSERT INTO slice_bad VALUES('safe'); INSERT INTO slice_bad VALUES(substring('abc','['));`, "2201B", "invalid regular expression: brackets [] not balanced", ""},
			{`CREATE TABLE slice_bad(s text); INSERT INTO slice_bad VALUES('safe'); INSERT INTO slice_bad VALUES(substring('abc','#"a#"b#"c','#'));`, "2200C", "SQL regular expression may not contain more than two escape-double-quote separators", `SQL function "substring" statement 1`},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			d.Context = tc.context
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(tc.sql)), &d)
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if before.String() != after.String() {
				t.Fatal("late failed slicing Restore published catalog or data")
			}
		}
		stringPadBoth(t, p, b, c, fixture.Select("SELECT s FROM slice_replay ORDER BY s", []string{"s"}, []string{"TEXT"}, [][]any{{"ab"}, {"b"}, {"bc"}, {"bc"}}))
	})
}
