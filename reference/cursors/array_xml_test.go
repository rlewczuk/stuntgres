package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceArrayElementXMLValues(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		both := func(s fixture.Step) { run(s); run(fixture.Prepared(s)) }
		arrayXMLSource(run)
		both(arrayXMLLeaves())
		for _, s := range arrayXMLPositions() {
			both(s)
		}
		both(fixture.ArrayReferenceSelect("SELECT ARRAY[XMLPARSE(CONTENT '<p/>'), NULL, XMLPARSE(CONTENT '')] AS a, pg_typeof(ARRAY[NULL::xml])::text AS t FROM public.one", []string{"a", "t"}, []string{"_XML", "TEXT"}, []string{"_XML", "TEXT"}, [][]any{{`{<p/>,NULL,""}`, "xml[]"}}, b.PG))
		both(fixture.Select("SELECT array_dims($1::XML[]) AS dims, ($1::XML[])[0]::text AS first, ($1::XML[])[1] IS NULL AS hole FROM public.one", []string{"dims", "first", "hole"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{"[0:1]", "<p/>", true}}, "[0:1]={<p/>,NULL}"))
		both(fixture.Select("SELECT '{}'::xml[] = '{NULL}'::xml[] AS eq FROM public.one", []string{"eq"}, []string{"BOOL"}, [][]any{{false}}))
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("SELECT ARRAY[XMLPARSE(CONTENT '<a/>')] = ARRAY[XMLPARSE(CONTENT '<a/>')] AS eq FROM public.one", fixture.Diagnostic{Code: "42883", Message: "could not identify an equality operator for type xml"})
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			run(s)
		}
		ragged := fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "{{<a/>},{<b/>,<c/>}}"`)
		ragged.Detail = "Multidimensional arrays must have sub-arrays with matching dimensions."
		fixture.ArrayReferenceInputError(t, p, b, c, "{{<a/>},{<b/>,<c/>}}", "XML[]", ragged)
		invalid := fixture.ControlsReferenceDiagnostic("2200N", "invalid XML content")
		invalid.Detail = "line 1: Premature end of data in tag a line 1\n<a>\n   ^"
		fixture.ArrayReferenceInputError(t, p, b, c, "{<a>}", "XML[]", invalid)
	})
}

func TestReferenceArrayElementXMLReplay(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		arrayXMLSource(run)
		leaves, positions := arrayXMLLeaves(), arrayXMLPositions()
		if !b.PG {
			arrayXMLReplayPostgres(t, p, string(fixture.CheckDump(t, p)), append([]fixture.Step{leaves}, positions...)...)
			fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, leaves)
			for _, s := range positions {
				run(s)
				run(fixture.Prepared(s))
			}
		}
		// A stored rank-1 constant has no INSERT destination: its standalone
		// reconstruction must keep the shifted bound and the NULL first leaf.
		run(fixture.Exec("SET XML OPTION DOCUMENT", 0))
		run(fixture.Exec(`CREATE TABLE public.array_xml_default (id INT, a XML[] DEFAULT '[0:1]={NULL,"<?xml version=\"1.0\" encoding=\"UTF8\"standalone=\"yes\"?><a/>"}'::XML[], g XML[] GENERATED ALWAYS AS (a) STORED)`, 0))
		run(fixture.Exec("SET XML OPTION CONTENT", 0))
		run(fixture.Exec("INSERT INTO public.array_xml_default (id) VALUES (1)", 1))
		// The stored generated copy replays as DEFAULT, never as a bounded target.
		check := fixture.Select("SELECT id, array_dims(a) AS dims, a[0] IS NULL AS hole, a[1]::text AS leaf, array_dims(g) AS gdims, g[1]::text AS gleaf FROM public.array_xml_default ORDER BY id", []string{"id", "dims", "hole", "leaf", "gdims", "gleaf"}, []string{"INT4", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "[0:1]", true, arrayXMLDecl, "[0:1]", arrayXMLDecl}})
		run(check)
		if b.PG {
			return
		}
		dump := string(fixture.CheckDump(t, p))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		defaulted := fixture.Exec("INSERT INTO public.array_xml_default (id) VALUES (2)", 1)
		check.Rows = append(check.Rows, []any{int64(2), "[0:1]", true, arrayXMLDecl, "[0:1]", arrayXMLDecl})
		run(defaulted)
		run(check)
		run(fixture.Prepared(check))
		arrayXMLReplayPostgres(t, p, dump+defaulted.Query+";", check, leaves)
	})
}

// A genuinely generated leaf that is not re-inputtable keeps its spelling; its
// replay fails like the scalar owner's and leaves the destination intact.
func TestReferenceArrayElementXMLReplayFailure(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE public.array_xml_generated (a XML[])", 0))
		run(fixture.Exec(`INSERT INTO public.array_xml_generated (a[0:1]) VALUES (ARRAY[XMLPARSE(CONTENT '<ok/>'), xmlroot('<a/>', VERSION 'x" y')])`, 1))
		source := fixture.Select("SELECT array_dims(a) AS dims, a[0]::text AS ok, a[1]::text AS generated FROM public.array_xml_generated", []string{"dims", "ok", "generated"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"[0:1]", "<ok/>", `<?xml version="x" y"?><a/>`}})
		run(source)
		run(fixture.Prepared(source))
		if b.PG {
			return
		}
		emitted := string(fixture.CheckDump(t, p))
		connector, err := stuntgres.NewDriver().OpenConnector("array-xml-replay-failure/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		destinationNative := sql.OpenDB(connector)
		defer destinationNative.Close()
		destination, recorder := fixture.RecordedNativePool(t, connector)
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE public.array_xml_destination (id INT, a XML[])", 0),
			fixture.Exec("INSERT INTO public.array_xml_destination (id, a[2:3]) VALUES (1, ARRAY["+arrayXMLParsed+", NULL])", 1),
		} {
			fixture.RunStep(t, p.Ctx, destination, recorder, s)
		}
		q := "SELECT id, array_dims(a) AS dims, a[2]::text AS leaf, a[3] IS NULL AS hole FROM public.array_xml_destination"
		kept := fixture.Select(q, []string{"id", "dims", "leaf", "hole"}, []string{"INT4", "TEXT", "TEXT", "BOOL"}, [][]any{{int64(1), "[2:3]", arrayXMLDecl, true}})
		heldStatement, err := destination.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer heldStatement.Close()
		held := fixture.HeldStatement{Executor: destination, Statement: heldStatement, SQL: q}
		fixture.RunStep(t, p.Ctx, held, recorder, kept)
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, destinationNative, &before); err != nil {
			t.Fatal(err)
		}
		want := fixture.ControlsReferenceDiagnostic("2200N", "invalid XML content: invalid XML declaration")
		want.Detail = "Parsing XML declaration: '?>' expected."
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, destinationNative, strings.NewReader(emitted)), &want)
		fixture.RunStep(t, p.Ctx, held, recorder, kept)
		fixture.RunStep(t, p.Ctx, destination, recorder, kept)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, destinationNative, &after); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("failed XML array replay changed the destination")
		}
		run(source)
		if again := string(fixture.CheckDump(t, p)); again != emitted {
			t.Fatal("failed destination replay changed the source")
		}
	})
}

