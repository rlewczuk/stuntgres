//go:build reference

package reference_test

import "testing"

func TestReferenceDatePostgresTimestampComparisons(t *testing.T) {
	p := newReferencePair(t)
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s) }
	run(referenceExec("CREATE TABLE d(id INTEGER,v DATE)", 0))
	run(referenceExec("INSERT INTO d VALUES(1,'2000-01-01'),(2,'-infinity'),(3,'infinity'),(4,NULL)", 4))
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			sql  string
			rows [][]any
		}{
			{"SELECT v=TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-01'=v,v<TIMESTAMP '2000-01-01 12:00',TIMESTAMP '2000-01-01 12:00'>v FROM d WHERE id=1", [][]any{{true, true, true, true}}},
			{"SELECT v=TIMESTAMP '-infinity',TIMESTAMP '-infinity'=v,v<TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-01'>v FROM d WHERE id=2", [][]any{{true, true, true, true}}},
			{"SELECT v=TIMESTAMP 'infinity',TIMESTAMP 'infinity'=v,v>TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-01'<v FROM d WHERE id=3", [][]any{{true, true, true, true}}},
			{"SELECT v=TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-01'=v,v<TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-01'<v FROM d WHERE id=4", [][]any{{nil, nil, nil, nil}}},
		} {
			s := referenceSelect(tc.sql, []string{"?column?", "?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, tc.rows)
			s.prepared = prepared
			run(s)
		}
	}
}

func TestReferenceDateTransactionRelativeInputs(t *testing.T) {
	p := newReferencePair(t)
	// The PostgreSQL oracle baseline is captured inside the same transaction,
	// rather than assuming the wall clock will not cross a civil-day boundary.
	tx, err := p.postgres.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(p.ctx, "CREATE TABLE d(v DATE)"); err != nil {
		t.Fatal(err)
	}
	var today, yesterday, tomorrow any
	if err = tx.QueryRowContext(p.ctx, "SELECT CURRENT_DATE,CURRENT_DATE-1,CURRENT_DATE+1").Scan(&today, &yesterday, &tomorrow); err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.PrepareContext(p.ctx, "SELECT DATE 'today',DATE 'now',DATE 'yesterday',DATE 'tomorrow' FROM d")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if _, err = tx.ExecContext(p.ctx, "INSERT INTO d VALUES('today')"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		var a, b, c, d any
		if err = stmt.QueryRowContext(p.ctx).Scan(&a, &b, &c, &d); err != nil {
			t.Fatal(err)
		}
		if a != today || b != today || c != yesterday || d != tomorrow {
			t.Fatal(a, b, c, d, today, yesterday, tomorrow)
		}
	}
}
