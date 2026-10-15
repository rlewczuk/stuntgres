package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func stringIdentStoredSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec(`CREATE TABLE ident_stored(id int,s text DEFAULT 'A.B',a text[] DEFAULT parse_ident('D.E'),g text[] GENERATED ALWAYS AS(parse_ident(s,false)) STORED,CHECK(parse_ident(s,false) IS NOT NULL))`, 0),
		fixture.Exec(`INSERT INTO ident_stored(id) VALUES(1)`, 1),
		fixture.Exec(`INSERT INTO ident_stored(id,s) VALUES(2,'"X.Y".Z[]')`, 1),
		fixture.Exec(`CREATE VIEW ident_view AS SELECT id,parse_ident(strict=>false,str=>s) AS ids FROM ident_stored`, 0),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func stringIdentStoredWants() []fixture.Step {
	return []fixture.Step{
		fixture.Select(`SELECT id,s,a,g FROM ident_stored ORDER BY id`, []string{"id", "s", "a", "g"}, []string{"INT4", "TEXT", "_TEXT", "_TEXT"}, [][]any{{int64(1), "A.B", `{d,e}`, `{a,b}`}, {int64(2), `"X.Y".Z[]`, `{d,e}`, `{X.Y,z}`}}),
		fixture.Select(`SELECT id,ids FROM ident_view ORDER BY id`, []string{"id", "ids"}, []string{"INT4", "_TEXT"}, [][]any{{int64(1), `{a,b}`}, {int64(2), `{X.Y,z}`}}),
	}
}

func stringIdentViewCopy(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	var definition string
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('ident_view'::regclass,true)").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	// Execute the actual backend's deparse: positional rendering of reversed
	// roles would bind false as the source, or silently change suffix behavior.
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW "+name+" AS "+definition, 0))
	want := stringIdentStoredWants()[1]
	want.Query = strings.Replace(want.Query, "ident_view", name, 1)
	stringReplaceBoth(t, p, b, c, want)
}

func TestReferenceStringIdentStored(t *testing.T) {
	stringIdentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		stringIdentStoredSetup(t, p, b, c)
		for _, s := range stringIdentStoredWants() {
			stringReplaceBoth(t, p, b, c, s)
		}
		stringIdentViewCopy(t, p, b, c, "ident_copy")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE ident_stored RENAME COLUMN s TO source", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE ident_stored RENAME TO ident_renamed", 0))
		for _, s := range stringIdentStoredWants()[1:] {
			stringReplaceBoth(t, p, b, c, s)
		}
		stringIdentViewCopy(t, p, b, c, "ident_renamed_copy")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ident_renamed(id) VALUES(3)", 1))
		stringReplaceBoth(t, p, b, c, fixture.Select("SELECT source,a,g FROM ident_renamed WHERE id=3", []string{"source", "a", "g"}, []string{"TEXT", "_TEXT", "_TEXT"}, [][]any{{"A.B", `{d,e}`, `{a,b}`}}))
	})
}

