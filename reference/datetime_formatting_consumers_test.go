package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDatetimeFormattingConsumers(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, conn *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
		run(referenceExec("CREATE TABLE dt_consumer(id integer, ts timestamp, input text, template text)", 0))
		run(referenceExec("INSERT INTO dt_consumer VALUES (1,'2006-10-19','2000-12-05','YYYY-MM-DD'),(2,'2006-10-20','2000-12-06','YYYY-MM-DD'),(3,'2007-01-01',NULL,'YYYY-MM-DD')", 3))
		for _, prepared := range []bool{false, true} {
			for _, s := range []referenceStep{
				referenceSelect("SELECT id, lower(to_char(ts,'MONTH')) AS month, CASE WHEN input IS NULL THEN 'missing' ELSE to_date(input,template)::text END AS d FROM dt_consumer ORDER BY id", []string{"id", "month", "d"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "october  ", "2000-12-05"}, {int64(2), "october  ", "2000-12-06"}, {int64(3), "january  ", "missing"}}),
				referenceSelect("SELECT id FROM dt_consumer WHERE to_char(ts,$1::text)=$2::text ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, "YYYY", "2006"),
				referenceSelect("SELECT a.id,b.id AS peer FROM dt_consumer a JOIN dt_consumer b ON to_char(a.ts,'YYYY')=to_char(b.ts,'YYYY') WHERE a.id=1 ORDER BY b.id", []string{"id", "peer"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(2)}}),
				referenceSelect("SELECT to_char(ts,'YYYY') AS year, count(*) AS n FROM dt_consumer GROUP BY to_char(ts,'YYYY') ORDER BY to_char(ts,'YYYY')", []string{"year", "n"}, []string{"TEXT", "INT8"}, [][]any{{"2006", int64(2)}, {"2007", int64(1)}}),
				referenceSelect("SELECT min(to_char(ts,'YYYY')) AS first, max(to_char(ts,'MM')) AS last FROM dt_consumer", []string{"first", "last"}, []string{"TEXT", "TEXT"}, [][]any{{"2006", "10"}}),
				referenceSelect("SELECT to_char(ts,'YYYY') AS year,to_char(ts,'MM') AS month,count(*) AS n FROM dt_consumer GROUP BY to_char(ts,'YYYY'),to_char(ts,'MM') ORDER BY year,month", []string{"year", "month", "n"}, []string{"TEXT", "TEXT", "INT8"}, [][]any{{"2006", "10", int64(2)}, {"2007", "01", int64(1)}}),
				referenceSelect("SELECT id FROM dt_consumer ORDER BY to_date(input,template) DESC NULLS LAST", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}, {int64(3)}}),
				referenceSelect("VALUES (to_char(TIMESTAMP '2006-10-19','YYYY')), (to_date('2000','YYYY')::text)", []string{"column1"}, []string{"TEXT"}, [][]any{{"2006"}, {"2000-01-01"}}),
				referenceSelect("SELECT id FROM dt_consumer WHERE to_date(input,template)=DATE '2000-12-05' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
			} {
				if prepared {
					s = referencePrepared(s)
				}
				run(s)
			}
			// Keep the original unsupported INSERT RETURNING route as a
			// capability witness, then exercise selected INSERT expressions
			// through the established INSERT + SELECT consumer.
			q := "INSERT INTO dt_consumer VALUES (4,to_date('2008-02-29','YYYY-MM-DD')::timestamp,'2008-02-29','YYYY-MM-DD') RETURNING id,to_char(ts,'YYYY-MM-DD') AS d"
			s := referenceSelect(q, []string{"id", "d"}, []string{"INT4", "TEXT"}, [][]any{{int64(4), "2008-02-29"}})
			if !b.pg {
				d := datetimeFormattingDiagnostic("0A000", "INSERT suffix is unsupported", "", "")
				d.Position = "108"
				s = referenceErrorDiagnostic(q, *d)
				s.selectRows = true
				s.errorStage = "execute"
				if prepared {
					s.errorStage = "prepare"
				}
			}
			if prepared {
				s = referencePrepared(s)
			}
			run(s)
			var originalRows [][]any
			affected := int64(0)
			if b.pg {
				originalRows = [][]any{{int64(4), "2008-02-29"}}
				affected = 1
			}
			run(referenceSelect("SELECT id,to_char(ts,'YYYY-MM-DD') AS d FROM dt_consumer WHERE id=4", []string{"id", "d"}, []string{"INT4", "TEXT"}, originalRows))
			run(referenceExec("DELETE FROM dt_consumer WHERE id=4", affected))
			s = referenceExec("INSERT INTO dt_consumer VALUES (4,to_date('2008-02-29','YYYY-MM-DD')::timestamp,'2008-02-29','YYYY-MM-DD')", 1)
			if prepared {
				s = referencePrepared(s)
			}
			run(s)
			for _, s := range []referenceStep{
				referenceSelect("SELECT id,to_char(ts,'YYYY-MM-DD') AS d FROM dt_consumer WHERE id=4", []string{"id", "d"}, []string{"INT4", "TEXT"}, [][]any{{int64(4), "2008-02-29"}}),
				referenceSelect("UPDATE dt_consumer SET ts=to_timestamp('2009-01-02 +00','YYYY-MM-DD TZH')::timestamp WHERE id=4 RETURNING id,to_char(ts,'YYYY-MM-DD') AS d", []string{"id", "d"}, []string{"INT4", "TEXT"}, [][]any{{int64(4), "2009-01-02"}}),
				referenceSelect("DELETE FROM dt_consumer WHERE to_char(ts,'YYYY')='2009' RETURNING id,to_char(ts,'YYYY') AS year", []string{"id", "year"}, []string{"INT4", "TEXT"}, [][]any{{int64(4), "2009"}}),
			} {
				if prepared {
					s = referencePrepared(s)
				}
				run(s)
			}
		}
	})
}

