package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
)

// One shared SQL contract validates both backends and actual emitted native
// Dump SQL. Native control phases have no PostgreSQL API equivalent.
func stringEncodingLifecycleSQL(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	for _, s := range []referenceStep{
		referenceExec(`CREATE TABLE se_life(id int,n int DEFAULT 65,s text DEFAULT chr(65),u text DEFAULT unistr($$\0041$$),a text DEFAULT to_ascii('é','LATIN1'::name),i text DEFAULT to_ascii('é',8),CONSTRAINT se_life_ck CHECK(unicode_assigned(s) AND ascii(s)=65))`, 0),
		referenceExec("INSERT INTO se_life(id) VALUES(1)", 1), referenceExec("INSERT INTO se_life VALUES(2,NULL,NULL,NULL,NULL,NULL)", 1),
		referenceExec("CREATE VIEW se_life_view AS SELECT id,ascii(s) AS code,chr(n) AS scalar,to_ascii(a,'LATIN1'::name) AS named,to_ascii(i,8) AS numbered,unistr(u) AS escaped,unicode_assigned(s) AS assigned FROM se_life", 0),
	} {
		referenceRunStep(t, p.ctx, c, b.recorder, s)
	}
	want := stringEncodingLifecycleRows(b.pg)
	stringEncodingBoth(t, p, b, c, want)
	stringEncodingHeld(t, p, b, c, want.query, []referenceStep{want, want})
	for _, tc := range []struct{ number, expr string }{{"3", "chr(65)"}, {"5", "to_ascii('é'::text, 'LATIN1'::name)"}, {"6", "to_ascii('é'::text, 8)"}} {
		q := "SELECT pg_get_expr(d.adbin,d.adrelid,true) AS expression FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='se_life' AND d.adnum=" + tc.number
		stringEncodingBoth(t, p, b, c, referenceSelect(q, []string{"expression"}, []string{"TEXT"}, [][]any{{tc.expr}}))
	}
	view := " SELECT id,\n    ascii(s) AS code,\n    chr(n) AS scalar,\n    to_ascii(a, 'LATIN1'::name) AS named,\n    to_ascii(i, 8) AS numbered,\n    unistr(u) AS escaped,\n    unicode_assigned(s) AS assigned\n   FROM se_life;"
	stringEncodingBoth(t, p, b, c, referenceSelect("SELECT pg_get_viewdef('se_life_view'::regclass,true) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{view}}))
	if !b.pg {
		stringEncodingLifecycleControls(t, p, b, c, want)
	}
}

func stringEncodingLifecycleRows(pg bool) referenceStep {
	s := referenceSelect("SELECT id,code,scalar,named,numbered,escaped,assigned FROM se_life_view ORDER BY id", []string{"id", "code", "scalar", "named", "numbered", "escaped", "assigned"}, []string{"INT4", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{int64(1), int64(65), "A", "AC", "AC", "A", true}, {int64(2), nil, nil, nil, nil, nil, nil}})
	s.checkMetadata = stringEncodingViewMetadata(pg, s.columns, s.types)
	return s
}

func TestReferenceStringEncodingLifecycle(t *testing.T) {
	stringEncodingScenario(t, stringEncodingLifecycleSQL)
}

func TestReferenceStringEncodingFeatureSmoke(t *testing.T) {
	stringEncodingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		s := referenceSelect(`SELECT ascii('é') AS a,chr(128578) AS c,to_ascii('é','LATIN1') AS t,unistr($$\D83D\DE42$$) AS u,unicode_assigned(chr(129769)) AS assigned,unicode_assigned(chr(888)) AS missing`, []string{"a", "c", "t", "u", "assigned", "missing"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(233), "🙂", "AC", "🙂", true, false}})
		s.checkMetadata = stringEncodingMetadata(b.pg, s.columns, s.types)
		stringEncodingBoth(t, p, b, c, s)
		stringEncodingFailure(t, p, b, c, "SELECT to_ascii('')", controlsReferenceDiagnostic("0A000", "encoding conversion from UTF8 to ASCII not supported"), false)
		q := "SELECT ascii($1) AS a,chr($2) AS c,to_ascii($1,$3) AS t,unistr($4) AS u,unicode_assigned($1) AS assigned"
		names, types := []string{"a", "c", "t", "u", "assigned"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL"}
		valid := referenceSelect(q, names, types, [][]any{{int64(233), "🙂", "AC", "🙂", true}}, "é", 128578, "LATIN1", `\D83D\uDE42`)
		bad := referenceErrorDiagnostic(q, controlsReferenceDiagnostic("54000", "null character not permitted"), "é", 0, "LATIN1", `\0041`)
		bad.selectRows, bad.errorStage = true, "execute"
		d := controlsReferenceDiagnostic("42601", "invalid Unicode escape")
		d.Hint = `Unicode escapes must be \XXXX, \+XXXXXX, \uXXXX, or \UXXXXXXXX.`
		escape := referenceErrorDiagnostic(q, d, "é", 65, "LATIN1", `\xyz`)
		escape.selectRows, escape.errorStage = true, "execute"
		stringEncodingHeld(t, p, b, c, q, []referenceStep{valid, referenceSelect(q, names, types, [][]any{{nil, nil, nil, nil, nil}}, nil, nil, nil, nil), bad, valid, escape, valid})
		stringEncodingLifecycleSQL(t, p, b, c)
	})
}

