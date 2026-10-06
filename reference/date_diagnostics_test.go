//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
	"time"
)

func TestReferenceDateFullDiagnosticsAtomicityAndBoundaries(t *testing.T) {
	p := newReferencePair(t)
	for _, b := range []struct {
		name   string
		pg     bool
		db     *sql.DB
		record *referenceRecorder
	}{{"native", false, p.memory, p.memoryRecord}, {"postgres", true, p.postgres, p.postgresRecord}} {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
			run(referenceExec("CREATE TABLE d(v DATE)", 0))
			for _, tc := range []struct{ input, code, primary, hint string }{{"bad", "22007", "invalid input syntax for type date", ""}, {"1900-02-29", "22008", "date/time field value out of range", ""}, {"1500-02-29", "22008", "date/time field value out of range", ""}, {"2100-02-29", "22008", "date/time field value out of range", ""}, {"0002-02-29 BC", "22008", "date/time field value out of range", ""}, {"2001-01-32", "22008", "date/time field value out of range", "Perhaps you need a different \"DateStyle\" setting."}, {"0000-01-01", "22008", "date/time field value out of range", ""}, {"4714-11-23 BC", "22008", "date out of range", ""}, {"5874898-01-01", "22008", "date out of range", ""}} {
				for _, prepared := range []bool{false, true} {
					want := referenceDiagnostic{Code: tc.code, Message: tc.primary + ": \"" + tc.input + "\"", Hint: tc.hint, Position: "13"}
					s := referenceImplicitError("SELECT DATE '"+tc.input+"' FROM d", want)
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					run(s)
					want.Position = ""
					if b.pg {
						want.Context = "unnamed portal parameter $1 = '...'"
					}
					s = referenceImplicitError("INSERT INTO d VALUES($1)", want)
					s.args = []any{tc.input}
					s.prepared = prepared
					s.errorStage = "execute"
					run(s)
				}
			}
			for _, tc := range []struct{ sql, pos string }{{"/*é*/ INSERT INTO d VALUES('bad')", "28"}, {"/*é*/ SELECT DATE 'bad' FROM d", "19"}, {"SELECT v FROM d WHERE v='bad'", "25"}} {
				s := referenceImplicitError(tc.sql, referenceDiagnostic{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: tc.pos})
				run(s)
				s.prepared = true
				s.errorStage = "prepare"
				run(s)
			}
			run(referenceExec("INSERT INTO d VALUES('infinity')", 1))
			s := referenceImplicitError("INSERT INTO d VALUES('2000-02-29'),('bad')", referenceDiagnostic{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: "37"})
			run(s)
			want := referenceDiagnostic{Code: "22007", Message: `invalid input syntax for type date: "bad"`}
			if b.pg {
				want.Context = "unnamed portal parameter $2 = '...'"
			}
			s = referenceImplicitError("INSERT INTO d VALUES($1),($2)", want)
			s.args = []any{"2000-02-29", "bad"}
			run(s)
			s.prepared = true
			run(s)
			run(referenceSelect("SELECT v FROM d", []string{"v"}, []string{"DATE"}, [][]any{{"infinity"}}))
			run(referenceExec("CREATE TABLE nn(v DATE NOT NULL)", 0))
			run(referenceImplicitError("INSERT INTO nn VALUES('epoch'),(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "v" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "nn", Column: "v"}))
			run(referenceSelect("SELECT v FROM nn", []string{"v"}, []string{"DATE"}, nil))
			for _, tc := range []struct{ literal, typ string }{{"TIME '00:00'", "time without time zone"}, {"TIMESTAMP '2000-01-01'", "timestamp without time zone"}} {
				if b.pg && tc.typ == "timestamp without time zone" {
					// PostgreSQL has date/timestamp operators; native deliberately
					// keeps the same-type temporal boundary.
					s = referenceSelect("SELECT v FROM d WHERE v="+tc.literal, []string{"v"}, []string{"DATE"}, nil)
					run(s) // The sentinel infinity is not equal to finite midnight.
					s.prepared = true
					run(s)
					continue
				}
				s = referenceImplicitError("SELECT v FROM d WHERE v="+tc.literal, referenceDiagnostic{Code: "42883", Message: "operator does not exist: date = " + tc.typ, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "24"})
				run(s)
				s.prepared = true
				s.errorStage = "prepare"
				run(s)
			}
			s = referenceImplicitError("SELECT $1 FROM d WHERE v=$1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "date versus text", Position: "8"})
			s.prepared = true
			s.errorStage = "prepare"
			run(s)
		})
	}
}

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

func TestReferenceDateTimeParameterRangeDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	// Run independent backend subtests so a native regression does not hide
	// the pinned PostgreSQL oracle's complete expected diagnostic records.
	for _, b := range []struct {
		name   string
		pg     bool
		db     *sql.DB
		record *referenceRecorder
	}{{"native", false, p.memory, p.memoryRecord}, {"postgres", true, p.postgres, p.postgresRecord}} {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
			run(referenceExec("CREATE TABLE d(v DATE)", 0))
			run(referenceExec("INSERT INTO d VALUES('2000-01-01')", 1))
			sentinel := any("2000-01-01")
			if b.pg {
				sentinel = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			}
			unchanged := referenceSelect("SELECT v FROM d ORDER BY v", []string{"v"}, []string{"DATE"}, [][]any{{sentinel}})
			for _, tc := range []struct {
				name, message string
				input         time.Time
			}{
				{"upperUTC", `date out of range: "5874898-01-01 00:00:00Z"`, time.Date(5874898, 1, 1, 0, 0, 0, 0, time.UTC)},
				{"belowMinimumUTC", `date out of range: "4714-11-23 00:00:00Z BC"`, time.Date(-4713, 11, 23, 0, 0, 0, 0, time.UTC)},
				{"yearFieldOverflow", `date/time field value out of range: "2147483648-01-01 00:00:00Z"`, time.Date(2147483648, 1, 1, 0, 0, 0, 0, time.UTC)},
				{"bcYearFieldOverflow", `date/time field value out of range: "2147483648-01-01 00:00:00Z BC"`, time.Date(-2147483647, 1, 1, 0, 0, 0, 0, time.UTC)},
				// DATE discards valid clocks/offsets, not their spelling in
				// a range failure's original temporal input diagnostic.
				{"upperOffset", `date out of range: "5874898-01-01 04:05:06.123456789+02:03:04"`, time.Date(5874898, 1, 1, 4, 5, 6, 123456789, time.FixedZone("east", 7384))},
				{"belowMinimumOffset", `date out of range: "4714-11-23 04:05:06.123456789-02:03:04 BC"`, time.Date(-4713, 11, 23, 4, 5, 6, 123456789, time.FixedZone("west", -7384))},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for _, mode := range []string{"direct", "prepared"} {
						t.Run(mode, func(t *testing.T) {
							for _, statement := range []struct{ name, query string }{
								{"singleRow", "INSERT INTO d VALUES($1)"},
								{"laterRow", "INSERT INTO d VALUES('2000-02-29'),($1)"},
							} {
								t.Run(statement.name, func(t *testing.T) {
									run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
									want := referenceDiagnostic{Code: "22008", Message: tc.message}
									if b.pg {
										want.Context = "unnamed portal parameter $1 = '...'"
									}
									s := referenceImplicitError(statement.query, want, tc.input)
									s.prepared = mode == "prepared"
									s.errorStage = "execute"
									run(s)
									run(unchanged)
								})
							}
						})
					}
				})
			}
		})
	}
}
