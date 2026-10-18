package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func datetimeFormattingReviewQuery(query string, prepared bool, want any, diagnostic *fixture.Diagnostic) fixture.Step {
	s := fixture.Select(query, []string{"result"}, []string{"TEXT"}, [][]any{{want}})
	if diagnostic != nil {
		s = fixture.ErrorDiagnostic(query, *diagnostic)
		s.SelectRows = true
		s.ErrorStage = "execute"
	}
	if prepared {
		s = fixture.Prepared(s)
	}
	return s
}

func TestReferenceDatetimeFormattingReviewWhitespaceDemand(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		integer := func(field string) *fixture.Diagnostic {
			return datetimeFormattingDiagnostic("22007", fmt.Sprintf("invalid value %q for %q", "", field), "Value must be an integer.", "")
		}
		name := func(field string) *fixture.Diagnostic {
			return datetimeFormattingDiagnostic("22007", fmt.Sprintf("invalid value %q for %q", "", field), "The given value did not match any of the allowed values for this field.", "")
		}
		short := datetimeFormattingDiagnostic("22007", `source string too short for "YYYY" formatting field`, "Field requires 4 characters, but only 0 remain.", `If your source string is not fixed-width, try using the "FM" modifier.`)
		for _, c := range []struct {
			input, mask, date string
			diagnostic        *fixture.Diagnostic
		}{
			{"", "YYYY", "0001-01-01 BC", nil}, {"", "MON", "0001-01-01 BC", nil}, {"", "FXYYYYMMDD", "0001-01-01 BC", nil},
			{"   ", "YYYY", "", integer("YYYY")}, {"\t\r\n", "YYYY", "", integer("YYYY")},
			{"   ", "YYYYMMDD", "", short}, {"   ", "FMYYYYMMDD", "", integer("YYYY")},
			{"   ", "MON", "", name("MON")}, {"   ", "MONTH", "", name("MONTH")}, {"   ", "DAY", "", name("DAY")}, {"   ", "RM", "", name("RM")},
			{"   ", "DY", "", name("DY")}, {"\t\n", "DY YYYY", "", name("DY")}, {"   ", "MON YYYY", "", name("MON")},
			{"   ", "FXYYYY", "", integer("YYYY")}, {"   ", "FXMON", "", name("MON")}, {"   ", "FXYYYYMMDD", "", short},
			{"   ", `"abc"YYYY`, "0001-01-01 BC", nil}, {"   ", "/YYYY", "0001-01-01 BC", nil},
			{"   ", `"abc"`, "0001-01-01 BC", nil}, {"   ", "/", "0001-01-01 BC", nil},
			{"2000", "YYYY MM", "2000-01-01", nil}, {"2000", "YYYY TMMon", "2000-01-01", nil},
			{"2000   ", "YYYY MON", "2000-01-01", nil}, {"05   ", "DD MON YYYY", "0001-01-05 BC", nil},
			{"2000   ", "FXYYYY MON", "", name("MON")}, {"2000   ", "YYYY FXMON", "2000-01-01", nil},
			{"  Dec", "MON", "0001-12-01 BC", nil},
		} {
			for _, function := range []string{"to_date", "to_timestamp"} {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%q/%s/prepared=%t", function, c.input, c.mask, prepared), func(t *testing.T) {
						want := c.date
						if function == "to_timestamp" && c.diagnostic == nil {
							if len(want) > 3 && want[len(want)-3:] == " BC" {
								want = want[:len(want)-3] + " 00:00:00+00 BC"
							} else {
								want += " 00:00:00+00"
							}
						}
						query := "SELECT " + function + "(" + datetimeFormattingQuote(c.input) + "," + datetimeFormattingQuote(c.mask) + ")::text AS result"
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, datetimeFormattingReviewQuery(query, prepared, want, c.diagnostic))
					})
				}
			}
		}
	})
}