func stringEncodingLifecycleControls(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, want referenceStep) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	stmt, err := c.PrepareContext(p.ctx, want.query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: want.query}
	old, err := c.QueryContext(p.ctx, want.query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	meta, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	stringEncodingColumnContract(t, meta, false, want.columns, want.types, true)
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	lexicalRows(t, false, old, want, nil)
	stringEncodingColumnContract(t, meta, false, want.columns, want.types, true)
	referenceRunStep(t, p.ctx, held, b.recorder, want)
	stringEncodingBoth(t, p, b, c, want)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != redump.String() {
		t.Fatal("canonical restored dump changed", dump.String(), redump.String())
	}
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.postgres.ExecContext(p.ctx, redump.String()); err != nil {
			t.Fatal("actual emitted native SQL replay", err)
		}
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, stringEncodingLifecycleRows(true))
		for _, tc := range []struct{ number, expr string }{{"5", "to_ascii('é'::text, 'LATIN1'::name)"}, {"6", "to_ascii('é'::text, 8)"}} {
			q := "SELECT pg_get_expr(d.adbin,d.adrelid,true) AS expression FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='se_life' AND d.adnum=" + tc.number
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect(q, []string{"expression"}, []string{"TEXT"}, [][]any{{tc.expr}}))
		}
		if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
	// A canceled native control must not publish or erase any state.
	ctx, cancel := context.WithCancel(p.ctx)
	cancel()
	var canceledDump bytes.Buffer
	lexicalOrdinary(t, stuntgres.Dump(ctx, p.native, &canceledDump), context.Canceled)
	if canceledDump.Len() != 0 {
		t.Fatal("canceled Dump emitted bytes")
	}
	lexicalOrdinary(t, stuntgres.Reset(ctx, p.native), context.Canceled)
	lexicalOrdinary(t, stuntgres.Restore(ctx, p.native, strings.NewReader("CREATE TABLE se_canceled(s text);")), context.Canceled)
	referenceRunStep(t, p.ctx, held, b.recorder, want)
	for _, tc := range []struct{ sql, code, message string }{
		{"CREATE TABLE se_bad(s text); INSERT INTO se_bad VALUES(chr(65)); INSERT INTO se_bad VALUES(chr(0));", "54000", "null character not permitted"},
		{`CREATE TABLE se_bad(s text); INSERT INTO se_bad VALUES(unistr($$\0041$$)); INSERT INTO se_bad VALUES(unistr($$\xyz$$));`, "42601", "invalid Unicode escape"},
	} {
		d := controlsReferenceDiagnostic(tc.code, tc.message)
		if tc.code == "42601" {
			d.Hint = `Unicode escapes must be \XXXX, \+XXXXXX, \uXXXX, or \UXXXXXXXX.`
		}
		referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, p.native, strings.NewReader(tc.sql)), &d)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != redump.String() {
			t.Fatal("failed Restore changed committed catalog")
		}
		referenceRunStep(t, p.ctx, held, b.recorder, want)
	}
	// Parameter-free Restore scalar evaluation covers every selected function.
	valid := `CREATE TABLE se_restored(a int,c text,n text,i text,u text,g bool); INSERT INTO se_restored VALUES(ascii('é'),chr(128578),to_ascii('é','LATIN1'::name),to_ascii('é',8),unistr($$\D83D\DE42$$),unicode_assigned(chr(888)));`
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
	stringEncodingBoth(t, p, b, c, referenceSelect("SELECT a,c,n,i,u,g FROM se_restored", []string{"a", "c", "n", "i", "u", "g"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{int64(233), "🙂", "AC", "AC", "🙂", false}}))
	// Restore the original catalog, proving the SAME held statement reanalyzes
	// after both an intervening different catalog and native replacement.
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, held, b.recorder, want)
}
