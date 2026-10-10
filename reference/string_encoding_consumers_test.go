package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceStringEncodingConsumers(t *testing.T) {
	stringEncodingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, s := range []referenceStep{
			referenceExec(`CREATE TABLE se_stored(id int,s text DEFAULT chr(65),u text DEFAULT unistr($$\0041$$),n text DEFAULT to_ascii('é','LATIN1'::name),i text DEFAULT to_ascii('é',8),CONSTRAINT se_check CHECK(unicode_assigned(s) AND ascii(s)=65))`, 0),
			referenceExec("INSERT INTO se_stored(id) VALUES(1)", 1), referenceExec("INSERT INTO se_stored VALUES(2,NULL,NULL,NULL,NULL)", 1),
			referenceExec("CREATE VIEW se_view AS SELECT id,ascii(s) AS a,chr(ascii(s)) AS c,to_ascii(n,'LATIN1'::name) AS n,to_ascii(i,8) AS i,unistr(u) AS u,unicode_assigned(s) AS assigned FROM se_stored", 0),
			referenceExec("CREATE TABLE se_generated(id int,s text,n int,a int GENERATED ALWAYS AS (ascii(s)) STORED,c text GENERATED ALWAYS AS (chr(n)) STORED,t text GENERATED ALWAYS AS (to_ascii(s,'LATIN1')) STORED,u text GENERATED ALWAYS AS (unistr(s)) STORED,g bool GENERATED ALWAYS AS (unicode_assigned(s)) STORED)", 0),
			referenceExec("CREATE INDEX se_ascii_idx ON se_generated ((ascii(s)))", 0), referenceExec("CREATE INDEX se_chr_idx ON se_generated ((chr(n)))", 0),
			referenceExec("CREATE INDEX se_toascii_idx ON se_generated ((to_ascii(s,8)))", 0), referenceExec("CREATE INDEX se_unistr_idx ON se_generated ((unistr(s)))", 0), referenceExec("CREATE INDEX se_assigned_idx ON se_generated ((unicode_assigned(s)))", 0),
			referenceExec("INSERT INTO se_generated(id,s,n) VALUES(1,'é',128578),(2,NULL,NULL)", 2),
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		stringEncodingBoth(t, p, b, c, referenceSelect("SELECT id,a,c,n,i,u,assigned FROM se_view ORDER BY id", []string{"id", "a", "c", "n", "i", "u", "assigned"}, []string{"INT4", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{int64(1), int64(65), "A", "AC", "AC", "A", true}, {int64(2), nil, nil, nil, nil, nil, nil}}))
		stringEncodingBoth(t, p, b, c, referenceSelect("SELECT id,a,c,t,u,g FROM se_generated ORDER BY id", []string{"id", "a", "c", "t", "u", "g"}, []string{"INT4", "INT4", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{int64(1), int64(233), "🙂", "AC", "é", true}, {int64(2), nil, nil, nil, nil, nil}}))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("UPDATE se_generated SET s='A',n=65 WHERE id=1", 1))
		stringEncodingBoth(t, p, b, c, referenceSelect("SELECT a,c,t,u,g FROM se_generated WHERE id=1", []string{"a", "c", "t", "u", "g"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{int64(65), "A", "A", "A", true}}))
		stringEncodingBoth(t, p, b, c, referenceSelect("SELECT id FROM se_generated WHERE ascii(s)=65 AND unicode_assigned(s) ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		d := controlsReferenceDiagnostic("23514", `new row for relation "se_stored" violates check constraint "se_check"`)
		d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (3, B, A, AC, AC).", "public", "se_stored", "se_check"
		for _, prepared := range []bool{false, true} {
			s := referenceErrorDiagnostic("INSERT INTO se_stored(id,s) VALUES(3,'B')", d)
			s.errorStage = "execute"
			if prepared {
				s = referencePrepared(s)
			}
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		for _, tc := range []struct{ number, want string }{{"2", "chr(65)"}, {"4", "to_ascii('é'::text, 'LATIN1'::name)"}, {"5", "to_ascii('é'::text, 8)"}} {
			q := "SELECT pg_get_expr(d.adbin,d.adrelid,true) AS d FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='se_stored' AND d.adnum=" + tc.number
			stringEncodingBoth(t, p, b, c, referenceSelect(q, []string{"d"}, []string{"TEXT"}, [][]any{{tc.want}}))
		}
		stringEncodingBoth(t, p, b, c, referenceSelect("SELECT pg_get_expr(conbin,conrelid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='se_check'", []string{"d"}, []string{"TEXT"}, [][]any{{"unicode_assigned(s) AND ascii(s) = 65"}}))
	})
}

func TestReferenceStringEncodingWrites(t *testing.T) {
	stringEncodingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, s := range []referenceStep{referenceExec("CREATE TABLE se_sink(id int,s text)", 0), referenceExec("INSERT INTO se_sink VALUES(0,'sentinel')", 1), referenceExec("CREATE TABLE se_input(id int,n int,u text)", 0), referenceExec(`INSERT INTO se_input VALUES(1,65,$$\0041$$),(2,0,$$\xyz$$)`, 2)} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		for _, tc := range []struct{ q, code, message string }{
			{"INSERT INTO se_sink VALUES(1,chr(65)),(2,chr(0))", "54000", "null character not permitted"},
			{`INSERT INTO se_sink VALUES(1,unistr($$\0041$$)),(2,unistr($$\xyz$$))`, "42601", "invalid Unicode escape"},
			{"INSERT INTO se_sink SELECT id,chr(n) FROM se_input ORDER BY id", "54000", "null character not permitted"},
			{"INSERT INTO se_sink SELECT id,unistr(u) FROM se_input ORDER BY id", "42601", "invalid Unicode escape"},
			{"UPDATE se_sink SET s=chr(0)", "54000", "null character not permitted"},
		} {
			d := controlsReferenceDiagnostic(tc.code, tc.message)
			if tc.message == "invalid Unicode escape" {
				d.Hint = `Unicode escapes must be \XXXX, \+XXXXXX, \uXXXX, or \UXXXXXXXX.`
			}
			for _, prepared := range []bool{false, true} {
				s := referenceErrorDiagnostic(tc.q, d)
				s.errorStage = "execute"
				if prepared {
					s = referencePrepared(s)
				}
				referenceRunStep(t, p.ctx, c, b.recorder, s)
				stringEncodingBoth(t, p, b, c, referenceSelect("SELECT id,s FROM se_sink ORDER BY id", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "sentinel"}}))
			}
		}
		for _, tc := range []struct{ q, code, message string }{{"SELECT chr(n) FROM se_input ORDER BY id", "54000", "null character not permitted"}, {"SELECT unistr(u) FROM se_input ORDER BY id", "42601", "invalid Unicode escape"}} {
			d := controlsReferenceDiagnostic(tc.code, tc.message)
			if tc.code == "42601" {
				d.Hint = `Unicode escapes must be \XXXX, \+XXXXXX, \uXXXX, or \UXXXXXXXX.`
			}
			stringEncodingFailure(t, p, b, c, tc.q, d, false)
		}
	})
}

func TestReferenceStringEncodingAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"se_reader"})
	stringEncodingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, s := range []referenceStep{referenceExec("CREATE TABLE se_acl(s text,n int)", 0), referenceExec("INSERT INTO se_acl VALUES('A',65)", 1), referenceExec("CREATE ROLE se_reader", 0)} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		if b.pg {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("GRANT USAGE ON SCHEMA public TO se_reader", 0))
		}
		defer c.ExecContext(p.ctx, "RESET ROLE")
		q := "SELECT ascii(s) AS a,chr(n) AS c,to_ascii(s,'LATIN1') AS t,unistr(s) AS u,unicode_assigned(s) AS g FROM se_acl"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, empty := range []bool{false, true} {
			if empty {
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("DELETE FROM se_acl", 1))
			}
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET ROLE se_reader", 0))
			d := controlsReferenceDiagnostic("42501", "permission denied for table se_acl")
			stringEncodingFailure(t, p, b, c, q, d, false)
			s := referenceErrorDiagnostic(q, d)
			s.selectRows, s.errorStage = true, "execute"
			referenceRunStep(t, p.ctx, held, b.recorder, s)
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("RESET ROLE", 0))
			var want [][]any
			if !empty {
				want = [][]any{{int64(65), "A", "A", "A", true}}
			}
			referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"a", "c", "t", "u", "g"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL"}, want))
		}
	})
}