func TestReferenceDatetimeFormattingReviewCollationDemand(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE dt_review_collation(id integer,input text COLLATE "C",mask text COLLATE "POSIX")`, 0))
		upper := datetimeFormattingDiagnostic("42P22", "could not determine which collation to use for upper() function", "", "Use the COLLATE clause to set the collation explicitly.")
		if b.PG {
			// Explicit release difference: PostgreSQL 18.6 looks up the
			// indeterminate collation before checking it while matching a TM
			// field case-insensitively, and fails internally. 18.0 and native
			// report the intended 42P22. See docs/POSTGRES_REGRESSION.md.
			upper = datetimeFormattingDiagnostic("XX000", "cache lookup failed for collation 0", "", "")
		}
		badDay := datetimeFormattingDiagnostic("22007", `invalid value "ba" for "DD"`, "Value must be an integer.", "")
		mixed := datetimeFormattingDiagnostic("22007", "invalid combination of date conventions", "", "Do not mix Gregorian and ISO week date conventions in a formatting template.")
		cases := []struct {
			input      *string
			mask, date string
			diagnostic *fixture.Diagnostic
		}{
			{input: new("2000"), mask: "YYYY TMMon", date: "2000-01-01"},
			{input: new("05 Dec 2000"), mask: "DD TMMon YYYY", date: "2000-12-05"},
			{input: new("05 dEc 2000"), mask: "DD TMMon YYYY", diagnostic: upper},
			{input: new(""), mask: "TMMonth", date: "0001-01-01 BC"},
			{input: new("bad Dec 2000"), mask: "DD TMMon YYYY", diagnostic: badDay},
			{input: new("05 Bogus 2000"), mask: "DD TMMon YYYY", diagnostic: upper},
			{input: new("05 DEC 2000"), mask: "DD TMMon YYYY", diagnostic: upper},
			{input: new("05 December 2000"), mask: "DD TMMonth YYYY", date: "2000-12-05"},
			{input: new("05 deCember 2000"), mask: "DD TMMonth YYYY", diagnostic: upper},
			{input: nil, mask: "TMMonth"},
			{input: new("2000   "), mask: "YYYY TMMon", date: "2000-01-01"},
			{input: new("2000 05"), mask: "YYYY DD TMMon", date: "2000-01-05"},
			{input: new("2000 2000 Dec"), mask: "YYYY IYYY TMMon", diagnostic: mixed},
			{input: new("2006-10-19 Thursday"), mask: "YYYY-MM-DD TMDay", date: "2006-10-19"},
			{input: new("2006-10-19 tHursday"), mask: "YYYY-MM-DD TMDay", diagnostic: upper},
			{input: new("2006-10-19 Thu"), mask: "YYYY-MM-DD TMDy", date: "2006-10-19"},
			{input: new("2006-10-19 tHu"), mask: "YYYY-MM-DD TMDy", diagnostic: upper},
			{input: new("oops Dec"), mask: "YYYY TMMon", diagnostic: datetimeFormattingDiagnostic("22007", `invalid value "oops" for "YYYY"`, "Value must be an integer.", "")},
		}
		for i, c := range cases {
			var input any
			if c.input != nil {
				input = *c.input
			}
			run(fixture.Exec("INSERT INTO dt_review_collation VALUES($1,$2,$3)", 1, int64(i+1), input, c.mask))
			for _, function := range []string{"to_date", "to_timestamp"} {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/id=%d/prepared=%t", function, i+1, prepared), func(t *testing.T) {
						var want any = c.date
						if c.input == nil {
							want = nil
						} else if function == "to_timestamp" && c.diagnostic == nil {
							date := c.date
							if len(date) > 3 && date[len(date)-3:] == " BC" {
								want = date[:len(date)-3] + " 00:00:00+00 BC"
							} else {
								want = date + " 00:00:00+00"
							}
						}
						query := fmt.Sprintf("SELECT %s(input,mask)::text AS result FROM dt_review_collation WHERE id=%d", function, i+1)
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, datetimeFormattingReviewQuery(query, prepared, want, c.diagnostic))
					})
				}
			}
		}
		for _, function := range []string{"to_date", "to_timestamp"} {
			query := "SELECT " + function + "(input,mask)::text AS result FROM dt_review_collation WHERE id=$1::integer"
			stmt, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
			for _, id := range []int{1, 2, 3, 1} {
				t.Run(fmt.Sprintf("held/%s/id=%d", function, id), func(t *testing.T) {
					c := cases[id-1]
					want := c.date
					if function == "to_timestamp" && c.diagnostic == nil {
						want += " 00:00:00+00"
					}
					s := datetimeFormattingReviewQuery(query, false, want, c.diagnostic)
					s.Args = []any{int64(id)}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				})
			}
		}
		for _, function := range []string{"to_date", "to_timestamp"} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("override/%s/prepared=%t", function, prepared), func(t *testing.T) {
					want := "2000-12-05"
					if function == "to_timestamp" {
						want += " 00:00:00+00"
					}
					query := "SELECT " + function + `(input COLLATE "C",mask)::text AS result FROM dt_review_collation WHERE id=3`
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, datetimeFormattingReviewQuery(query, prepared, want, nil))
				})
			}
		}
		run(fixture.Exec(`CREATE TABLE dt_review_output(id integer,suffix text COLLATE "C",mask text COLLATE "POSIX")`, 0))
		for i, mask := range []string{"TMMonth", "TMMONTH", "TMmonth"} {
			run(fixture.Exec("INSERT INTO dt_review_output VALUES($1,'',$2)", 1, int64(i+1), mask))
			operation := []string{"initcap()", "upper()", "lower()"}[i]
			diagnostic := datetimeFormattingDiagnostic("42P22", "could not determine which collation to use for "+operation+" function", "", "Use the COLLATE clause to set the collation explicitly.")
			for _, source := range []string{"TIMESTAMP 'infinity'", "TIMESTAMP '-infinity'", "NULL::timestamp", "TIMESTAMPTZ 'infinity'", "TIMESTAMPTZ '-infinity'", "NULL::timestamptz", "INTERVAL 'infinity'", "INTERVAL '-infinity'", "NULL::interval", "TIMESTAMP '2006-10-19'", "TIMESTAMPTZ '2006-10-19 00:00+00'", "INTERVAL '1 day'"} {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("output/%s/%s/prepared=%t", mask, source, prepared), func(t *testing.T) {
						var expected *fixture.Diagnostic
						if source == "TIMESTAMP '2006-10-19'" || source == "TIMESTAMPTZ '2006-10-19 00:00+00'" {
							expected = diagnostic
						}
						if source == "INTERVAL '1 day'" {
							expected = datetimeFormattingDiagnostic("22007", "invalid format specification for an interval value", "", "Intervals are not tied to specific calendar dates.")
						}
						query := fmt.Sprintf("SELECT to_char(%s,mask||suffix) AS result FROM dt_review_output WHERE id=%d", source, i+1)
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, datetimeFormattingReviewQuery(query, prepared, nil, expected))
					})
				}
			}
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("output override/%s/prepared=%t", mask, prepared), func(t *testing.T) {
					query := fmt.Sprintf(`SELECT to_char(TIMESTAMP '2006-10-19',(mask||suffix) COLLATE "C") AS result FROM dt_review_output WHERE id=%d`, i+1)
					want := []string{"October", "OCTOBER", "october"}[i]
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, datetimeFormattingReviewQuery(query, prepared, want, nil))
				})
			}
		}
		run(fixture.Exec("INSERT INTO dt_review_output VALUES(4,'','DAY TMMonth')", 1))
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("output earlier interval error/prepared=%t", prepared), func(t *testing.T) {
				query := "SELECT to_char(INTERVAL '1 day',mask||suffix) AS result FROM dt_review_output WHERE id=4"
				d := datetimeFormattingDiagnostic("22007", "invalid format specification for an interval value", "", "Intervals are not tied to specific calendar dates.")
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, datetimeFormattingReviewQuery(query, prepared, nil, d))
			})
		}
	})
}

var datetimeFormattingReviewRestoreDDL = []string{
	"CREATE TABLE dt_restore_stage(id integer,input text,d date DEFAULT to_date('05 Dec 2000','DD Mon YYYY'),ts timestamp,CHECK(to_date(input,'YYYY IW') IS NOT NULL))",
	"CREATE VIEW dt_restore_stage_view AS SELECT id,to_char(d::timestamp,'YYYY-MM-DD') AS d,to_char(ts,'YYYY') AS year FROM dt_restore_stage",
	"INSERT INTO dt_restore_stage(id,input,ts) VALUES(71,'2000','2006-10-19')",
}

const datetimeFormattingReviewRestoreBadInsert = "INSERT INTO dt_restore_stage(id,input,ts) VALUES(72,'2000 42','2007-01-01')"

func TestReferenceDatetimeFormattingReviewRestoreSQL(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
				run := func(s fixture.Step) {
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				}
				t.Cleanup(func() {
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("DROP VIEW IF EXISTS dt_restore_stage_view", 0))
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("DROP TABLE IF EXISTS dt_restore_stage", 0))
				})
				for i, q := range datetimeFormattingReviewRestoreDDL {
					count := int64(0)
					if i == 2 {
						count = 1
					}
					run(fixture.Exec(q, count))
				}
				query := "SELECT id,d,year FROM dt_restore_stage_view ORDER BY id"
				run(fixture.Select(query, []string{"id", "d", "year"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(71), "2000-12-05", "2006"}}))
				d := datetimeFormattingDiagnostic("22007", "invalid combination of date conventions", "", "Do not mix Gregorian and ISO week date conventions in a formatting template.")
				s := fixture.ErrorDiagnostic(datetimeFormattingReviewRestoreBadInsert, *d)
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select(query, []string{"id", "d", "year"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(71), "2000-12-05", "2006"}}))
				run(fixture.Exec("DROP VIEW dt_restore_stage_view", 0))
				run(fixture.Exec("DROP TABLE dt_restore_stage", 0))
			})
		}
	})
}
