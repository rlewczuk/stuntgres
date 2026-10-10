package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceNumericFormattingConsumers(t *testing.T) {
	numericFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE nf_query(id integer,n numeric,src text,mask text)", 0))
		run(referenceExec("INSERT INTO nf_query VALUES(1,8,'VIII','RN'),(2,9,'IX','RN'),(3,NULL,NULL,'9.9.9')", 3))
		for _, s := range []referenceStep{
			referenceSelect("SELECT id,lower(to_char(n,'FMRN')) AS roman,CASE WHEN src IS NULL THEN 'missing' ELSE to_char(to_number(src,mask),'FM99') END AS parsed FROM nf_query ORDER BY id", []string{"id", "roman", "parsed"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "viii", "8"}, {int64(2), "ix", "9"}, {int64(3), nil, "missing"}}),
			referenceSelect("SELECT id FROM nf_query WHERE to_char(n,$1::text)=$2::text ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}, "FMRN", "VIII"),
			referenceSelect("SELECT a.id,b.id AS peer FROM nf_query a JOIN nf_query b ON to_char(a.n,'FMRN')=to_char(b.n,'FMRN') ORDER BY a.id,b.id", []string{"id", "peer"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}),
			referenceSelect("SELECT to_char(n,'FM9') AS rendered,count(*) AS n FROM nf_query GROUP BY to_char(n,'FM9') ORDER BY rendered NULLS LAST", []string{"rendered", "n"}, []string{"TEXT", "INT8"}, [][]any{{"8", int64(1)}, {"9", int64(1)}, {nil, int64(1)}}),
			referenceSelect("SELECT DISTINCT to_char(n,'FM9') AS rendered FROM nf_query ORDER BY rendered NULLS LAST", []string{"rendered"}, []string{"TEXT"}, [][]any{{"8"}, {"9"}, {nil}}),
			referenceSelect("SELECT min(to_char(n,'FMRN')) AS first,max(to_number(src,mask)) AS last FROM nf_query", []string{"first", "last"}, []string{"TEXT", "NUMERIC"}, [][]any{{"IX", "9"}}),
			referenceSelect("SELECT to_char(n,'FM9') AS rendered,to_char(n,'FMRN') AS roman,count(*) AS n FROM nf_query GROUP BY to_char(n,'FM9'),to_char(n,'FMRN') ORDER BY rendered NULLS LAST", []string{"rendered", "roman", "n"}, []string{"TEXT", "TEXT", "INT8"}, [][]any{{"8", "VIII", int64(1)}, {"9", "IX", int64(1)}, {nil, nil, int64(1)}}),
			referenceSelect("SELECT id FROM nf_query ORDER BY to_number(src,mask) DESC NULLS LAST", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}, {int64(3)}}),
			referenceSelect("VALUES(to_char(12,'FM99')),(to_char(to_number('13','99'),'FM99'))", []string{"column1"}, []string{"TEXT"}, [][]any{{"12"}, {"13"}}),
			referenceSelect("SELECT id FROM nf_query WHERE to_number(src,mask)=8 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
		} {
			t.Run(s.query, func(t *testing.T) { numericFormattingBoth(t, p, b, c, s) })
		}
		for _, prepared := range []bool{false, true} {
			s := referenceExec("INSERT INTO nf_query VALUES(4,to_number('X','RN'),'X','RN')", 1)
			s.prepared = prepared
			run(s)
			s = referenceSelect("UPDATE nf_query SET n=to_number('XI','RN'),src=to_char(11,'FMRN') WHERE id=4 RETURNING id,to_char(n,'FMRN') AS roman", []string{"id", "roman"}, []string{"INT4", "TEXT"}, [][]any{{int64(4), "XI"}})
			s.prepared = prepared
			run(s)
			s = referenceSelect("DELETE FROM nf_query WHERE to_char(n,'FMRN')='XI' RETURNING id,to_char(n,'FM99') AS rendered", []string{"id", "rendered"}, []string{"INT4", "TEXT"}, [][]any{{int64(4), "11"}})
			s.prepared = prepared
			run(s)
		}
	})
}