func TestReferenceDatetimeFormattingStoredConsumers(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, conn *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
		run(referenceExec("CREATE TABLE dt_stored(unused integer,id integer,d date DEFAULT to_date('05 Dec 2000','DD Mon YYYY'),label text DEFAULT to_char(TIMESTAMP '2006-10-19','YYYY'),CHECK (to_char(d::timestamp,'YYYY')='2000'))", 0))
		run(referenceExec("CREATE VIEW dt_stored_view AS SELECT id,to_char(d::timestamp,'YYYY-MM-DD') AS d,label FROM dt_stored", 0))
		for _, prepared := range []bool{false, true} {
			s := referenceExec("INSERT INTO dt_stored(id) VALUES($1::integer)", 1, int64(1))
			if prepared {
				s = referencePrepared(s)
			}
			run(s)
			s = referenceSelect("SELECT id,d,label FROM dt_stored_view ORDER BY id", []string{"id", "d", "label"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "2000-12-05", "2006"}})
			if prepared {
				s = referencePrepared(s)
			}
			run(s)
			run(referenceExec("DELETE FROM dt_stored", 1))
		}
		run(referenceExec("ALTER TABLE dt_stored DROP COLUMN unused", 0))
		run(referenceExec("ALTER TABLE dt_stored RENAME COLUMN d TO parsed", 0))
		run(referenceExec("INSERT INTO dt_stored(id) VALUES(2)", 1))
		run(referenceSelect("SELECT id,d,label FROM dt_stored_view", []string{"id", "d", "label"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(2), "2000-12-05", "2006"}}))
		for _, q := range []string{
			"INSERT INTO dt_stored(id,parsed) VALUES(3,'2001-01-01')",
			"CREATE TABLE dt_generated(ts timestamp,label text GENERATED ALWAYS AS (to_char(ts,'YYYY')) STORED)",
			"CREATE TABLE dt_generated_date(input text,d date GENERATED ALWAYS AS (to_date(input,'YYYY')) STORED)",
			"CREATE INDEX dt_format_index ON dt_stored ((to_char(parsed::timestamp,'YYYY')))",
			"SELECT pg_get_viewdef('dt_stored_view'::regclass,true)",
		} {
			for _, prepared := range []bool{false, true} {
				datetimeFormattingAssertContract(t, p, b, conn, q, prepared)
			}
		}
	})
}

func TestReferenceDatetimeFormattingStableLegality(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, conn *sql.Conn) {
		for _, q := range []string{
			"CREATE TABLE dt_stable_z(z timestamptz,label text GENERATED ALWAYS AS (to_char(z,'YYYY')) STORED)",
			"CREATE TABLE dt_stable_i(iv interval,label text GENERATED ALWAYS AS (to_char(iv,'HH24')) STORED)",
			"CREATE TABLE dt_stable_parse(input text,z timestamptz GENERATED ALWAYS AS (to_timestamp(input,'YYYY')) STORED)",
		} {
			for _, prepared := range []bool{false, true} {
				datetimeFormattingAssertContract(t, p, b, conn, q, prepared)
			}
		}
	})
}
