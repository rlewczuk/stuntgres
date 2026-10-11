package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceDriverBatchParsePrepared(t *testing.T) {
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		batchSeed(t, p, b, a)
		for _, q := range []string{"INSERT INTO batch_t VALUES(9); COMMIT; SELECT i FROM batch_t WHERE", "/*é*/ INSERT INTO batch_t VALUES(9); COMMIT; SELECT i FROM batch_t WHERE"} {
			w := batchDiag("42601", "syntax error at end of input")
			w.Position = fixture.Position(utf8.RuneCountInString(q) + 1)
			batchError(t, p, a, q, w)
			batchValues(t, p, b, peer, "SELECT i FROM batch_t", 7)
		}
		for _, tc := range []struct{ q, msg, token string }{{"INSERT INTO batch_t VALUES(9); SELECT $tag$broken", `unterminated dollar-quoted string at or near "$tag$broken"`, "$tag$"}, {"INSERT INTO batch_t VALUES(9); /* broken", `unterminated /* comment at or near "/* broken"`, "/*"}} {
			w := batchDiag("42601", tc.msg)
			w.Position = batchPosition(tc.q, tc.token)
			batchError(t, p, a, tc.q, w)
			batchValues(t, p, b, a, "SELECT i FROM batch_t", 7)
		}
		batchSets(t, batchQuery(t, p, a, "SELECT ';' AS s; /* outer /* ; */ nested */ SELECT $$a;b$$ AS s"), batchSet{[]string{"s"}, []string{"TEXT"}, [][]any{{";"}}, false}, batchSet{[]string{"s"}, []string{"TEXT"}, [][]any{{"a;b"}}, false})
		q := "INSERT INTO batch_t VALUES($1); SELECT i FROM batch_t"
		w := batchDiag("42601", "cannot insert multiple commands into a prepared statement")
		s, err := a.PrepareContext(p.Ctx, q)
		if s != nil {
			s.Close()
			t.Fatal("Prepare returned handle")
		}
		fixture.AssertDiagnostic(t, err, &w)
		r, err := a.ExecContext(p.Ctx, q, 9)
		if r != nil {
			t.Fatal("extended error returned Result")
		}
		fixture.AssertDiagnostic(t, err, &w)
		rows, err := a.QueryContext(p.Ctx, q, 9)
		if rows != nil {
			rows.Close()
			t.Fatal("extended error returned Rows")
		}
		fixture.AssertDiagnostic(t, err, &w)
		s, err = b.DB.Prepare(q)
		if s != nil {
			s.Close()
			t.Fatal("legacy handle")
		}
		fixture.AssertDiagnostic(t, err, &w)
		_, err = b.DB.Exec(q, 9)
		fixture.AssertDiagnostic(t, err, &w)
		rows, err = b.DB.Query(q, 9)
		if rows != nil {
			rows.Close()
			t.Fatal("legacy Rows")
		}
		fixture.AssertDiagnostic(t, err, &w)
		batchValues(t, p, b, a, "SELECT i FROM batch_t", 7)
		s, err = a.PrepareContext(p.Ctx, ";; SELECT i FROM batch_t WHERE i=$1 ;; ")
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		rows, err = s.QueryContext(p.Ctx, 7)
		if err != nil {
			t.Fatal(err)
		}
		batchSets(t, rows, batchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}, false})
		malformed := "SELECT $1; SELECT i FROM batch_t WHERE"
		want := batchDiag("42601", "syntax error at end of input")
		want.Position = fixture.Position(utf8.RuneCountInString(malformed) + 1)
		_, err = a.ExecContext(p.Ctx, malformed, 7)
		fixture.AssertDiagnostic(t, err, &want)
		batchExec(t, p, b, a, "BEGIN", 0)
		fail := "SELECT missing FROM batch_t"
		want = batchDiag("42703", `column "missing" does not exist`)
		want.Position = "8"
		batchError(t, p, a, fail, want)
		s, err = a.PrepareContext(p.Ctx, "SELECT i FROM batch_t; SELECT i FROM batch_t")
		if s != nil {
			s.Close()
			t.Fatal("aborted handle")
		}
		fixture.AssertDiagnostic(t, err, &w)
		batchExec(t, p, b, a, "ROLLBACK", 0)
	})
}
func TestReferenceDriverBatchErrorTimingRollback(t *testing.T) {
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		batchSeed(t, p, b, a)
		q := "INSERT INTO batch_t VALUES(1); SELECT missing FROM batch_t; INSERT INTO batch_t VALUES(2)"
		w := batchDiag("42703", `column "missing" does not exist`)
		w.Position = batchPosition(q, "missing")
		batchError(t, p, a, q, w)
		batchValues(t, p, b, peer, "SELECT i FROM batch_t", 7)
		q = "CREATE TABLE new_t(i INT); INSERT INTO new_t VALUES(1); SELECT i FROM absent"
		w = batchDiag("42P01", `relation "absent" does not exist`)
		w.Position = batchPosition(q, "absent")
		batchError(t, p, a, q, w)
		q = "SELECT i FROM new_t"
		w = batchDiag("42P01", `relation "new_t" does not exist`)
		w.Position = "15"
		batchError(t, p, a, q, w)
		for _, empty := range []bool{false, true} {
			for _, closeEarly := range []bool{false, true} {
				first := "SELECT i FROM batch_t"
				want := [][]any{{int64(7)}}
				if empty {
					first += " WHERE i=99"
					want = nil
				}
				q = first + "; INSERT INTO batch_t VALUES(1); SELECT missing FROM batch_t"
				w = batchDiag("42703", `column "missing" does not exist`)
				w.Position = batchPosition(q, "missing")
				rows, err := a.QueryContext(p.Ctx, q)
				if err != nil {
					t.Fatal("completed first set lost", err)
				}
				if closeEarly {
					err = rows.Close()
					fixture.AssertDiagnostic(t, err, &w)
				} else {
					batchReadSet(t, rows, batchSet{[]string{"i"}, []string{"INT4"}, want, false})
					fixture.AssertDiagnostic(t, rows.Err(), &w)
					rows.Close()
				}
				batchValues(t, p, b, peer, "SELECT i FROM batch_t", 7)
			}
		}
		q = "INSERT INTO batch_t VALUES(NULL); INSERT INTO batch_t VALUES(2)"
		w = batchDiag("23502", `null value in column "i" of relation "batch_t" violates not-null constraint`)
		w.Detail = "Failing row contains (null)."
		w.Schema = "public"
		w.Table = "batch_t"
		w.Column = "i"
		batchError(t, p, a, q, w)
		batchValues(t, p, b, a, "SELECT i FROM batch_t", 7)
		q = "SELECT missing FROM batch_t; SELECT i FROM batch_t"
		w = batchDiag("42703", `column "missing" does not exist`)
		w.Position = "8"
		rows, err := a.QueryContext(p.Ctx, q)
		if rows != nil {
			rows.Close()
			t.Fatal("first failure produced Rows")
		}
		fixture.AssertDiagnostic(t, err, &w)
	})
}