// Element domains check each leaf; a domain over xml[] checks the final value at
// its actual bounds, including after bounded native replay.
func TestReferenceArrayDomainXML(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE DOMAIN public.array_xml_doc AS XML CHECK (VALUE IS DOCUMENT)",
			"CREATE DOMAIN public.array_xml_bounded AS XML[] CHECK (array_lower(VALUE, 1) = 5)",
			"CREATE TABLE public.array_xml_domains (id INT, e public.array_xml_doc[], d public.array_xml_bounded)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO public.array_xml_domains (id, e[2:3], d[5:6]) VALUES (1, ARRAY["+arrayXMLParsed+", XMLPARSE(CONTENT '<p/>')], ARRAY[XMLPARSE(CONTENT 'x<y/>'), NULL])", 1))
		run(fixture.Exec("INSERT INTO public.array_xml_domains VALUES (2, ARRAY[XMLPARSE(CONTENT '<q/>')::public.array_xml_doc], NULL)", 1))
		for _, tc := range []struct{ q, domain string }{
			{"INSERT INTO public.array_xml_domains (id, e) VALUES (3, ARRAY[XMLPARSE(CONTENT '<a/><b/>')])", "array_xml_doc"},
			{"INSERT INTO public.array_xml_domains (id, d) VALUES (3, '{<a/>}'::XML[])", "array_xml_bounded"},
		} {
			want := fixture.ControlsReferenceDiagnostic("23514", `value for domain `+tc.domain+` violates check constraint "`+tc.domain+`_check"`)
			want.Schema, want.DataType, want.Constraint = "public", tc.domain, tc.domain+"_check"
			run(fixture.ErrorDiagnostic(tc.q, want))
			run(fixture.Prepared(fixture.ErrorDiagnostic(tc.q, want)))
		}
		// A subscripted destination checks the domain at the assigned bounds.
		slice := fixture.Error("INSERT INTO public.array_xml_domains (id, d[1:2]) VALUES (3, '{<a/>,NULL}'::XML[])", "23514")
		run(slice)
		run(fixture.Prepared(slice))
		check := fixture.Select("SELECT id, pg_typeof(e)::text AS et, pg_typeof(d)::text AS dt, array_dims(e) AS edims, e[2]::text AS e2, e[3]::text AS e3, array_dims(d) AS ddims, d[5]::text AS d5, d[6] IS NULL AS d6 FROM public.array_xml_domains ORDER BY id", []string{"id", "et", "dt", "edims", "e2", "e3", "ddims", "d5", "d6"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{
			{int64(1), "array_xml_doc[]", "array_xml_bounded", "[2:3]", arrayXMLDecl, "<p/>", "[5:6]", "x<y/>", true},
			{int64(2), "array_xml_doc[]", "array_xml_bounded", "[1:1]", nil, nil, nil, nil, true},
		})
		run(check)
		run(fixture.Prepared(check))
		if b.PG {
			return
		}
		arrayXMLReplayPostgres(t, p, string(fixture.CheckDump(t, p)), check)
		fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, check)
	})
}
