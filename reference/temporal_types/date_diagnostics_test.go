package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDateFullDiagnosticsAtomicityAndBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.DateRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE d(v DATE)", 0))
			for _, tc := range []struct{ input, code, primary, hint string }{{"bad", "22007", "invalid input syntax for type date", ""}, {"1900-02-29", "22008", "date/time field value out of range", ""}, {"1500-02-29", "22008", "date/time field value out of range", ""}, {"2100-02-29", "22008", "date/time field value out of range", ""}, {"0002-02-29 BC", "22008", "date/time field value out of range", ""}, {"2001-01-32", "22008", "date/time field value out of range", "Perhaps you need a different \"DateStyle\" setting."}, {"0000-01-01", "22008", "date/time field value out of range", ""}, {"4714-11-23 BC", "22008", "date out of range", ""}, {"5874898-01-01", "22008", "date out of range", ""}} {
				for _, prepared := range []bool{false, true} {
					want := fixture.Diagnostic{Code: tc.code, Message: tc.primary + ": \"" + tc.input + "\"", Hint: tc.hint, Position: "13"}
					s := fixture.ImplicitError("SELECT DATE '"+tc.input+"' FROM d", want)
					s.Prepared = prepared
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
					want.Position = ""
					want.Context = "unnamed portal parameter $1 = '...'"
					s = fixture.ImplicitError("INSERT INTO d VALUES($1)", want)
					s.Args = []any{tc.input}
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
				}
			}
			for _, tc := range []struct{ sql, pos string }{{"/*é*/ INSERT INTO d VALUES('bad')", "28"}, {"/*é*/ SELECT DATE 'bad' FROM d", "19"}, {"SELECT v FROM d WHERE v='bad'", "25"}} {
				s := fixture.ImplicitError(tc.sql, fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: tc.pos})
				s.SelectRows = tc.sql != "/*é*/ INSERT INTO d VALUES('bad')"
				run(s)
				s.Prepared = true
				s.ErrorStage = "prepare"
				run(s)
			}
			run(fixture.Exec("INSERT INTO d VALUES('infinity')", 1))
			s := fixture.ImplicitError("INSERT INTO d VALUES('2000-02-29'),('bad')", fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: "37"})
			run(s)
			want := fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type date: "bad"`}
			want.Context = "unnamed portal parameter $2 = '...'"
			s = fixture.ImplicitError("INSERT INTO d VALUES($1),($2)", want)
			s.Args = []any{"2000-02-29", "bad"}
			run(s)
			s.Prepared = true
			run(s)
			run(fixture.Select("SELECT v FROM d", []string{"v"}, []string{"DATE"}, [][]any{{"infinity"}}))
			run(fixture.Exec("CREATE TABLE nn(v DATE NOT NULL)", 0))
			run(fixture.ImplicitError("INSERT INTO nn VALUES('epoch'),(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "nn", Column: "v"}))
			run(fixture.Select("SELECT v FROM nn", []string{"v"}, []string{"DATE"}, nil))
			for _, tc := range []struct{ literal, typ string }{{"TIME '00:00'", "time without time zone"}, {"TIMESTAMP '2000-01-01'", "timestamp without time zone"}} {
				if tc.typ == "timestamp without time zone" {
					// DATE/TIMESTAMP comparison is supported on both backends.
					s = fixture.Select("SELECT v FROM d WHERE v="+tc.literal, []string{"v"}, []string{"DATE"}, nil)
					run(s) // The sentinel infinity is not equal to finite midnight.
					s.Prepared = true
					run(s)
					continue
				}
				s = fixture.ImplicitError("SELECT v FROM d WHERE v="+tc.literal, fixture.Diagnostic{Code: "42883", Message: "operator does not exist: date = " + tc.typ, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "24"})
				s.SelectRows = true
				run(s)
				s.Prepared = true
				s.ErrorStage = "prepare"
				run(s)
			}
			s = fixture.ImplicitError("SELECT $1 FROM d WHERE v=$1", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "date versus text", Position: "8"})
			s.Prepared = true
			s.ErrorStage = "prepare"
			run(s)
		})
	}
}

func TestReferenceDateTimeParameterRangeDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	// Run independent backend subtests so a native regression does not hide
	// the pinned PostgreSQL oracle's complete expected diagnostic records.
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.DateRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE d(v DATE)", 0))
			run(fixture.Exec("INSERT INTO d VALUES('2000-01-01')", 1))
			sentinel := any("2000-01-01")
			if b.PG {
				sentinel = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			}
			unchanged := fixture.Select("SELECT v FROM d ORDER BY v", []string{"v"}, []string{"DATE"}, [][]any{{sentinel}})
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
									run := func(s fixture.Step) { fixture.DateRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
									want := fixture.Diagnostic{Code: "22008", Message: tc.message}
									want.Context = "unnamed portal parameter $1 = '...'"
									s := fixture.ImplicitError(statement.query, want, tc.input)
									s.Prepared = mode == "prepared"
									s.ErrorStage = "execute"
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
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.DateRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE d(v DATE)", 0))
			run(fixture.Select("SELECT v FROM d", []string{"v"}, []string{"DATE"}, nil))
			run(fixture.Exec("CREATE TABLE empty_date(v DATE)", 0))
			s := fixture.ImplicitError("SELECT DATE 'bad' FROM empty_date", fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: "13"})
			s.Prepared, s.SelectRows, s.ErrorStage = true, true, "prepare"
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
					s := fixture.ImplicitError("INSERT INTO d VALUES('"+tc.input+"')", fixture.Diagnostic{Code: tc.code, Message: tc.primary + `: "` + tc.input + `"`, Hint: tc.hint, Position: "22"})
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
					run(fixture.Select("SELECT v FROM d", []string{"v"}, []string{"DATE"}, nil))
				}
			}
			run(fixture.Exec("INSERT INTO d VALUES('2000-01-01')", 1))
			var value any = "2000-01-01"
			if b.PG {
				value = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			}
			unchanged := fixture.Select("SELECT v FROM d", []string{"v"}, []string{"DATE"}, [][]any{{value}})
			query := "INSERT INTO d VALUES($1)"
			param, err := b.DB.PrepareContext(p.Ctx, query)
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
					want := fixture.Diagnostic{Code: tc.code, Message: tc.primary + `: "` + tc.input + `"`, Hint: tc.hint, Position: "22"}
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError("INSERT INTO d VALUES('"+tc.input+"')", want)
						s.Prepared, s.ErrorStage = prepared, "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						run(s)
						run(unchanged)
					}
					want.Position, want.Context = "", "unnamed portal parameter $1 = '...'"
					for _, held := range []bool{false, true} {
						x := fixture.Executor(b.DB)
						if held {
							x = fixture.HeldStatement{Executor: b.DB, Statement: param, SQL: query}
						}
						s := fixture.ImplicitError(query, want, tc.input)
						s.ErrorStage = "execute"
						fixture.DateRunStep(t, p.Ctx, x, b.Recorder, s)
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
				s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: tc.position, Context: tc.context}, tc.args...)
				run(s)
				run(unchanged)
			}
			run(fixture.Select("SELECT v FROM empty_date", []string{"v"}, []string{"DATE"}, nil))
		})
	}
}
