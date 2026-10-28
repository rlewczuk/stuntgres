package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceXMLControls(t *testing.T) {
	t.Run("stored", func(t *testing.T) { xmlScenario(t, xmlStoredControls) })
	t.Run("accepted-mode", func(t *testing.T) { xmlScenario(t, xmlAcceptedModeControls) })
}

func xmlStoredControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE DOMAIN xml_stored_text AS text", 0),
		fixture.Exec(`CREATE TABLE xml_stored(id int, source text DEFAULT '<a/>', v xml DEFAULT XMLPARSE(DOCUMENT '<a/>'), rendered text GENERATED ALWAYS AS (XMLSERIALIZE(CONTENT XMLPARSE(CONTENT source PRESERVE WHITESPACE) AS text NO INDENT)) STORED, CHECK(XMLSERIALIZE(CONTENT v AS text) <> 'forbidden'))`, 0),
		fixture.Exec("INSERT INTO xml_stored(id) VALUES(1)", 1),
		fixture.Exec("INSERT INTO xml_stored(id,source,v) VALUES(2,'text<b/>',XMLPARSE(CONTENT 'text<b/>'))", 1),
		fixture.Exec(`CREATE VIEW xml_stored_view AS SELECT id,XMLPARSE(CONTENT rendered STRIP WHITESPACE) AS parsed,XMLSERIALIZE(CONTENT v AS name) AS named,XMLSERIALIZE(CONTENT v AS xml_stored_text NO INDENT) AS texted,XMLSERIALIZE(CONTENT v AS varchar(20)) AS limited FROM xml_stored`, 0),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
	want := fixture.Select("SELECT id,parsed,named,texted,limited FROM xml_stored_view ORDER BY id", []string{"id", "parsed", "named", "texted", "limited"}, []string{"INT4", "XML", "NAME", "TEXT", "VARCHAR"}, [][]any{{int64(1), "<a/>", "<a/>", "<a/>", "<a/>"}, {int64(2), "text<b/>", "text<b/>", "text<b/>", "text<b/>"}})
	xmlBoth(t, p, b, c, want)
	subquery := xmlSelect("SELECT XMLPARSE(CONTENT (SELECT source FROM xml_stored WHERE id=$1)) AS v", "XML", "<a/>")
	subquery.Args = []any{1}
	xmlBoth(t, p, b, c, subquery)
	// Catalog deparse must remain executable, not merely contain XML spellings.
	var definition string
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('xml_stored_view'::regclass,true)").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW xml_stored_replay AS "+definition, 0))
	replayed := want
	replayed.Query = "SELECT id,parsed,named,texted,limited FROM xml_stored_replay ORDER BY id"
	xmlBoth(t, p, b, c, replayed)
	if b.PG {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	xmlBoth(t, p, b, c, want)
	xmlBoth(t, p, b, c, replayed)
	if p.Postgres != nil {
		xmlReplayPostgres(t, p, dump.String(), want)
	}
}

func xmlAcceptedModeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	original := []any{"<a/>", "text<a/>", "", nil, `<a>é 'quote' &amp; "double"</a>`, `<?xml version="1.0" encoding="LATIN1"?><a>é</a>`, `<!DOCTYPE a [<!ENTITY e "hello">]><a>&e;</a>`, xmlAdjacency}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE xml_control(id int,v xml)", 0))
	for i, v := range original {
		mode := "CONTENT"
		if i == len(original)-1 {
			mode = "DOCUMENT"
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xml_control VALUES($1,XMLPARSE("+mode+" $2))", 1, i+1, v))
	}
	// Use raw characters, not typed-output normalization, to prove exact retained spelling.
	want := xmlSelect("SELECT v::text AS v FROM xml_control ORDER BY id", "TEXT", original...)
	xmlBoth(t, p, b, c, want)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET XML OPTION DOCUMENT", 0))
	xmlBoth(t, p, b, c, want)
	if b.PG {
		return
	} // Explicit native-control phase in this shared scenario.
	old, err := c.QueryContext(p.Ctx, "SELECT v AS v FROM xml_control ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	xmlMetadata(t, old, false, true)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	xmlMetadata(t, old, false, true)
	xmlBoth(t, p, b, c, want)
	typed := append([]any(nil), original...)
	typed[5] = "<a>é</a>"
	for i, w := range typed {
		if !old.Next() {
			t.Fatal("lost old cursor", i, old.Err())
		}
		var got any
		if err := old.Scan(&got); err != nil {
			t.Fatal(err)
		}
		fixture.AssertRawValue(t, false, "XML", got)
		if fixture.Normalize(got) != w {
			t.Fatalf("old XML row %d got %#v want %#v", i, got, w)
		}
	}
	if old.Next() || old.Err() != nil {
		t.Fatal("old cursor tail", old.Err())
	}
	old.Close()
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != redump.String() {
		t.Fatal("XML requested mode or spelling changed after restore", dump.String(), redump.String())
	}
	if p.Postgres != nil {
		xmlReplayPostgres(t, p, dump.String(), want)
	}
	// Successful staged rows must not escape a later malformed XML/read/cancel.
	prefix := `CREATE TABLE staged_xml(v xml); INSERT INTO staged_xml VALUES(XMLPARSE(CONTENT '<ok/>')); `
	bad := prefix + `INSERT INTO staged_xml VALUES(XMLPARSE(CONTENT '<a>'));`
	d := xmlError("2200N", "invalid XML content", xmlBadA, 0)
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &d)
	readErr := errors.New("XML restore reader failed")
	err = stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(strings.NewReader(prefix), &fixture.ReferenceRangeErrorReader{Err: readErr}))
	if !errors.Is(err, readErr) {
		t.Fatalf("reader failure %v", err)
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	err = stuntgres.Restore(ctx, p.Native, &xmlCancelReader{Reader: strings.NewReader(prefix), cancel: cancel})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("restore cancellation %v", err)
	}
	for _, expr := range []string{"XMLPARSE(CONTENT $1)", "XMLSERIALIZE(CONTENT NULL::xml AS varchar($1))", "XMLPARSE(CONTENT NULL::text || $1)"} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE staged_xml(v xml); INSERT INTO staged_xml VALUES("+expr+");")); err == nil {
			t.Fatal("Restore accepted nested parameter", expr)
		}
	}
	var after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
		t.Fatal(err)
	}
	if after.String() != redump.String() {
		t.Fatal("failed staging replaced accepted XML catalog")
	}
	xmlBoth(t, p, b, c, want)
}

type xmlCancelReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r *xmlCancelReader) Read(p []byte) (int, error) {
	n, e := r.Reader.Read(p)
	r.cancel()
	return n, e
}

func xmlReplayPostgres(t *testing.T, p *fixture.Pair, script string, want fixture.Step) {
	t.Helper()
	for _, q := range []string{"DROP SCHEMA public CASCADE; CREATE SCHEMA public", "SET XML OPTION DOCUMENT", script} {
		if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
			t.Fatal("actual emitted XML dump replay", err)
		}
	}
	fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, want)
	if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
}
