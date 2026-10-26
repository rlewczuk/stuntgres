package reference_test

import (
	"context"
	"errors"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func pgLSNDiagnostic(input string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type pg_lsn: "`+input+`"`)
}

func TestReferencePGLSNDiagnostics(t *testing.T) {
	pgLSNScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE pglsn_empty(v pg_lsn)", 0))
		for _, input := range []string{"", "bad", "/", "0/", "/0", "0//0", "0/0/0", "+0/0", "-1/0", "0x1/0", "G/0", "0/G", " 0/0", "0/0 ", "0 /0", "0/ 0", "0/\t0", "0/0\n", "000000000/0", "0/000000000", "100000000/0", "0/100000000", "0/0junk", "bad'\"\\\n"} {
			for _, prefix := range []string{"", "/*é*/ "} {
				q := prefix + "SELECT " + tidQuote(input) + "::pg_lsn"
				d := pgLSNDiagnostic(input)
				d.Position = fixture.Position(8 + utf8.RuneCountInString(prefix))
				for _, query := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, d)
						s.SelectRows = query
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						run(s)
					}
				}
			}
			for _, arg := range []any{input, []byte(input)} {
				d := pgLSNDiagnostic(input)
				d.Context = "unnamed portal parameter $1 = '...'"
				if input == "" {
					d.Context = "unnamed portal parameter $1 = ''"
				}
				for _, prepared := range []bool{false, true} {
					for _, query := range []bool{false, true} {
						s := fixture.ErrorDiagnostic("SELECT $1::pg_lsn", d, arg)
						s.SelectRows = query
						s.Prepared = prepared
						s.ErrorStage = "execute"
						run(s)
					}
				}
			}
		}
		for _, tc := range []struct {
			q   string
			pos int
		}{
			{"SELECT pg_lsn 'bad'", 15}, {"/*é*/ SELECT pg_lsn 'bad'", 21},
			{"SELECT 'bad'::pg_lsn FROM pglsn_empty", 8}, {"SELECT 'bad'::pg_lsn FROM pglsn_empty WHERE FALSE", 8},
		} {
			d := pgLSNDiagnostic("bad")
			d.Position = fixture.Position(tc.pos)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.q, d)
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT 'bad'::text::pg_lsn", pgLSNDiagnostic("bad"))
			s.Prepared = prepared
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
		}
		d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: numeric - pg_lsn")
		d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		d.Position = fixture.Position(19)
		for _, query := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT 1::numeric - '0/0'::pg_lsn", d)
				s.SelectRows = query
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT 'FFFFFFFF/FFFFFFFF'::pg_lsn + $1::numeric", fixture.ControlsReferenceDiagnostic("22023", "pg_lsn out of range"), "1")
			s.Prepared = prepared
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			reverse := fixture.ControlsReferenceDiagnostic("22023", "pg_lsn out of range")
			reverse.Context = "SQL function \"numeric_pl_pg_lsn\" statement 1"
			s = fixture.ErrorDiagnostic("SELECT $1::numeric + 'FFFFFFFF/FFFFFFFF'::pg_lsn", reverse, "1")
			s.Prepared = prepared
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
		}
		for _, expr := range []string{"'0/0'::pg_lsn - 0.5::numeric", "'FFFFFFFF/FFFFFFFF'::pg_lsn + 0.5::numeric", "'FFFFFFFF/FFFFFFFF'::pg_lsn + 1", "'0/0'::pg_lsn - 1", "'0/0'::pg_lsn + (-1)::numeric", "'FFFFFFFF/FFFFFFFF'::pg_lsn - (-1)::numeric"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT "+expr, fixture.ControlsReferenceDiagnostic("22023", "pg_lsn out of range"))
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
			}
		}
		for _, offset := range []string{"NaN", "Infinity", "-Infinity"} {
			for _, tc := range []struct{ q, message string }{
				{"SELECT '0/0'::pg_lsn + $1::numeric", "cannot add NaN to pg_lsn"},
				{"SELECT $1::numeric + '0/0'::pg_lsn", "cannot add NaN to pg_lsn"},
				{"SELECT '0/0'::pg_lsn - $1::numeric", "cannot subtract NaN from pg_lsn"},
			} {
				message := tc.message
				if offset != "NaN" {
					message = "cannot convert infinity to pg_lsn"
				}
				for _, prepared := range []bool{false, true} {
					d := fixture.ControlsReferenceDiagnostic("0A000", message)
					if tc.q == "SELECT $1::numeric + '0/0'::pg_lsn" {
						d.Context = "SQL function \"numeric_pl_pg_lsn\" statement 1"
					}
					s := fixture.ErrorDiagnostic(tc.q, d, offset)
					s.Prepared = prepared
					s.SelectRows = true
					s.ErrorStage = "execute"
					run(s)
				}
			}
		}
	})
}

func TestReferencePGLSNAtomicity(t *testing.T) {
	pgLSNScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE pglsn_atomic(id integer,v pg_lsn,source text)", 0))
		run(fixture.Exec("INSERT INTO pglsn_atomic VALUES(1,'0/0','0/1'),(2,'FFFFFFFF/FFFFFFFF','bad')", 2))
		unchanged := fixture.Select("SELECT id,v FROM pglsn_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "PG_LSN"}, [][]any{{int64(1), "0/0"}, {int64(2), "FFFFFFFF/FFFFFFFF"}})
		for _, prepared := range []bool{false, true} {
			d := pgLSNDiagnostic("bad")
			d.Context = "unnamed portal parameter $2 = '...'"
			s := fixture.ErrorDiagnostic("INSERT INTO pglsn_atomic(id,v) VALUES(3,$1),(4,$2)", d, "0/1", "bad")
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			run(unchanged)
			s = fixture.ErrorDiagnostic("UPDATE pglsn_atomic SET v=v+1", fixture.ControlsReferenceDiagnostic("22023", "pg_lsn out of range"))
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			run(unchanged)
			s = fixture.ErrorDiagnostic("UPDATE pglsn_atomic SET v=source::pg_lsn", pgLSNDiagnostic("bad"))
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			run(unchanged)
		}
		tx, err := b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		txrun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
		txrun(fixture.Exec("SAVEPOINT pglsn_recover", 0))
		recoverError := fixture.ErrorDiagnostic("UPDATE pglsn_atomic SET v=v+1", fixture.ControlsReferenceDiagnostic("22023", "pg_lsn out of range"))
		recoverError.ErrorStage = "execute"
		txrun(recoverError)
		txrun(fixture.Exec("ROLLBACK TO SAVEPOINT pglsn_recover", 0))
		txrun(unchanged)
		txrun(fixture.Exec("UPDATE pglsn_atomic SET v=v+1 WHERE id=1", 1))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		committed := fixture.Select("SELECT id,v FROM pglsn_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "PG_LSN"}, [][]any{{int64(1), "0/1"}, {int64(2), "FFFFFFFF/FFFFFFFF"}})
		run(committed)
		if !b.PG {
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err := b.DB.ExecContext(canceled, "INSERT INTO pglsn_atomic(id,v) VALUES(3,$1)", "0/1")
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			run(committed)
		}
	})
}
