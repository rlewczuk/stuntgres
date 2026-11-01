package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceXMLXPathArrayControls(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const document = `<?xml version="1.0" encoding="UTF8"standalone="yes"?><a/>`
		const declaration = `<?xml version="1.0" encoding="LATIN1"?><r>é</r>`
		const array = `[0:1][-1:0]={{"<?xml version=\"1.0\" encoding=\"UTF8\"standalone=\"yes\"?><a/>",x<y/>},{"",NULL}}`
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE public.xpath_array_controls(id integer,v xml[])", 0),
			fixture.Exec("SET XML OPTION DOCUMENT", 0),
			fixture.Exec("INSERT INTO public.xpath_array_controls(id,v[0:1][-1:0]) VALUES(1,ARRAY[ARRAY[XMLPARSE(DOCUMENT "+xpathSQL(document)+"),XMLPARSE(CONTENT 'x<y/>')],ARRAY[XMLPARSE(CONTENT ''),NULL::xml]])", 1),
			fixture.Exec(`INSERT INTO public.xpath_array_controls VALUES(2,xpath('""','<r/>'::xml))`, 1),
			fixture.Exec("INSERT INTO public.xpath_array_controls VALUES(3,$1::xml[])", 1, `[0:0][-1:0]={{"<?xml version=\"1.0\" encoding=\"LATIN1\"?><r>é</r>",NULL}}`),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		xpathFailure(t, p, b, c, "SELECT XMLPARSE(CONTENT "+xpathSQL(document)+")", xpathError("2200N", "invalid XML content: invalid XML declaration", "Space required.", 0), false)
		checks := func(backend fixture.Backend) []fixture.Step {
			leaves := fixture.Select("SELECT v[0][-1]::text AS doc_leaf,v[0][0]::text AS fragment_leaf,v[1][-1]::text AS empty_leaf,v[1][0]::text AS null_leaf,v[1][-1] IS NULL AS empty_null,v[1][0] IS NULL AS hole_null FROM public.xpath_array_controls WHERE id=1", []string{"doc_leaf", "fragment_leaf", "empty_leaf", "null_leaf", "empty_null", "hole_null"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{document, "x<y/>", "", nil, false, true}})
			leaves.CheckMetadata = resultsMetadata(backend, leaves.Columns, leaves.Types, false, resultsTextMetadata, resultsTextMetadata, resultsTextMetadata, resultsTextMetadata, resultsNativeColumn{}, resultsNativeColumn{})
			shape := fixture.Select("SELECT id,array_dims(v) AS dimensions,cardinality(v) AS elements FROM public.xpath_array_controls ORDER BY id", []string{"id", "dimensions", "elements"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "[0:1][-1:0]", int64(4)}, {int64(2), "[1:1]", int64(1)}, {int64(3), "[0:0][-1:0]", int64(2)}})
			shape.CheckMetadata = resultsMetadata(backend, shape.Columns, shape.Types, false, resultsNullableMetadata, resultsTextMetadata, resultsNativeColumn{})
			bounds := fixture.Select("SELECT array_ndims(v) AS rank,array_lower(v,1) AS l1,array_upper(v,1) AS u1,array_length(v,1) AS n1,array_lower(v,2) AS l2,array_upper(v,2) AS u2,array_length(v,2) AS n2 FROM public.xpath_array_controls WHERE id=1", []string{"rank", "l1", "u1", "n1", "l2", "u2", "n2"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(0), int64(1), int64(2), int64(-1), int64(0), int64(2)}})
			bounds.CheckMetadata = resultsMetadata(backend, bounds.Columns, bounds.Types, false)
			return []fixture.Step{
				xpathStoredArrayStep("SELECT v FROM public.xpath_array_controls WHERE id=1", backend.PG, array),
				xpathStoredArrayStep("SELECT v FROM public.xpath_array_controls WHERE false", backend.PG),
				leaves, shape, bounds,
				xpathStep(backend, "SELECT v[0][-1]::text AS v FROM public.xpath_array_controls WHERE id=3", "TEXT", declaration),
				xpathStep(backend, "SELECT v[1]::text AS v FROM public.xpath_array_controls WHERE id=2", "TEXT", ""),
				xpathStep(backend, "SELECT v[0][0] IS NULL AS v FROM public.xpath_array_controls WHERE id=3", "BOOL", true),
			}
		}
		check := func(conn *sql.Conn, backend fixture.Backend) {
			for _, s := range checks(backend) {
				fixture.SQLJSONBoth(t, p, backend, conn, s)
			}
		}
		check(c, b)
		if b.PG {
			return
		}
		raw := checks(b)[0]
		oldArray, err := c.QueryContext(p.Ctx, raw.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer oldArray.Close()
		raw.CheckMetadata(t, oldArray)
		old, err := c.QueryContext(p.Ctx, "SELECT v[0][-1]::text AS v FROM public.xpath_array_controls WHERE id=3")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Native.ExecContext(p.Ctx, "SET XML OPTION DOCUMENT"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(c, b)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if dump.String() != redump.String() {
			t.Fatal("array declaration/mode replay changed deterministic dump")
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE public.xpath_array_controls SET v='{<new/>}'::xml[] WHERE id=1", 1))
		fixture.SQLJSONBoth(t, p, b, c, xpathStoredArrayStep("SELECT v FROM public.xpath_array_controls WHERE id=1", false, "{<new/>}"))
		fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, "SELECT v[1]::text AS v FROM public.xpath_array_controls WHERE id=1", "TEXT", "<new/>"))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE public.xpath_array_controls SET v='{<replacement/>}'::xml[] WHERE id=3", 1))
		if !oldArray.Next() {
			t.Fatal("retained array lost row", oldArray.Err())
		}
		var retainedArray any
		if err := oldArray.Scan(&retainedArray); err != nil {
			t.Fatal(err)
		}
		fixture.AssertRawValue(t, false, "_XML", retainedArray)
		if fixture.Normalize(retainedArray) != array || oldArray.Next() || oldArray.Err() != nil {
			t.Fatal("retained array changed", retainedArray, oldArray.Err())
		}
		if !old.Next() {
			t.Fatal("retained declaration cursor lost its row", old.Err())
		}
		var retained string
		if err := old.Scan(&retained); err != nil || retained != declaration {
			t.Fatal("retained declaration changed", retained, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained declaration cursor tail", old.Err())
		}
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/xpath-array-target")
		if err != nil {
			t.Fatal(err)
		}
		target := sql.OpenDB(connector)
		defer target.Close()
		target.SetMaxOpenConns(1)
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		defer recorded.Close()
		targetConn, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer targetConn.Close()
		targetBackend := fixture.Backend{Recorder: recorder}
		for _, q := range []string{"CREATE TABLE public.xpath_array_spacer(v integer)", "CREATE DOMAIN public.xpath_array_spacer_domain AS text"} {
			fixture.RunStep(t, p.Ctx, targetConn, recorder, fixture.Exec(q, 0))
		}
		if _, err := target.ExecContext(p.Ctx, "SET XML OPTION DOCUMENT"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(targetConn, targetBackend)
		var independent bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, target, &independent); err != nil {
			t.Fatal(err)
		}
		if independent.String() != dump.String() {
			t.Fatal("independent XML[] replay changed dump")
		}
		fixture.RunStep(t, p.Ctx, targetConn, recorder, fixture.Exec("CREATE TABLE public.xpath_array_sentinel(v integer)", 0))
		fixture.RunStep(t, p.Ctx, targetConn, recorder, fixture.Exec("INSERT INTO public.xpath_array_sentinel VALUES(42)", 1))
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, target, &before); err != nil {
			t.Fatal(err)
		}
		bad := `CREATE TABLE public.xpath_array_staged(v xml[]); INSERT INTO public.xpath_array_staged VALUES(xpath('/r','<r/>'::xml)); INSERT INTO public.xpath_array_staged VALUES(xpath('[','<r/>'::xml,'{}'::text[]));`
		d := xpathError("10608", "invalid XPath expression", "Invalid expression", 0)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, target, strings.NewReader(bad)), &d)
		fixture.SQLJSONBoth(t, p, targetBackend, targetConn, xpathStep(targetBackend, "SELECT v FROM public.xpath_array_sentinel AS s(v)", "INT4", int64(42)))
		check(targetConn, targetBackend)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, target, &after); err != nil {
			t.Fatal(err)
		}
		if before.String() != after.String() {
			t.Fatal("failed staged replay changed independent target")
		}
		// Execute the actual emitted SQL, unchanged, on the pinned public oracle.
		if p.Postgres != nil {
			pg, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pg.Close()
			pgBackend := fixture.Backend{PG: true, Recorder: p.PostgresRecord}
			for _, q := range []string{"DROP SCHEMA public CASCADE; CREATE SCHEMA public", "SET XML OPTION DOCUMENT", dump.String()} {
				if _, err := pg.ExecContext(p.Ctx, q); err != nil {
					t.Fatal("PostgreSQL emitted XML[] replay", err)
				}
			}
			check(pg, pgBackend)
			if _, err := pg.ExecContext(p.Ctx, "RESET xmloption; DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
	})
}
