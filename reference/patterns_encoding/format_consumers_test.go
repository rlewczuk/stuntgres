package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFormatConsumers(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE format_consumer(id int,name text,v text DEFAULT (format('%s:%s',0,'default')),CONSTRAINT format_good CHECK(format('%s',name)<>'bad'))", 0))
		run(fixture.Exec("INSERT INTO format_consumer(id,name) VALUES(1,'a'),(2,'b')", 2))
		for _, s := range []fixture.Step{
			fixture.Select("SELECT format('%s:%s',id,name) AS v FROM format_consumer ORDER BY id", []string{"v"}, []string{"TEXT"}, [][]any{{"1:a"}, {"2:b"}}),
			fixture.Select("SELECT id FROM format_consumer WHERE format('%s',name)='b'", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}),
			fixture.Select("SELECT id FROM format_consumer ORDER BY format('%s',name) DESC", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}}),
			fixture.Select("SELECT v FROM format_consumer ORDER BY id", []string{"v"}, []string{"TEXT"}, [][]any{{"0:default"}, {"0:default"}}),
			fixture.Select("SELECT CAST(format('%s',id) AS integer) AS n,COALESCE(format(NULL),'fallback') AS v FROM format_consumer ORDER BY id", []string{"n", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "fallback"}, {int64(2), "fallback"}}),
		} {
			formatReferenceBoth(t, p, b, c, s)
		}
		run(fixture.Exec("CREATE TABLE format_copy(id int,v text)", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO format_copy VALUES(3,format('%s:%s',3,'c'))", 1)))
		run(fixture.Exec("INSERT INTO format_copy SELECT id,format('%s:%s',id,name) FROM format_consumer", 2))
		run(fixture.Select("UPDATE format_consumer SET v=format('%s:%s',id,upper(name)) WHERE id=1 RETURNING id,format('%s',v) AS v", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "1:A"}}))
		run(fixture.Prepared(fixture.Select("DELETE FROM format_copy WHERE format('%s',id)='3' RETURNING format('%s',v) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"3:c"}})))
		run(fixture.Select("SELECT id,v FROM format_copy ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "1:a"}, {int64(2), "2:b"}}))
		d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "format_consumer" violates check constraint "format_good"`)
		d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (3, bad, 0:default).", "public", "format_consumer", "format_good"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO format_consumer(id,name) VALUES(3,'bad')", d)
			s.ErrorStage = "execute"
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
		}
		run(fixture.Select("SELECT id,name,v FROM format_consumer ORDER BY id", []string{"id", "name", "v"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "a", "1:A"}, {int64(2), "b", "0:default"}}))
		formatReferenceBoth(t, p, b, c, fixture.Select("SELECT format('%s/%s',a.name,b.name) AS v FROM format_consumer a JOIN format_consumer b ON format('%s',a.id)=format('%s',b.id) ORDER BY a.id", []string{"v"}, []string{"TEXT"}, [][]any{{"a/a"}, {"b/b"}}))
	})
}

func TestReferenceFormatGrouping(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE format_groups(k text,v int)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO format_groups VALUES('a',1),('a',2),('b',3)", 3))
		for _, s := range []fixture.Step{
			fixture.Select("SELECT format('%s',k) AS k,format('%s',count(v)) AS total FROM format_groups GROUP BY k ORDER BY k", []string{"k", "total"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "2"}, {"b", "1"}}),
			fixture.Select("SELECT min(format('%s',v)) AS v FROM format_groups", []string{"v"}, []string{"TEXT"}, [][]any{{"1"}}),
			fixture.Select("SELECT format('%s',k) AS k,count(*) AS n FROM format_groups GROUP BY format('%s',k) ORDER BY k", []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {"b", int64(1)}}),
		} {
			formatReferenceBoth(t, p, b, c, s)
		}
		q := "SELECT format('%s',v),count(*) FROM format_groups"
		d := fixture.ControlsReferenceDiagnostic("42803", `column "format_groups.v" must appear in the GROUP BY clause or be used in an aggregate function`)
		d.Position = "20"
		formatReferenceFailure(t, p, b, c, q, d, true)
	})
}

func TestReferenceFormatAtomicity(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE format_source(id int,p text,v text)", 0))
		run(fixture.Exec("INSERT INTO format_source VALUES(1,'%s','first'),(2,'%I',NULL)", 2))
		run(fixture.Exec("CREATE TABLE format_target(id int,v text)", 0))
		run(fixture.Exec("INSERT INTO format_target VALUES(0,'sentinel')", 1))
		d := fixture.ControlsReferenceDiagnostic("22004", "null values cannot be formatted as an SQL identifier")
		for _, q := range []string{
			"INSERT INTO format_target SELECT id,format(p,v) FROM format_source ORDER BY id",
			"UPDATE format_source SET v=format(p,v)",
			"UPDATE format_source SET v=format(p,v) RETURNING format('%s',v)",
		} {
			t.Run(q, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, d)
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					run(fixture.Select("SELECT id,v FROM format_target ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "sentinel"}}))
					run(fixture.Select("SELECT id,p,v FROM format_source ORDER BY id", []string{"id", "p", "v"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "%s", "first"}, {int64(2), "%I", nil}}))
				}
			})
		}
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "late-query-direct", true: "late-query-held"}[prepared], func(t *testing.T) {
				q := "SELECT format(p,v) AS rendered FROM format_source ORDER BY id"
				var executor fixture.Executor = c
				if prepared {
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal("quiet late-query Prepare", err)
					}
					defer stmt.Close()
					executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				}
				rows, err := executor.QueryContext(p.Ctx, q)
				if rows != nil {
					rows.Close()
					t.Fatal("late format error published cursor")
				}
				fixture.AssertDiagnostic(t, err, &d)
			})
		}
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		s := fixture.ErrorDiagnostic("UPDATE format_source SET v=format(p,v)", d)
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
		s = fixture.ErrorDiagnostic("SELECT format('after')", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT id,v FROM format_target", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "sentinel"}}))
	})
}
