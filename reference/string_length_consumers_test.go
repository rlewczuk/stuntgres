package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestReferenceStringLengthConsumersReplay(t *testing.T) {
	stringLengthScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE sl_use(id int,v text,c char(4),n int DEFAULT (char_length('é🙂')),CONSTRAINT sl_valid CHECK(char_length(v)<=2 AND octet_length(c)=8))", 0))
		stringLengthBoth(t, p, b, c, referenceExec("INSERT INTO sl_use(id,v,c) VALUES(1,'é🙂','é🙂')", 1))
		// INSERT VALUES has no RETURNING admission; assert its count and default,
		// then project the same measure results. UPDATE below owns RETURNING.
		stringLengthBoth(t, p, b, c, referenceSelect("SELECT id,n,char_length(v) AS chars,octet_length(c) AS physical FROM sl_use", []string{"id", "n", "chars", "physical"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(2), int64(8)}, {int64(1), int64(2), int64(2), int64(8)}}))
		run(referenceSelect("SELECT char_length(v) AS chars,count(*) AS total FROM sl_use WHERE strpos(v,'🙂')=2 GROUP BY char_length(v) ORDER BY char_length(v)", []string{"chars", "total"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(2)}}))
		stringLengthBoth(t, p, b, c, referenceSelect("UPDATE sl_use SET v='🙂é' WHERE id=1 RETURNING strpos(v,'é') AS found,n", []string{"found", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(2)}, {int64(2), int64(2)}}))
		before := referenceSelect("SELECT id,v,c,n FROM sl_use ORDER BY id", []string{"id", "v", "c", "n"}, []string{"INT4", "TEXT", "BPCHAR", "INT4"}, [][]any{{int64(1), "🙂é", "é🙂  ", int64(2)}, {int64(1), "🙂é", "é🙂  ", int64(2)}})
		for _, prepared := range []bool{false, true} {
			d := controlsReferenceDiagnostic("23514", `new row for relation "sl_use" violates check constraint "sl_valid"`)
			d.Detail = "Failing row contains (3, abc, é🙂  , 2)."
			d.Schema, d.Table, d.Constraint = "public", "sl_use", "sl_valid"
			s := referenceErrorDiagnostic("INSERT INTO sl_use(id,v,c) VALUES(2,'é🙂','é🙂'),(3,'abc','é🙂')", d)
			s.selectRows, s.errorStage = true, "execute"
			if prepared {
				s = referencePrepared(s)
			}
			run(s)
			run(before)
		}
		run(referenceExec(`CREATE VIEW sl_view AS SELECT octet_length(c) AS physical,octet_length(c::text) AS trimmed,position('🙂' IN v) AS special,"position"(v,'é') AS ordinary FROM sl_use`, 0))
		view := referenceSelect("SELECT * FROM sl_view", []string{"physical", "trimmed", "special", "ordinary"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(8), int64(6), int64(1), int64(2)}, {int64(8), int64(6), int64(1), int64(2)}})
		stringLengthBoth(t, p, b, c, view)
		// SQL presentation is independently asserted; structural native nodes are
		// intentionally not compared with PostgreSQL's physical node encoding.
		t.Run("check-deparse", func(t *testing.T) {
			stringLengthBoth(t, p, b, c, referenceSelect("SELECT pg_get_expr(conbin,conrelid),pg_get_expr(conbin,conrelid,true) FROM pg_constraint WHERE conname='sl_valid'", []string{"pg_get_expr", "pg_get_expr"}, []string{"TEXT", "TEXT"}, [][]any{{"((char_length(v) <= 2) AND (octet_length(c) = 8))", "char_length(v) <= 2 AND octet_length(c) = 8"}}))
		})
		if !b.pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(before)
			run(view)
			// Replay the actual returned SQL, not a hand-written approximation.
			if p.postgres != nil {
				if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
					t.Fatal(err)
				}
				if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, before)
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, view)
				if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
					t.Fatal(err)
				}
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewBufferString("CREATE TABLE sl_restore(n int); INSERT INTO sl_restore VALUES(char_length('é🙂')), (strpos('é🙂','🙂'));")); err != nil {
				t.Fatal(err)
			}
			run(referenceSelect("SELECT n FROM sl_restore ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}}))
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewBufferString("CREATE TABLE sl_bad(v text,CONSTRAINT bad CHECK(char_length(v)<=2)); INSERT INTO sl_bad VALUES('abc');")); err == nil {
				t.Fatal("failed Restore accepted")
			}
			run(referenceSelect("SELECT n FROM sl_restore ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}}))
		}
	})
}
