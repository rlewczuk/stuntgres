package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceFormatConsumers(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE format_consumer(id int,name text,v text DEFAULT (format('%s:%s',0,'default')),CONSTRAINT format_good CHECK(format('%s',name)<>'bad'))", 0))
		run(referenceExec("INSERT INTO format_consumer(id,name) VALUES(1,'a'),(2,'b')", 2))
		for _, s := range []referenceStep{
			referenceSelect("SELECT format('%s:%s',id,name) AS v FROM format_consumer ORDER BY id", []string{"v"}, []string{"TEXT"}, [][]any{{"1:a"}, {"2:b"}}),
			referenceSelect("SELECT id FROM format_consumer WHERE format('%s',name)='b'", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}),
			referenceSelect("SELECT id FROM format_consumer ORDER BY format('%s',name) DESC", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}}),
			referenceSelect("SELECT v FROM format_consumer ORDER BY id", []string{"v"}, []string{"TEXT"}, [][]any{{"0:default"}, {"0:default"}}),
			referenceSelect("SELECT CAST(format('%s',id) AS integer) AS n,COALESCE(format(NULL),'fallback') AS v FROM format_consumer ORDER BY id", []string{"n", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "fallback"}, {int64(2), "fallback"}}),
		} {
			formatReferenceBoth(t, p, b, c, s)
		}
		run(referenceExec("CREATE TABLE format_copy(id int,v text)", 0))
		run(referencePrepared(referenceExec("INSERT INTO format_copy VALUES(3,format('%s:%s',3,'c'))", 1)))
		run(referenceExec("INSERT INTO format_copy SELECT id,format('%s:%s',id,name) FROM format_consumer", 2))
		run(referenceSelect("UPDATE format_consumer SET v=format('%s:%s',id,upper(name)) WHERE id=1 RETURNING id,format('%s',v) AS v", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "1:A"}}))
		run(referencePrepared(referenceSelect("DELETE FROM format_copy WHERE format('%s',id)='3' RETURNING format('%s',v) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"3:c"}})))
		run(referenceSelect("SELECT id,v FROM format_copy ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "1:a"}, {int64(2), "2:b"}}))
		d := controlsReferenceDiagnostic("23514", `new row for relation "format_consumer" violates check constraint "format_good"`)
		d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (3, bad, 0:default).", "public", "format_consumer", "format_good"
		for _, prepared := range []bool{false, true} {
			s := referenceErrorDiagnostic("INSERT INTO format_consumer(id,name) VALUES(3,'bad')", d)
			s.errorStage = "execute"
			if prepared {
				s = referencePrepared(s)
			}
			run(s)
		}
		run(referenceSelect("SELECT id,name,v FROM format_consumer ORDER BY id", []string{"id", "name", "v"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "a", "1:A"}, {int64(2), "b", "0:default"}}))
		formatReferenceBoth(t, p, b, c, referenceSelect("SELECT format('%s/%s',a.name,b.name) AS v FROM format_consumer a JOIN format_consumer b ON format('%s',a.id)=format('%s',b.id) ORDER BY a.id", []string{"v"}, []string{"TEXT"}, [][]any{{"a/a"}, {"b/b"}}))
	})
}

func TestReferenceFormatGrouping(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE format_groups(k text,v int)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO format_groups VALUES('a',1),('a',2),('b',3)", 3))
		for _, s := range []referenceStep{
			referenceSelect("SELECT format('%s',k) AS k,format('%s',count(v)) AS total FROM format_groups GROUP BY k ORDER BY k", []string{"k", "total"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "2"}, {"b", "1"}}),
			referenceSelect("SELECT min(format('%s',v)) AS v FROM format_groups", []string{"v"}, []string{"TEXT"}, [][]any{{"1"}}),
			referenceSelect("SELECT format('%s',k) AS k,count(*) AS n FROM format_groups GROUP BY format('%s',k) ORDER BY k", []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {"b", int64(1)}}),
		} {
			formatReferenceBoth(t, p, b, c, s)
		}
		q := "SELECT format('%s',v),count(*) FROM format_groups"
		d := controlsReferenceDiagnostic("42803", `column "format_groups.v" must appear in the GROUP BY clause or be used in an aggregate function`)
		d.Position = "20"
		formatReferenceFailure(t, p, b, c, q, d, true)
	})
}

func TestReferenceFormatAtomicity(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE format_source(id int,p text,v text)", 0))
		run(referenceExec("INSERT INTO format_source VALUES(1,'%s','first'),(2,'%I',NULL)", 2))
		run(referenceExec("CREATE TABLE format_target(id int,v text)", 0))
		run(referenceExec("INSERT INTO format_target VALUES(0,'sentinel')", 1))
		d := controlsReferenceDiagnostic("22004", "null values cannot be formatted as an SQL identifier")
		for _, q := range []string{
			"INSERT INTO format_target SELECT id,format(p,v) FROM format_source ORDER BY id",
			"UPDATE format_source SET v=format(p,v)",
			"UPDATE format_source SET v=format(p,v) RETURNING format('%s',v)",
		} {
			t.Run(q, func(t *testing.T) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
				for _, prepared := range []bool{false, true} {
					s := referenceErrorDiagnostic(q, d)
					s.errorStage = "execute"
					if prepared {
						s = referencePrepared(s)
					}
					run(s)
					run(referenceSelect("SELECT id,v FROM format_target ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "sentinel"}}))
					run(referenceSelect("SELECT id,p,v FROM format_source ORDER BY id", []string{"id", "p", "v"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "%s", "first"}, {int64(2), "%I", nil}}))
				}
			})
		}
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "late-query-direct", true: "late-query-held"}[prepared], func(t *testing.T) {
				q := "SELECT format(p,v) AS rendered FROM format_source ORDER BY id"
				var executor referenceExecutor = c
				if prepared {
					stmt, err := c.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal("quiet late-query Prepare", err)
					}
					defer stmt.Close()
					executor = referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
				}
				rows, err := executor.QueryContext(p.ctx, q)
				if rows != nil {
					rows.Close()
					t.Fatal("late format error published cursor")
				}
				referenceAssertDiagnostic(t, err, &d)
			})
		}
		tx, err := c.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		s := referenceErrorDiagnostic("UPDATE format_source SET v=format(p,v)", d)
		s.errorStage = "execute"
		referenceRunStep(t, p.ctx, tx, b.recorder, s)
		s = referenceErrorDiagnostic("SELECT format('after')", controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
		s.selectRows, s.errorStage = true, "execute"
		referenceRunStep(t, p.ctx, tx, b.recorder, s)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(referenceSelect("SELECT id,v FROM format_target", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "sentinel"}}))
	})
}