func numericFormattingAtomic(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
	run(referenceExec("CREATE TABLE nf_atomic(id integer,n numeric,src text,mask text)", 0))
	run(referenceExec("CREATE INDEX nf_atomic_id ON nf_atomic(id)", 0))
	run(referenceExec("INSERT INTO nf_atomic VALUES(10,100,'VIII','RN'),(20,200,'IL','RN')", 2))
	want := [][]any{{int64(10), "100", "VIII", "RN"}, {int64(20), "200", "IL", "RN"}}
	assertState := func() {
		run(referenceSelect("SELECT id,n,src,mask FROM nf_atomic ORDER BY id", []string{"id", "n", "src", "mask"}, []string{"INT4", "NUMERIC", "TEXT", "TEXT"}, want))
		run(referenceSelect("SELECT id FROM nf_atomic WHERE id=10 OR id=20 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(10)}, {int64(20)}}))
	}
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{
			"INSERT INTO nf_atomic VALUES(1,to_number('VIII','RN'),'ok','RN'),(2,to_number('IL','RN'),'bad','RN')",
			"INSERT INTO nf_atomic(id,n,src,mask) SELECT id+100,to_number(src,mask),src,mask FROM nf_atomic ORDER BY id",
			"UPDATE nf_atomic SET n=to_number(src,mask)",
			"DELETE FROM nf_atomic WHERE to_number(src,mask)=8",
		} {
			t.Run(q, func(t *testing.T) {
				s := referenceErrorDiagnostic(q, numericFormattingDiagnostic("22P02", "invalid Roman numeral", "", "", ""))
				s.prepared = prepared
				run(s)
				assertState()
			})
		}
		t.Run("ordered-late-select", func(t *testing.T) {
			s := numericFormattingError("SELECT id,to_number(src,mask) AS n FROM nf_atomic ORDER BY id", numericFormattingDiagnostic("22P02", "invalid Roman numeral", "", "", ""))
			s.prepared = prepared
			run(s) // Query must fail with a nil cursor, not a partial Rows.Err.
		})
	}
	assertState()
}

func TestReferenceNumericFormattingAtomicity(t *testing.T) {
	numericFormattingScenario(t, numericFormattingAtomic)
}

func numericFormattingStored(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
	run(referenceExec("CREATE TABLE nf_consumer(unused integer,id integer,src text,mask text,n numeric DEFAULT to_number('12.50','99.99'),CONSTRAINT nf_positive CHECK(to_number(src,mask)>0))", 0))
	run(referenceExec("CREATE VIEW nf_consumer_view AS SELECT id,to_char(to_number(src,mask),'FMRN') AS roman,n FROM nf_consumer", 0))
	run(referenceExec("INSERT INTO nf_consumer(id,src,mask) VALUES(1,'VIII','RN'),(2,'IX','RN')", 2))
	q := "SELECT id,roman,n FROM nf_consumer_view ORDER BY id"
	want := [][]any{{int64(1), "VIII", "12.50"}, {int64(2), "IX", "12.50"}}
	numericFormattingBoth(t, p, b, c, referenceSelect(q, []string{"id", "roman", "n"}, []string{"INT4", "TEXT", "NUMERIC"}, want))
	for _, prepared := range []bool{false, true} {
		t.Run("check-failure", func(t *testing.T) {
			d := numericFormattingDiagnostic("23514", `new row for relation "nf_consumer" violates check constraint "nf_positive"`, "Failing row contains (null, 3, 0, 9, 12.50).", "", "")
			d.Schema, d.Table, d.Constraint = "public", "nf_consumer", "nf_positive"
			s := referenceErrorDiagnostic("INSERT INTO nf_consumer(id,src,mask) VALUES(3,'0','9')", d)
			s.prepared, s.errorStage = prepared, "execute"
			run(s)
		})
		run(referenceSelect(q, []string{"id", "roman", "n"}, []string{"INT4", "TEXT", "NUMERIC"}, want))
	}
	// These are deparse API results, not numeric-format computed columns.
	// Reuse their existing complete backend metadata contract rather than
	// applying numeric-format's intentionally unknown native nullability.
	for _, v := range []struct{ q, want string }{
		{"SELECT pg_get_viewdef('nf_consumer_view'::regclass,true) AS definition", " SELECT id,\n    to_char(to_number(src, mask), 'FMRN'::text) AS roman,\n    n\n   FROM nf_consumer;"},
		{"SELECT pg_get_constraintdef(oid,true) AS definition FROM pg_constraint WHERE conname='nf_positive'", "CHECK (to_number(src, mask) > 0::numeric)"},
		{"SELECT pg_get_expr(d.adbin,d.adrelid,true) AS definition FROM pg_attrdef d JOIN pg_class c ON d.adrelid=c.oid WHERE c.relname='nf_consumer'", "to_number('12.50'::text, '99.99'::text)"},
	} {
		t.Run(v.q, func(t *testing.T) {
			s := deparseRelationsTextStep(t, b, v.q, v.want)
			s.checkRaw = func(t *testing.T, i, j int, value any) bool {
				referenceAssertRawValue(t, b.pg, "TEXT", value)
				return true
			}
			numericFormattingBoth(t, p, b, c, s)
		})
	}
	run(referenceExec("ALTER TABLE nf_consumer DROP COLUMN unused", 0))
	run(referenceExec("ALTER TABLE nf_consumer RENAME COLUMN src TO source", 0))
	run(referenceExec("INSERT INTO nf_consumer(id,source,mask) VALUES(3,'X','RN')", 1))
	numericFormattingBoth(t, p, b, c, referenceSelect(q, []string{"id", "roman", "n"}, []string{"INT4", "TEXT", "NUMERIC"}, append(want, []any{int64(3), "X", "12.50"})))
}

func TestReferenceNumericFormattingStoredConsumers(t *testing.T) {
	numericFormattingScenario(t, numericFormattingStored)
}

func TestReferenceNumericFormattingStableLegality(t *testing.T) {
	numericFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE nf_stable(n numeric,src text)", 0))
		for _, q := range []string{
			"CREATE TABLE nf_gen_char(n numeric,label text GENERATED ALWAYS AS (to_char(n,'FM99')) STORED)",
			"CREATE TABLE nf_gen_number(src text,n numeric GENERATED ALWAYS AS (to_number(src,'99')) STORED)",
			"CREATE INDEX nf_stable_char ON nf_stable((to_char(n,'FM99')))",
			"CREATE INDEX nf_stable_number ON nf_stable((to_number(src,'99')))",
		} {
			t.Run(q, func(t *testing.T) {
				message := "generation expression is not immutable"
				if strings.HasPrefix(q, "CREATE INDEX") {
					message = "functions in index expression must be marked IMMUTABLE"
				}
				s := referenceErrorDiagnostic(q, numericFormattingDiagnostic("42P17", message, "", "", ""))
				s.errorStage = "execute"
				numericFormattingBoth(t, p, b, c, s)
			})
		}
	})
}
