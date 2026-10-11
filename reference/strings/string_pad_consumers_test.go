package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringPadConsumers(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE sp_source(id int,s text)", 0), fixture.Exec("INSERT INTO sp_source VALUES(1,'ab'),(2,'ac'),(3,NULL)", 3),
			fixture.Select("SELECT id,lpad(s,3,'x') AS l,rpad(s,3,'x') AS r,repeat(s,2) AS t,reverse(s) AS v FROM sp_source ORDER BY id", []string{"id", "l", "r", "t", "v"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "xab", "abx", "abab", "ba"}, {int64(2), "xac", "acx", "acac", "ca"}, {int64(3), nil, nil, nil, nil}}),
			fixture.Select("SELECT id FROM sp_source WHERE reverse(s)='ba' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
			fixture.Select("SELECT lpad(s,1,'x') AS v,count(*) AS n FROM sp_source GROUP BY lpad(s,1,'x') ORDER BY v NULLS LAST", []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}),
			fixture.Select("SELECT rpad(s,1,'x') AS v,count(*) AS n FROM sp_source GROUP BY rpad(s,1,'x') HAVING repeat(rpad(s,1,'x'),2)='aa' ORDER BY v", []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}}),
			fixture.Select("SELECT a.id,b.id AS other FROM sp_source a JOIN sp_source b ON reverse(a.s)=reverse(b.s) ORDER BY a.id,b.id", []string{"id", "other"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}),
			fixture.Select("SELECT id FROM sp_source ORDER BY reverse(s) DESC NULLS LAST", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}, {int64(3)}}),
			fixture.Select("VALUES(lpad('a',2,'x'),rpad('a',2,'x'),repeat('a',2),reverse('ab'))", []string{"column1", "column2", "column3", "column4"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"xa", "ax", "aa", "ba"}}),
		} {
			if s.SelectRows {
				stringPadBoth(t, p, b, c, s)
			} else {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE sp_dest(id int,s text)", 0), fixture.Exec("INSERT INTO sp_dest SELECT id,repeat(s,2) FROM sp_source ORDER BY id", 3), fixture.Exec("INSERT INTO sp_dest VALUES(4,lpad(reverse('ab'),3,'x'))", 1), fixture.Select("UPDATE sp_dest SET s=repeat(s,2) WHERE id=1 RETURNING reverse(s) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"babababa"}}), fixture.Select("DELETE FROM sp_dest WHERE id=2 RETURNING reverse(s) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"caca"}}), fixture.Select("SELECT id,s FROM sp_dest ORDER BY id", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "abababab"}, {int64(3), nil}, {int64(4), "xba"}}), fixture.Exec("DROP TABLE sp_dest", 0)} {
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
	})
}

func TestReferenceStringPadAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sp_reader"})
	stringPadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE sp_acl(s text)", 0), fixture.Exec("INSERT INTO sp_acl VALUES('ab')", 1), fixture.Exec("CREATE ROLE sp_reader", 0)} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		if b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT USAGE ON SCHEMA public TO sp_reader", 0))
		}
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		for _, empty := range []bool{false, true} {
			if empty {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM sp_acl", 1))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE sp_reader", 0))
			stringPadFailure(t, p, b, c, "SELECT lpad(s,3),rpad(NULL,3),repeat(NULL,2),reverse(s) FROM sp_acl", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table sp_acl"), false)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
		}
	})
}

func TestReferenceStringPadAtomicity(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE sp_atomic(id int,s text,n int)", 0), fixture.Exec("INSERT INTO sp_atomic VALUES(1,'x',2),(2,'x',1073741820)", 2), fixture.Exec("CREATE TABLE sp_sink(id int,s text)", 0), fixture.Exec("INSERT INTO sp_sink VALUES(0,'sentinel')", 1)} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, tc := range []struct {
			q    string
			rows bool
		}{
			{"INSERT INTO sp_sink SELECT id,repeat(s,n) FROM sp_atomic ORDER BY id", false},
			{"UPDATE sp_atomic SET s=repeat(s,n)", false},
			{"UPDATE sp_atomic SET s='z' RETURNING repeat(s,n) AS v", true},
			{"INSERT INTO sp_sink SELECT id,lpad(s,n,'x') FROM sp_atomic ORDER BY id", false},
			{"UPDATE sp_atomic SET s=rpad(s,n,'x')", false},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.q+fixture.Position(map[bool]int{false: 1, true: 2}[prepared]), func(t *testing.T) {
					var old *sql.Rows
					if !b.PG {
						var err error
						old, err = c.QueryContext(p.Ctx, "SELECT id,s FROM sp_atomic ORDER BY id")
						if err != nil {
							t.Fatal(err)
						}
						defer old.Close()
					}
					s := fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic("54000", "requested length too large"))
					s.SelectRows = tc.rows
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					if old != nil {
						fixture.StringCoreRetainedRows(t, old, nil, []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), "x"}}, false)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,s,n FROM sp_atomic ORDER BY id", []string{"id", "s", "n"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "x", int64(2)}, {int64(2), "x", int64(1073741820)}}))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,s FROM sp_sink", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "sentinel"}}))
				})
			}
		}
	})
}

func TestReferenceStringPadCancellation(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE sp_cancel(s text)", 0))
		writeSQL := "INSERT INTO sp_cancel VALUES(lpad(reverse(repeat('ab',2)),5,'x'))"
		readSQL := "SELECT lpad('ab',3,'x'),rpad('ab',3,'x'),repeat('ab',2),reverse('ab')"
		write, err := b.DB.PrepareContext(p.Ctx, writeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer write.Close()
		read, err := b.DB.PrepareContext(p.Ctx, readSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, exec := range []func() (sql.Result, error){func() (sql.Result, error) { return b.DB.ExecContext(ctx, writeSQL) }, func() (sql.Result, error) { return write.ExecContext(ctx) }} {
			r, err := exec()
			if r != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(r, err)
			}
		}
		for _, query := range []func() (*sql.Rows, error){func() (*sql.Rows, error) { return b.DB.QueryContext(ctx, readSQL) }, func() (*sql.Rows, error) { return read.QueryContext(ctx) }} {
			r, err := query()
			if r != nil {
				r.Close()
				t.Fatal("canceled query published rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT s FROM sp_cancel", []string{"s"}, []string{"TEXT"}, nil))
		stringPadBoth(t, p, b, c, fixture.Select(readSQL, []string{"lpad", "rpad", "repeat", "reverse"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"xab", "abx", "abab", "ba"}}))
	})
}
