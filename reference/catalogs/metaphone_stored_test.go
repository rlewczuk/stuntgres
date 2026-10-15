package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func metaphoneStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE SCHEMA phon_addon", 0))
	run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA phon_addon VERSION '1.2'", 0))
	run(fixture.Exec("CREATE TABLE phon_source(id int,name text,cap int DEFAULT 8,d text DEFAULT phon_addon.metaphone('gumbo',4),m text GENERATED ALWAYS AS (phon_addon.metaphone(name,cap)) STORED,p text GENERATED ALWAYS AS (phon_addon.dmetaphone(name)) STORED,a text GENERATED ALWAYS AS (phon_addon.dmetaphone_alt(name)) STORED,CONSTRAINT phon_valid CHECK(phon_addon.metaphone(name,cap)<>'INVALID'))", 0))
	run(fixture.Exec("INSERT INTO phon_source(id,name) VALUES(1,'Smith'),(2,'gumbo'),(3,''),(4,NULL)", 4))
	run(fixture.Exec("CREATE VIEW phon_values AS SELECT id,d,m,p,a,phon_addon.dmetaphone_alt(name) AS recalculated FROM phon_source", 0))
	run(fixture.Exec("CREATE VIEW phon_literals AS SELECT phon_addon.metaphone('AX',2) AS m,phon_addon.dmetaphone('Smith') AS p,phon_addon.dmetaphone_alt('Schmidt') AS a", 0))
	run(metaphoneLiteralView())
	run(fixture.Prepared(metaphoneLiteralView()))
	check := fixture.Select("SELECT id,d,m,p,a,recalculated FROM phon_values ORDER BY id", []string{"id", "d", "m", "p", "a", "recalculated"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{
		{int64(1), "KM", "SM0", "SM0", "XMT", "XMT"}, {int64(2), "KM", "KM", "KMP", "KMP", "KMP"},
		{int64(3), "KM", "", "", "", ""}, {int64(4), "KM", nil, nil, nil, nil},
	})
	run(check)
	run(fixture.Prepared(check))
	return check
}

func TestReferenceMetaphoneStoredExpressions(t *testing.T) {
	metaphoneCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := metaphoneStored(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		failure := metaphoneFailure("INSERT INTO phon_source(id,name,cap) VALUES(5,'gumbo',4),(6,'Smith',0)", "2200F", "output cannot be empty string")
		failure.SelectRows = false
		for _, prepared := range []bool{false, true} {
			failure.Prepared = prepared
			run(failure)
			run(check)
		}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT id FROM phon_source WHERE phon_addon.dmetaphone_alt(name)='XMT' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
			fixture.Select("SELECT phon_addon.dmetaphone(name) AS code,count(*) FROM phon_source GROUP BY phon_addon.dmetaphone(name) ORDER BY code NULLS LAST", []string{"code", "count"}, []string{"TEXT", "INT8"}, [][]any{{"", int64(1)}, {"KMP", int64(1)}, {"SM0", int64(1)}, {nil, int64(1)}}),
			fixture.Select("SELECT a.id,b.id FROM phon_source a JOIN phon_source b ON phon_addon.dmetaphone(a.name)=phon_addon.dmetaphone(b.name) ORDER BY a.id,b.id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}}),
			fixture.Select("SELECT id,CASE WHEN id=1 THEN phon_addon.metaphone(name,8) ELSE phon_addon.dmetaphone(name) END AS code,phon_addon.dmetaphone(phon_addon.metaphone(name,8)) AS nested FROM phon_source WHERE id<=2 ORDER BY id", []string{"id", "code", "nested"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "SM0", "SM"}, {int64(2), "KMP", "KM"}}),
			fixture.Select("SELECT m,p,a FROM phon_values WHERE false", []string{"m", "p", "a"}, []string{"TEXT", "TEXT", "TEXT"}, nil),
			fixture.Select("SELECT count(*)>0 AS linked FROM pg_depend d JOIN pg_proc p ON p.oid=d.refobjid WHERE d.refclassid=1255 AND p.proname='metaphone'", []string{"linked"}, []string{"BOOL"}, [][]any{{true}}),
			fixture.Select("SELECT count(*)>0 AS linked FROM pg_depend d JOIN pg_proc p ON p.oid=d.refobjid WHERE d.refclassid=1255 AND p.proname='dmetaphone'", []string{"linked"}, []string{"BOOL"}, [][]any{{true}}),
			fixture.Select("SELECT count(*)>0 AS linked FROM pg_depend d JOIN pg_proc p ON p.oid=d.refobjid WHERE d.refclassid=1255 AND p.proname='dmetaphone_alt'", []string{"linked"}, []string{"BOOL"}, [][]any{{true}}),
		} {
			run(s)
			run(fixture.Prepared(s))
		}
		run(fixture.Exec("UPDATE phon_source SET name='Schmidt' WHERE id=2", 1))
		run(fixture.Select("SELECT m,p,a FROM phon_values WHERE id=2", []string{"m", "p", "a"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"SKMTT", "XMT", "SMT"}}))
	})
}

// Keep the same literal-call view witness before and after native replacement;
// this exercises persisted raw/bound literal capture, not SQL quoting choices.
func metaphoneLiteralView() fixture.Step {
	return fixture.Select("SELECT m,p,a FROM phon_literals", []string{"m", "p", "a"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"AK", "SM0", "SMT"}})
}
