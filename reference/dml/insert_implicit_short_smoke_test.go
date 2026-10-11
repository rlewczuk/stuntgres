package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

// Migrates the complete public TestInsertImplicitSmoke union. The detailed
// matrix remains in the existing Short family; no native-only SQL copy remains.
func TestReferenceInsertImplicitShortFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.InsertSelectRun(t, p.Ctx, b.DB, b.Recorder, s) }
			peer := p.Memory
			if b.PG {
				var err error
				peer, err = sql.Open("stuntgres_reference_postgres", fixture.DSN)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
			}
			run(fixture.Exec("CREATE TABLE t(id INT NOT NULL,big BIGINT,label TEXT,flag BOOL)", 0))
			run(fixture.Exec("INSERT INTO t VALUES (1)", 1))
			run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES ($1,$2)", 1, 2, "20")))
			run(fixture.Exec("INSERT INTO t(label,id) VALUES ('reordered',3)", 1))
			want := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(1), nil, nil, nil}, {int64(2), int64(20), nil, nil}, {int64(3), nil, "reordered", nil}})
			want.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				referenceInsertShortMetadata(t, rows, b.PG, want.Types, []bool{false, true, true, true})
			}
			run(want)
			fixture.InsertSelectRun(t, p.Ctx, peer, b.Recorder, want)
			for _, tc := range []struct{ q, code, message, token string }{
				{"INSERT INTO t VALUES (4),('oops')", "22P02", `invalid input syntax for type integer: "oops"`, "'oops'"},
				{"INSERT INTO t VALUES (4),(5,6)", "42601", "VALUES lists must all be the same length", "5,6"},
			} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fixture.Position(utf8.RuneCountInString(tc.q[:strings.Index(tc.q, tc.token)]) + 1)})
				s.ErrorStage = "execute"
				run(s)
				run(want)
			}
			s := fixture.ImplicitError("INSERT INTO t VALUES (NULL),(2147483648)", fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
			s.Prepared, s.ErrorStage = true, "execute"
			run(s)
			run(want)
			run(fixture.Exec(`CREATE TABLE quoted("a""b" INT,label TEXT,flag BOOL)`, 0))
			run(fixture.Exec("INSERT INTO quoted VALUES(99,'sentinel',TRUE)", 1))
			var before bytes.Buffer
			if !b.PG {
				if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
					t.Fatal(err)
				}
			}
			q := `/* é */ INSERT INTO quoted VALUES(TRUE)`
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42804", Message: `column "a"b" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression.", Position: fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "TRUE")]) + 1)})
				s.Prepared, s.ErrorStage = prepared, map[bool]string{false: "execute", true: "prepare"}[prepared]
				run(s)
				fixture.InsertSelectRun(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT * FROM quoted", []string{`a"b`, "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(99), "sentinel", true}}))
				if !b.PG {
					var after bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(before.Bytes(), after.Bytes()) {
						t.Fatal("quoted mismatch changed complete catalog")
					}
				}
			}
			run(fixture.Exec("CREATE TABLE required(id INT,flag BOOL NOT NULL)", 0))
			run(fixture.ImplicitError("INSERT INTO required VALUES (1)", fixture.Diagnostic{Code: "23502", Message: `null value in column "flag" of relation "required" violates not-null constraint`, Detail: "Failing row contains (1, null).", Schema: "public", Table: "required", Column: "flag"}))
			run(fixture.Select("SELECT * FROM required", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{}))
			if !b.PG {
				insertImplicitSmokeControls(t, p, peer, want)
			}
		})
	}
}

func insertImplicitSmokeControls(t *testing.T, p *fixture.Pair, peer *sql.DB, want fixture.Step) {
	t.Helper()
	retained, err := p.Native.QueryContext(p.Ctx, want.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
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
	var actual [][]any
	for retained.Next() {
		values := make([]any, 4)
		if err := retained.Scan(&values[0], &values[1], &values[2], &values[3]); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, values)
	}
	if err := retained.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, want.Rows) {
		t.Fatalf("retained short rows=%#v want=%#v", actual, want.Rows)
	}
	fixture.InsertSelectRun(t, p.Ctx, peer, p.MemoryRecord, want)
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("short smoke dump changed after restore")
	}
	err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE staged(v INT,label TEXT); INSERT INTO staged VALUES(1); CREATE TABLE required_again(id INT,flag BOOL NOT NULL); INSERT INTO required_again VALUES(2);"))
	d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "flag" of relation "required_again" violates not-null constraint`, Detail: "Failing row contains (2, null).", Schema: "public", Table: "required_again", Column: "flag"}
	fixture.AssertDiagnostic(t, err, &d)
	fixture.InsertSelectRun(t, p.Ctx, peer, p.MemoryRecord, want)
	var preserved bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &preserved); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), preserved.Bytes()) {
		t.Fatal("failed short staging Restore changed complete catalog")
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE restored(id INT,label TEXT); INSERT INTO restored VALUES(8);")); err != nil {
		t.Fatal(err)
	}
	var id int64
	var label sql.NullString
	if err := peer.QueryRowContext(p.Ctx, "SELECT * FROM restored").Scan(&id, &label); err != nil {
		t.Fatal(err)
	}
	if id != 8 || label.Valid {
		t.Fatalf("hand-authored short Restore=%d %+v", id, label)
	}
}
