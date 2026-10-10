package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

// Migrates the complete public TestInsertImplicitSmoke union. The detailed
// matrix remains in the existing Short family; no native-only SQL copy remains.
func TestReferenceInsertImplicitShortFeatureSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { insertSelectRun(t, p.ctx, b.db, b.recorder, s) }
			peer := p.memory
			if b.pg {
				var err error
				peer, err = sql.Open("stuntgres_reference_postgres", referenceDSN)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
			}
			run(referenceExec("CREATE TABLE t(id INT NOT NULL,big BIGINT,label TEXT,flag BOOL)", 0))
			run(referenceExec("INSERT INTO t VALUES (1)", 1))
			run(referencePrepared(referenceExec("INSERT INTO t VALUES ($1,$2)", 1, 2, "20")))
			run(referenceExec("INSERT INTO t(label,id) VALUES ('reordered',3)", 1))
			want := referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(1), nil, nil, nil}, {int64(2), int64(20), nil, nil}, {int64(3), nil, "reordered", nil}})
			want.checkMetadata = func(t *testing.T, rows *sql.Rows) {
				referenceInsertShortMetadata(t, rows, b.pg, want.types, []bool{false, true, true, true})
			}
			run(want)
			insertSelectRun(t, p.ctx, peer, b.recorder, want)
			for _, tc := range []struct{ q, code, message, token string }{
				{"INSERT INTO t VALUES (4),('oops')", "22P02", `invalid input syntax for type integer: "oops"`, "'oops'"},
				{"INSERT INTO t VALUES (4),(5,6)", "42601", "VALUES lists must all be the same length", "5,6"},
			} {
				s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: referencePosition(utf8.RuneCountInString(tc.q[:strings.Index(tc.q, tc.token)]) + 1)})
				s.errorStage = "execute"
				run(s)
				run(want)
			}
			s := referenceImplicitError("INSERT INTO t VALUES (NULL),(2147483648)", referenceDiagnostic{Code: "22003", Message: "integer out of range"})
			s.prepared, s.errorStage = true, "execute"
			run(s)
			run(want)
			run(referenceExec(`CREATE TABLE quoted("a""b" INT,label TEXT,flag BOOL)`, 0))
			run(referenceExec("INSERT INTO quoted VALUES(99,'sentinel',TRUE)", 1))
			var before bytes.Buffer
			if !b.pg {
				if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
					t.Fatal(err)
				}
			}
			q := `/* é */ INSERT INTO quoted VALUES(TRUE)`
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "42804", Message: `column "a"b" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression.", Position: referencePosition(utf8.RuneCountInString(q[:strings.Index(q, "TRUE")]) + 1)})
				s.prepared, s.errorStage = prepared, map[bool]string{false: "execute", true: "prepare"}[prepared]
				run(s)
				insertSelectRun(t, p.ctx, peer, b.recorder, referenceSelect("SELECT * FROM quoted", []string{`a"b`, "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(99), "sentinel", true}}))
				if !b.pg {
					var after bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(before.Bytes(), after.Bytes()) {
						t.Fatal("quoted mismatch changed complete catalog")
					}
				}
			}
			run(referenceExec("CREATE TABLE required(id INT,flag BOOL NOT NULL)", 0))
			run(referenceImplicitError("INSERT INTO required VALUES (1)", referenceDiagnostic{Code: "23502", Message: `null value in column "flag" of relation "required" violates not-null constraint`, Detail: "Failing row contains (1, null).", Schema: "public", Table: "required", Column: "flag"}))
			run(referenceSelect("SELECT * FROM required", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{}))
			if !b.pg {
				insertImplicitSmokeControls(t, p, peer, want)
			}
		})
	}
}

func insertImplicitSmokeControls(t *testing.T, p *referencePair, peer *sql.DB, want referenceStep) {
	t.Helper()
	retained, err := p.native.QueryContext(p.ctx, want.query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
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
	if !reflect.DeepEqual(actual, want.rows) {
		t.Fatalf("retained short rows=%#v want=%#v", actual, want.rows)
	}
	insertSelectRun(t, p.ctx, peer, p.memoryRecord, want)
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("short smoke dump changed after restore")
	}
	err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE staged(v INT,label TEXT); INSERT INTO staged VALUES(1); CREATE TABLE required_again(id INT,flag BOOL NOT NULL); INSERT INTO required_again VALUES(2);"))
	d := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "flag" of relation "required_again" violates not-null constraint`, Detail: "Failing row contains (2, null).", Schema: "public", Table: "required_again", Column: "flag"}
	referenceAssertDiagnostic(t, err, &d)
	insertSelectRun(t, p.ctx, peer, p.memoryRecord, want)
	var preserved bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &preserved); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), preserved.Bytes()) {
		t.Fatal("failed short staging Restore changed complete catalog")
	}
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE restored(id INT,label TEXT); INSERT INTO restored VALUES(8);")); err != nil {
		t.Fatal(err)
	}
	var id int64
	var label sql.NullString
	if err := peer.QueryRowContext(p.ctx, "SELECT * FROM restored").Scan(&id, &label); err != nil {
		t.Fatal(err)
	}
	if id != 8 || label.Valid {
		t.Fatalf("hand-authored short Restore=%d %+v", id, label)
	}
}