func TestReferenceStringIdentLifecycle(t *testing.T) {
	stringIdentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ident_atomic(id int,a text[])", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ident_atomic VALUES(9,'{sentinel}'::text[])", 1))
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO ident_atomic VALUES(1,parse_ident($1)),(2,parse_ident($2))", stringIdentDiagnostic("a.", `No valid identifier after ".".`), "a.b", "a.")
			s.ErrorStage = "execute"
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			stringReplaceBoth(t, p, b, c, fixture.Select("SELECT id,a FROM ident_atomic ORDER BY id", []string{"id", "a"}, []string{"INT4", "_TEXT"}, [][]any{{int64(9), `{sentinel}`}}))
		}
		stringIdentStoredSetup(t, p, b, c)
		stringIdentViewCopy(t, p, b, c, "ident_copy")
		for _, s := range stringIdentStoredWants() {
			stringReplaceBoth(t, p, b, c, s)
		}
		if b.PG {
			return
		} // Native controls have no PostgreSQL API equivalent.
		q := "SELECT parse_ident($1,$2) AS ids"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, held := range []bool{false, true} {
			var rows *sql.Rows
			if held {
				rows, err = st.QueryContext(ctx, "A.B", true)
			} else {
				rows, err = c.QueryContext(ctx, q, "A.B", true)
			}
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled query returned rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("precancel: %T %v", err, err)
			}
		}
		var dump strings.Builder
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		want := stringIdentStoredWants()[0]
		old, err := c.QueryContext(p.Ctx, want.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		metadata, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		fixture.StringCoreRetainedRows(t, old, nil, want.Columns, want.Types, want.Rows, false)
		for i, col := range metadata {
			if col.DatabaseTypeName() != want.Types[i] {
				t.Fatal(metadata)
			}
		}
		for _, s := range stringIdentStoredWants() {
			stringReplaceBoth(t, p, b, c, s)
		}
		copyWant := stringIdentStoredWants()[1]
		copyWant.Query = strings.Replace(copyWant.Query, "ident_view", "ident_copy", 1)
		stringReplaceBoth(t, p, b, c, copyWant)
		var roundtrip strings.Builder
		if err := stuntgres.Dump(p.Ctx, p.Native, &roundtrip); err != nil {
			t.Fatal(err)
		}
		if roundtrip.String() != dump.String() {
			t.Fatal("canonical redump changed")
		}
		bad := `CREATE TABLE ident_bad(a text[]); INSERT INTO ident_bad VALUES(parse_ident('ok')); INSERT INTO ident_bad VALUES(parse_ident('a.'));`
		d := stringIdentDiagnostic("a.", `No valid identifier after ".".`)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &d)
		var after strings.Builder
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != roundtrip.String() {
			t.Fatal("failed Restore published prefix")
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ident_stored(id) VALUES(3)", 1))
		stringReplaceBoth(t, p, b, c, fixture.Select("SELECT a,g FROM ident_stored WHERE id=3", []string{"a", "g"}, []string{"_TEXT", "_TEXT"}, [][]any{{`{d,e}`, `{a,b}`}}))
		// Direct Restore SQL, rather than only serialized stored expressions,
		// exercises the existing nonstored scalar admission whitelist.
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(`CREATE TABLE ident_restored(a text[]); INSERT INTO ident_restored VALUES(parse_ident(strict=>false,str=>'R.S[]'));`)); err != nil {
			t.Fatal(err)
		}
		s := fixture.ArrayReferenceSelect("SELECT a AS ids FROM ident_restored", []string{"ids"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{`{r,s}`}}, b.PG)
		checkArrayMetadata := s.CheckMetadata
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			checkArrayMetadata(t, rows)
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if nullable, known := columns[0].Nullable(); !nullable || !known {
				t.Fatalf("nullable stored column: %v/%v", nullable, known)
			}
		}
		stringReplaceBoth(t, p, b, c, s)
	})
}

func TestReferenceStringIdentFeatureSmoke(t *testing.T) {
	stringIdentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		stringReplaceBoth(t, p, b, c, stringIdentSelect(`SELECT parse_ident('"SomeSchema".someTable') AS ids`, b, `{SomeSchema,sometable}`))
		stringReplaceBoth(t, p, b, c, stringIdentSelect(`SELECT parse_ident('"a""b".C') AS ids`, b, `{"a\"b",c}`))
		stringReplaceBoth(t, p, b, c, stringIdentSelect(`SELECT parse_ident(strict=>false,str=>'A.B[]') AS ids`, b, `{a,b}`))
		stringReplaceBoth(t, p, b, c, stringIdentSelect(`SELECT parse_ident($1,$2) AS ids`, b, `{a,b}`, "A.B", true))
		stringReplaceBoth(t, p, b, c, stringIdentSelect(`SELECT parse_ident($1,$2) AS ids`, b, nil, `"`, nil))
		s := fixture.ErrorDiagnostic("SELECT parse_ident($1,$2)", stringIdentDiagnostic("A.", `No valid identifier after ".".`), "A.", true)
		s.SelectRows = true
		s.ErrorStage = "execute"
		stringReplaceBoth(t, p, b, c, s)
	})
}
