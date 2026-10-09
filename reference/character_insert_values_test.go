package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceCharacterSourceValues(t *testing.T) {
	p := newReferenceScenario(t)
	for _, tc := range []struct {
		first, second string
		want          [][]any
	}{
		{"CAST('b  ' AS text)", "CAST('a ' AS varchar)", [][]any{{"a "}, {"b  "}}},
		{"CAST('a ' AS varchar)", "CAST('b  ' AS text)", [][]any{{"a "}, {"b  "}}},
		{"CAST('b  ' AS text)", "CAST('a ' AS char(2))", [][]any{{"a"}, {"b  "}}},
		{"CAST('a ' AS char(2))", "CAST('b  ' AS text)", [][]any{{"a"}, {"b"}}},
		{"CAST('b  ' AS char(3))", "CAST('a ' AS varchar)", [][]any{{"a"}, {"b"}}},
		{"CAST('a ' AS varchar)", "CAST('b  ' AS char(3))", [][]any{{"a "}, {"b"}}},
		{"CAST('b' AS char(2))", "CAST('a' AS char(3))", [][]any{{"a"}, {"b"}}},
		{"CAST('b ' AS varchar(2))", "CAST('a ' AS varchar(2))", [][]any{{"a "}, {"b "}}},
	} {
		for _, prepared := range []bool{false, true} {
			p.step(t, referenceExec("CREATE TABLE dst(v TEXT)", 0))
			s := referenceExec("INSERT INTO dst VALUES("+tc.first+"),("+tc.second+") ORDER BY 1", 2)
			s.prepared = prepared
			for _, backend := range p.backends() {
				if backend.pg {
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				} else {
					insertSelectRun(t, p.ctx, backend.db, backend.recorder, s)
				}
			}
			p.step(t, referenceSelect("SELECT v FROM dst ORDER BY v", []string{"v"}, []string{"TEXT"}, tc.want))
			for _, backend := range p.backends() {
				referenceCharacterMetadata(t, p, backend.db, "SELECT v FROM dst", prepared, !backend.pg, []string{"v"}, []string{"TEXT"}, []int64{-1}, []referenceCharacterNullability{{true, true}})
			}
			p.step(t, referenceExec("DROP TABLE dst", 0))
		}
	}
}

func TestReferenceCharacterSourceValuesHeldParameters(t *testing.T) {
	p := newReferenceScenario(t)
	for _, source := range []string{"bpchar", "varchar"} {
		for _, prepared := range []bool{false, true} {
			p.step(t, referenceExec("CREATE TABLE dst(v TEXT)", 0))
			q := "INSERT INTO dst VALUES(CAST($1 AS text)),(CAST($2 AS " + source + ")) ORDER BY 1"
			for _, backend := range p.backends() {
				func() {
					var stmt *sql.Stmt
					var executor referenceExecutor = backend.db
					if prepared {
						var err error
						stmt, err = backend.db.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
					}
					for _, args := range [][]any{{"b  ", "a "}, {nil, "c "}} {
						s := referenceExec(q, 2, args...)
						if backend.pg {
							referenceRunStep(t, p.ctx, executor, backend.recorder, s)
						} else {
							insertSelectRun(t, p.ctx, executor, backend.recorder, s)
						}
					}
				}()
			}
			want := [][]any{{"a "}, {"b  "}, {"c "}, {nil}}
			if source == "bpchar" {
				want = [][]any{{"a"}, {"b  "}, {"c"}, {nil}}
			}
			p.step(t, referenceSelect("SELECT v FROM dst ORDER BY v", []string{"v"}, []string{"TEXT"}, want))
			p.step(t, referenceExec("DROP TABLE dst", 0))
		}
	}
}

func TestReferenceCharacterSourceValuesAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE dst(v VARCHAR(2))", 0))
	p.step(t, referenceExec("INSERT INTO dst VALUES('s')", 1))
	q := "INSERT INTO dst VALUES(CAST('aa' AS text)),(CAST('zzx' AS varchar)) ORDER BY 1"
	for _, backend := range p.backends() {
		for _, prepared := range []bool{false, true} {
			var result sql.Result
			var err error
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			if prepared {
				stmt, prepareErr := backend.db.PrepareContext(ctx, q)
				if prepareErr != nil {
					t.Fatal("assignment rejected at Prepare", prepareErr)
				}
				result, err = stmt.ExecContext(ctx)
				stmt.Close()
			} else {
				result, err = backend.db.ExecContext(ctx, q)
			}
			if result != nil {
				t.Fatal("failed source insert exposed result")
			}
			referenceState(t, err, "22001")
			referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22001", Message: "value too long for type character varying(2)"})
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT v FROM dst", []string{"v"}, []string{"VARCHAR"}, [][]any{{"s"}}))
		}
	}
}

func TestReferenceCharacterSourceValuesCrossTypmod(t *testing.T) {
	p := newReferenceScenario(t)
	for _, tc := range []struct {
		first, second string
		want          [][]any
	}{
		{"CAST('b' AS char(2))", "CAST('a' AS varchar(2))", [][]any{{"a"}, {"b "}}},
		{"CAST('a' AS varchar(2))", "CAST('b' AS char(2))", [][]any{{"a"}, {"b"}}},
		{"CAST('b' AS char(2))", "CAST('a' AS char(2))", [][]any{{"a "}, {"b "}}},
		{"CAST('b' AS varchar(2))", "CAST('a' AS varchar(2))", [][]any{{"a"}, {"b"}}},
		{"CAST('b' AS char(3))", "CAST('a' AS varchar(2))", [][]any{{"a"}, {"b  "}}},
		{"CAST('a' AS varchar(2))", "CAST('b' AS char(3))", [][]any{{"a"}, {"b"}}},
		{"CAST('b' AS char(2))", "NULL", [][]any{{"b "}, {nil}}},
	} {
		for _, prepared := range []bool{false, true} {
			p.step(t, referenceExec("CREATE TABLE dst(v BPCHAR)", 0))
			s := referenceExec("INSERT INTO dst VALUES("+tc.first+"),("+tc.second+") ORDER BY 1", 2)
			s.prepared = prepared
			for _, backend := range p.backends() {
				if backend.pg {
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				} else {
					insertSelectRun(t, p.ctx, backend.db, backend.recorder, s)
				}
			}
			p.step(t, referenceSelect("SELECT v FROM dst ORDER BY v", []string{"v"}, []string{"BPCHAR"}, tc.want))
			for _, backend := range p.backends() {
				referenceCharacterMetadata(t, p, backend.db, "SELECT v FROM dst", prepared, !backend.pg, []string{"v"}, []string{"BPCHAR"}, []int64{-1}, []referenceCharacterNullability{{true, true}})
			}
			p.step(t, referenceExec("DROP TABLE dst", 0))
		}
	}
}
