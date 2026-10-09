package reference_test

import (
	"testing"
	"time"
)

func TestReferenceDateFullDiagnosticsAtomicityAndBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceDateRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE d(v DATE)", 0))
			for _, tc := range []struct{ input, code, primary, hint string }{{"bad", "22007", "invalid input syntax for type date", ""}, {"1900-02-29", "22008", "date/time field value out of range", ""}, {"1500-02-29", "22008", "date/time field value out of range", ""}, {"2100-02-29", "22008", "date/time field value out of range", ""}, {"0002-02-29 BC", "22008", "date/time field value out of range", ""}, {"2001-01-32", "22008", "date/time field value out of range", "Perhaps you need a different \"DateStyle\" setting."}, {"0000-01-01", "22008", "date/time field value out of range", ""}, {"4714-11-23 BC", "22008", "date out of range", ""}, {"5874898-01-01", "22008", "date out of range", ""}} {
				for _, prepared := range []bool{false, true} {
					want := referenceDiagnostic{Code: tc.code, Message: tc.primary + ": \"" + tc.input + "\"", Hint: tc.hint, Position: "13"}
					s := referenceImplicitError("SELECT DATE '"+tc.input+"' FROM d", want)
					s.prepared = prepared
					s.selectRows = true
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					run(s)
					want.Position = ""
					want.Context = "unnamed portal parameter $1 = '...'"
					s = referenceImplicitError("INSERT INTO d VALUES($1)", want)
					s.args = []any{tc.input}
					s.prepared = prepared
					s.errorStage = "execute"
					run(s)
				}
			}
			for _, tc := range []struct{ sql, pos string }{{"/*é*/ INSERT INTO d VALUES('bad')", "28"}, {"/*é*/ SELECT DATE 'bad' FROM d", "19"}, {"SELECT v FROM d WHERE v='bad'", "25"}} {
				s := referenceImplicitError(tc.sql, referenceDiagnostic{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: tc.pos})
				s.selectRows = tc.sql != "/*é*/ INSERT INTO d VALUES('bad')"
				run(s)
				s.prepared = true
				s.errorStage = "prepare"
				run(s)
			}
			run(referenceExec("INSERT INTO d VALUES('infinity')", 1))
			s := referenceImplicitError("INSERT INTO d VALUES('2000-02-29'),('bad')", referenceDiagnostic{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: "37"})
			run(s)
			want := referenceDiagnostic{Code: "22007", Message: `invalid input syntax for type date: "bad"`}
			want.Context = "unnamed portal parameter $2 = '...'"
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
				if tc.typ == "timestamp without time zone" {
					// DATE/TIMESTAMP comparison is supported on both backends.
					s = referenceSelect("SELECT v FROM d WHERE v="+tc.literal, []string{"v"}, []string{"DATE"}, nil)
					run(s) // The sentinel infinity is not equal to finite midnight.
					s.prepared = true
					run(s)
					continue
				}
				s = referenceImplicitError("SELECT v FROM d WHERE v="+tc.literal, referenceDiagnostic{Code: "42883", Message: "operator does not exist: date = " + tc.typ, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "24"})
				s.selectRows = true
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

func TestReferenceDateTimeParameterRangeDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	// Run independent backend subtests so a native regression does not hide
	// the pinned PostgreSQL oracle's complete expected diagnostic records.
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceDateRunStep(t, p.ctx, b.db, b.recorder, s) }
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
									run := func(s referenceStep) { referenceDateRunStep(t, p.ctx, b.db, b.recorder, s) }
									want := referenceDiagnostic{Code: "22008", Message: tc.message}
									want.Context = "unnamed portal parameter $1 = '...'"
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

func TestReferenceDateDiagnosticsAndAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceDateRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE d(v DATE)", 0))
			run(referenceSelect("SELECT v FROM d", []string{"v"}, []string{"DATE"}, nil))
			run(referenceExec("CREATE TABLE empty_date(v DATE)", 0))
			s := referenceImplicitError("SELECT DATE 'bad' FROM empty_date", referenceDiagnostic{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: "13"})
			s.prepared, s.selectRows, s.errorStage = true, true, "prepare"
			run(s)
			for _, tc := range []struct{ input, code, primary, hint string }{
				{"bad", "22007", "invalid input syntax for type date", ""},
				{"2001-02-29", "22008", "date/time field value out of range", ""},
				{"0000-01-01", "22008", "date/time field value out of range", ""},
				{"2001-13-01", "22008", "date/time field value out of range", "Perhaps you need a different \"DateStyle\" setting."},
				{"4714-11-23 BC", "22008", "date out of range", ""},
				{"5874898-01-01", "22008", "date out of range", ""},
			} {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError("INSERT INTO d VALUES('"+tc.input+"')", referenceDiagnostic{Code: tc.code, Message: tc.primary + `: "` + tc.input + `"`, Hint: tc.hint, Position: "22"})
					s.prepared, s.errorStage = prepared, "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					run(s)
					run(referenceSelect("SELECT v FROM d", []string{"v"}, []string{"DATE"}, nil))
				}
			}
			run(referenceExec("INSERT INTO d VALUES('2000-01-01')", 1))
			var value any = "2000-01-01"
			if b.pg {
				value = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			}
			unchanged := referenceSelect("SELECT v FROM d", []string{"v"}, []string{"DATE"}, [][]any{{value}})
			query := "INSERT INTO d VALUES($1)"
			param, err := b.db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := param.Close(); err != nil {
					t.Error(err)
				}
			}()
			for _, tc := range dateReferenceBadInputs {
				t.Run(tc.input, func(t *testing.T) {
					want := referenceDiagnostic{Code: tc.code, Message: tc.primary + `: "` + tc.input + `"`, Hint: tc.hint, Position: "22"}
					for _, prepared := range []bool{false, true} {
						s := referenceImplicitError("INSERT INTO d VALUES('"+tc.input+"')", want)
						s.prepared, s.errorStage = prepared, "execute"
						if prepared {
							s.errorStage = "prepare"
						}
						run(s)
						run(unchanged)
					}
					want.Position, want.Context = "", "unnamed portal parameter $1 = '...'"
					for _, held := range []bool{false, true} {
						x := referenceExecutor(b.db)
						if held {
							x = referenceHeldStatement{referenceExecutor: b.db, statement: param, query: query}
						}
						s := referenceImplicitError(query, want, tc.input)
						s.errorStage = "execute"
						referenceDateRunStep(t, p.ctx, x, b.recorder, s)
						run(unchanged)
					}
				})
			}
			for _, tc := range []struct {
				query, position, context string
				args                     []any
			}{
				{"/*é*/ INSERT INTO d VALUES('bad')", "28", "", nil},
				{"INSERT INTO d VALUES('2000-02-29'),('bad')", "37", "", nil},
				{"INSERT INTO d VALUES($1),($2)", "", "unnamed portal parameter $2 = '...'", []any{"2000-02-29", "bad"}},
			} {
				s := referenceImplicitError(tc.query, referenceDiagnostic{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: tc.position, Context: tc.context}, tc.args...)
				run(s)
				run(unchanged)
			}
			run(referenceSelect("SELECT v FROM empty_date", []string{"v"}, []string{"DATE"}, nil))
		})
	}
}
