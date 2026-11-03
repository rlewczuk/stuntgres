package reference_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func walNamesInvalid(input string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic("22023", `invalid WAL file name "`+input+`"`)
}

func TestReferenceWALNamesDiagnostics(t *testing.T) {
	walNamesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE wal_names_bad(n text)", 0))
		for _, input := range []string{"", strings.Repeat("0", 23), strings.Repeat("0", 25), "G00000000000000000000000", "00000000000000000000000g", " 000000010000000000000001", "000000010000000000000001 ", "00000001000000000000000\n", "00000001000000000000000\t", "+00000010000000000000001", "0x0000010000000000000001", "00000001/000000000000001", "dir/000000010000000000000001", "000000010000000000000001.partial", "00000001.history", "é00000010000000000000001", "bad'\"\\\n"} {
			run(fixture.Exec("DELETE FROM wal_names_bad", 0))
			run(fixture.Exec("INSERT INTO wal_names_bad VALUES($1)", 1, input))
			for _, q := range []string{"SELECT pg_split_walfile_name($1)", "SELECT pg_split_walfile_name(n) FROM wal_names_bad", "SELECT pg_split_walfile_name(" + walNamesQuote(input) + ")"} {
				for _, prepared := range []bool{false, true} {
					for _, query := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, walNamesInvalid(input))
						if strings.Contains(q, "$1") {
							s.Args = []any{input}
						}
						s.Prepared = prepared
						s.SelectRows = query
						s.ErrorStage = "execute"
						run(s)
					}
				}
			}
			run(fixture.Exec("DELETE FROM wal_names_bad", 1))
		}
		for _, name := range []string{"pg_walfile_name", "pg_walfile_name_offset"} {
			for _, input := range []string{"", "bad", "/", "0/", "0//0", "G/0", "0/000000000", " 0/0", "0/0 ", "bad'\"\\\n"} {
				for _, prefix := range []string{"", "/*é*/ "} {
					before := prefix + "SELECT " + name + "("
					q := before + walNamesQuote(input) + ")"
					d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type pg_lsn: "`+input+`"`)
					d.Position = fixture.Position(utf8.RuneCountInString(before) + 1)
					for _, prepared := range []bool{false, true} {
						for _, query := range []bool{false, true} {
							s := fixture.ErrorDiagnostic(q, d)
							s.Prepared = prepared
							s.SelectRows = query
							s.ErrorStage = "execute"
							if prepared {
								s.ErrorStage = "prepare"
							}
							run(s)
						}
					}
				}
				for _, arg := range []any{input, []byte(input)} {
					for _, prepared := range []bool{false, true} {
						for _, query := range []bool{false, true} {
							d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type pg_lsn: "`+input+`"`)
							d.Context = "unnamed portal parameter $1 = '...'"
							if input == "" {
								d.Context = "unnamed portal parameter $1 = ''"
							}
							s := fixture.ErrorDiagnostic("SELECT "+name+"($1)", d, arg)
							s.Prepared = prepared
							s.SelectRows = query
							s.ErrorStage = "execute"
							run(s)
						}
					}
				}
			}
		}
		for _, tc := range []struct{ q, message string }{
			{"SELECT pg_walfile_name(1)", "function pg_walfile_name(integer) does not exist"},
			{"SELECT pg_walfile_name()", "function pg_walfile_name() does not exist"},
			{"SELECT pg_walfile_name('0/0', '0/0')", "function pg_walfile_name(unknown, unknown) does not exist"},
			{"SELECT pg_walfile_name_offset(1)", "function pg_walfile_name_offset(integer) does not exist"},
			{"SELECT pg_split_walfile_name(1)", "function pg_split_walfile_name(integer) does not exist"},
			{"SELECT pg_walfile_name(lsn => '0/0')", "function pg_walfile_name(lsn => unknown) does not exist"},
			{"SELECT pg_walfile_name_offset(file_name => '0/0')", "function pg_walfile_name_offset(file_name => unknown) does not exist"},
			{"SELECT pg_split_walfile_name(lsn => 'bad')", "function pg_split_walfile_name(lsn => unknown) does not exist"},
			{"SELECT pg_walfile_name('0/0'::text)", "function pg_walfile_name(text) does not exist"},
			{"SELECT pg_walfile_name(VARIADIC ARRAY[1])", "function pg_walfile_name(integer[]) does not exist"},
		} {
			d := fixture.ControlsReferenceDiagnostic("42883", tc.message)
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			d.Position = fixture.Position(8)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.q, d)
				s.Prepared = prepared
				s.SelectRows = true
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		}
		for _, tc := range []struct{ q, message string }{
			{"SELECT pg_walfile_name(DISTINCT '0/0')", "DISTINCT specified, but pg_walfile_name is not an aggregate function"},
			{"SELECT pg_walfile_name('0/0') FILTER (WHERE TRUE)", "FILTER specified, but pg_walfile_name is not an aggregate function"},
			{"SELECT pg_walfile_name('0/0') OVER ()", "OVER specified, but pg_walfile_name is not a window function nor an aggregate function"},
		} {
			d := fixture.ControlsReferenceDiagnostic("42809", tc.message)
			d.Position = fixture.Position(8)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.q, d)
				s.Prepared = prepared
				s.SelectRows = true
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		}
	})
}

func TestReferenceWALNamesAtomicity(t *testing.T) {
	walNamesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE wal_names_atomic(id integer,n numeric,t bigint)", 0))
		run(fixture.Exec("INSERT INTO wal_names_atomic VALUES(1,256,1)", 1))
		run(fixture.Exec("CREATE TABLE wal_names_atomic_inputs(id integer,n text)", 0))
		run(fixture.Exec("INSERT INTO wal_names_atomic_inputs VALUES(2,'000000010000000000000001'),(3,'bad')", 2))
		unchanged := fixture.Select("SELECT id,n,t FROM wal_names_atomic ORDER BY id", []string{"id", "n", "t"}, []string{"INT4", "NUMERIC", "INT8"}, [][]any{{int64(1), "256", int64(1)}})
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{
				"INSERT INTO wal_names_atomic VALUES(2,(pg_split_walfile_name($1)).segment_number,1),(3,(pg_split_walfile_name($2)).segment_number,1)",
				"INSERT INTO wal_names_atomic SELECT id,(pg_split_walfile_name(n)).segment_number,(pg_split_walfile_name(n)).timeline_id FROM wal_names_atomic_inputs ORDER BY id",
			} {
				s := fixture.ErrorDiagnostic(q, walNamesInvalid("bad"))
				if strings.Contains(q, "$1") {
					s.Args = []any{"000000010000000000000001", "bad"}
				}
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				run(unchanged)
			}
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, q := range []string{"SELECT pg_walfile_name('0/0')", "SELECT * FROM pg_walfile_name_offset('0/0')", "SELECT * FROM pg_split_walfile_name('000000010000000000000001')"} {
			_, err := b.DB.QueryContext(ctx, q)
			if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary canceled query", err)
			}
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			_, err = stmt.QueryContext(ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary canceled execute", err)
			}
			stmt.Close()
		}
		run(unchanged)
	})
}
