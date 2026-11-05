package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const xml2SetsViewQuery = "SELECT id,n2,n3,n4,l2,l3 FROM public.xml2_sets_view ORDER BY id"
const xml2SetsGuardInsert = "INSERT INTO public.xml2_sets_guard VALUES($1::integer,$2::text,$3::text)"

func xml2SetsViewRows() [][]any {
	return [][]any{
		{int64(1), xml2SetsNodes, xml2SetsItems, "<o>" + xml2SetsItems + "</o>", "ABC,D&E", "ABC|D&E"},
		{int64(2), "", "", "<o></o>", "", ""},
		{int64(3), nil, nil, nil, nil, nil},
	}
}

func xml2SetsViewStep() fixture.Step {
	return fixture.Select(xml2SetsViewQuery, []string{"id", "n2", "n3", "n4", "l2", "l3"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, xml2SetsViewRows())
}

func xml2SetsConsumers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE public.xml2_sets_saved(id integer,n2 text DEFAULT xpath_nodeset('<r><a>A</a></r>','/r/a'),n3 text DEFAULT xpath_nodeset('<r><a>A</a></r>','/r/a','i'),n4 text DEFAULT xpath_nodeset('<r><a>A</a></r>','/r/a','o','i'),l2 text DEFAULT xpath_list('<r><a>A</a></r>','/r/a'),l3 text DEFAULT xpath_list('<r><a>A</a></r>','/r/a','|'))", 0),
		fixture.Exec("INSERT INTO public.xml2_sets_saved(id) VALUES(1)", 1),
		fixture.Exec("CREATE TABLE public.xml2_sets_source(id integer,doc text,path text)", 0),
		fixture.Exec("INSERT INTO public.xml2_sets_source VALUES(1,$1,'/r/a'),(2,'<r/>','/missing'),(3,NULL,'[')", 3, xml2SetsDocument),
		fixture.Exec("CREATE VIEW public.xml2_sets_view AS SELECT id,xpath_nodeset(doc,path) AS n2,xpath_nodeset(doc,path,'i') AS n3,xpath_nodeset(doc,path,'o','i') AS n4,xpath_list(doc,path) AS l2,xpath_list(doc,path,'|') AS l3 FROM public.xml2_sets_source", 0),
		fixture.Exec("CREATE TABLE public.xml2_sets_guard(id integer,doc text,path text,CONSTRAINT xml2_sets_guard_xpath CHECK(xpath_list(doc,path,'|') <> ''))", 0),
		fixture.Exec("INSERT INTO public.xml2_sets_guard VALUES(1,'<r><a>A</a></r>','/r/a')", 1),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func xml2SetsSaved(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	xml2Both(t, p, b, c, xml2SetsViewStep())
	xml2Both(t, p, b, c, fixture.Select("SELECT id,n2,n3,n4,l2,l3 FROM public.xml2_sets_saved ORDER BY id", []string{"id", "n2", "n3", "n4", "l2", "l3"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "<a>A</a>", "<i><a>A</a></i>", "<o><i><a>A</a></i></o>", "A", "A"}}))
	// Fresh writes demand the real stored defaults after each replacement.
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.xml2_sets_saved(id) VALUES(2)", 1))
	xml2Both(t, p, b, c, fixture.Select("SELECT n2,n3,n4,l2,l3 FROM public.xml2_sets_saved WHERE id=2", []string{"n2", "n3", "n4", "l2", "l3"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"<a>A</a>", "<i><a>A</a></i>", "<o><i><a>A</a></i></o>", "A", "A"}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM public.xml2_sets_saved WHERE id=2", 1))
}

func xml2SetsCheckError(row string) fixture.Diagnostic {
	d := xml2Error("23514", `new row for relation "xml2_sets_guard" violates check constraint "xml2_sets_guard_xpath"`)
	d.Detail = "Failing row contains (" + row + ")."
	d.Schema, d.Table, d.Constraint = "public", "xml2_sets_guard", "xml2_sets_guard_xpath"
	return d
}

func xml2SetsGuard(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	read := func(extra ...[]any) {
		t.Helper()
		rows := append([][]any{{int64(1), "<r><a>A</a></r>", "/r/a"}}, extra...)
		xml2Both(t, p, b, c, fixture.Select("SELECT id,doc,path FROM public.xml2_sets_guard ORDER BY id", []string{"id", "doc", "path"}, []string{"INT4", "TEXT", "TEXT"}, rows))
	}
	stmt, err := c.PrepareContext(p.Ctx, xml2SetsGuardInsert)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: xml2SetsGuardInsert}
	for _, route := range []string{"direct", "prepared", "held"} {
		x := fixture.Executor(c)
		if route == "held" {
			x = h
		}
		for _, tc := range []struct{ doc, path any }{{"<r><a>A</a></r>", "/r/a"}, {nil, "["}, {"<r>", "["}, {"<r/>", nil}} {
			s := fixture.Exec(xml2SetsGuardInsert, 1, int64(2), tc.doc, tc.path)
			s.Prepared = route == "prepared"
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
			read([]any{int64(2), tc.doc, tc.path})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM public.xml2_sets_guard WHERE id=2", 1))
		}
		for _, tc := range []struct {
			doc, path string
			diagnostic fixture.Diagnostic
		}{
			{"<r/>", "/r/a", xml2SetsCheckError("2, <r/>, /r/a")},
			{"<r><a/></r>", "/r/a", xml2SetsCheckError("2, <r><a/></r>, /r/a")},
			{"<r/>", "[", xml2Compile("Invalid expression")},
		} {
			s := fixture.ErrorDiagnostic(xml2SetsGuardInsert, tc.diagnostic, int64(2), tc.doc, tc.path)
			s.Prepared, s.ErrorStage = route == "prepared", "execute"
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
			read()
		}
	}
	for _, tc := range []struct{ q string; d fixture.Diagnostic }{
		{"INSERT INTO public.xml2_sets_guard VALUES(10,'<r>A</r>','.'),(11,'<r/>','.')", xml2SetsCheckError("11, <r/>, .")},
		{"INSERT INTO public.xml2_sets_guard VALUES(12,'<r>A</r>','.'),(13,'<r/>','[')", xml2Compile("Invalid expression")},
	} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(tc.q, tc.d)
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			read()
		}
	}
}

func xml2SetsConfigure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET client_min_messages TO notice", 0))
	for _, call := range xml2SetCalls() {
		if xml2SetsWrapperName(call) != "" {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER FUNCTION "+call.signature+" SET client_min_messages = warning", 0))
		}
	}
}

func xml2SetsConfigured(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
	t.Helper()
	for _, call := range xml2SetCalls() {
		if xml2SetsWrapperName(call) == "" {
			continue
		}
		xml2Both(t, p, b, c, fixture.Select("SELECT array_to_string(proconfig,',') AS config FROM pg_proc WHERE oid=$1::regprocedure", []string{"config"}, []string{"TEXT"}, [][]any{{"client_min_messages=warning"}}, call.signature))
		args := append([]any(nil), call.args...)
		args[1] = "count(/r/a)"
		xml2Both(t, p, b, c, xml2SetValue("SELECT "+call.expression+" AS value", "<unsupported/>", args...))
		xml2SetsNotices(t, notices)
		xml2Both(t, p, b, c, fixture.Select("SHOW client_min_messages", []string{"client_min_messages"}, []string{"TEXT"}, [][]any{{"notice"}}))
	}
}

// Keep every sibling wrapper unconfigured when the selected wrapper changes.
// These are genuine row-dependent index expressions, not constant SQL folds.
func xml2SetsMixedIndexes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
	t.Helper()
	const nodes = "xpath_nodeset(doc,path)"
	const list = "xpath_list(doc,path)"
	const nestedNodes = "xpath_nodeset('<r><a>' || " + list + " || '</a></r>','/r/a')"
	const nestedList = "xpath_list('<r>' || " + nodes + " || '</r>','/r/a')"
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE TABLE public.xml2_sets_mixed_index(id integer,doc text,path text)", 0),
		fixture.Exec("INSERT INTO public.xml2_sets_mixed_index VALUES(1,'<r><a>A</a></r>','/r/a'),(2,NULL,'[')", 2),
		fixture.Exec("CREATE INDEX xml2_sets_mixed_key ON public.xml2_sets_mixed_index (("+nodes+" || "+list+"))", 0),
		fixture.Exec("CREATE INDEX xml2_sets_mixed_predicate ON public.xml2_sets_mixed_index (id) WHERE "+nodes+" <> '' AND "+list+" <> ''", 0),
		fixture.Exec("CREATE INDEX xml2_sets_mixed_nested_nodes ON public.xml2_sets_mixed_index (("+nestedNodes+"))", 0),
		fixture.Exec("CREATE INDEX xml2_sets_mixed_nested_list ON public.xml2_sets_mixed_index (("+nestedList+"))", 0),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	}
	read := func(extra ...[]any) {
		t.Helper()
		rows := append([][]any{
			{int64(1), "<a>A</a>", "A", "<a>A</a>A", "<a>A</a>", "A"},
			{int64(2), nil, nil, nil, nil, nil},
		}, extra...)
		xml2Both(t, p, b, c, fixture.Select(
			"SELECT id,"+nodes+" AS nodes,"+list+" AS list,"+nodes+" || "+list+" AS mixed,"+nestedNodes+" AS nested_nodes,"+nestedList+" AS nested_list FROM public.xml2_sets_mixed_index ORDER BY id",
			[]string{"id", "nodes", "list", "mixed", "nested_nodes", "nested_list"},
			[]string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, rows))
	}
	read()
	for _, selected := range []string{"xpath_nodeset(text,text)", "xpath_list(text,text)"} {
		for _, action := range []struct {
			clause string
			config any
		}{
			{"SET client_min_messages = warning", "client_min_messages=warning"},
			{"RESET client_min_messages", nil},
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER FUNCTION "+selected+" "+action.clause, 0))
			for _, signature := range []string{"xpath_nodeset(text,text)", "xpath_list(text,text)"} {
				var config any
				if signature == selected {
					config = action.config
				}
				xml2Both(t, p, b, c, fixture.Select("SELECT array_to_string(proconfig,',') AS config FROM pg_proc WHERE oid=$1::regprocedure",
					[]string{"config"}, []string{"TEXT"}, [][]any{{config}}, signature))
			}
			read()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.xml2_sets_mixed_index VALUES(3,'<r><a>B</a></r>','/r/a')", 1))
			read([]any{int64(3), "<a>B</a>", "B", "<a>B</a>B", "<a>B</a>", "B"})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE public.xml2_sets_mixed_index SET doc='<r><a>C</a></r>' WHERE id=3", 1))
			read([]any{int64(3), "<a>C</a>", "C", "<a>C</a>C", "<a>C</a>", "C"})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM public.xml2_sets_mixed_index WHERE id=3", 1))
			read()
			xml2SetsNotices(t, notices)
		}
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE public.xml2_sets_mixed_index", 0))
}

func TestReferenceXML2SetsControls(t *testing.T) {
	for _, schema := range []string{"public", `xml2 sets "schema`} {
		t.Run(schema, func(t *testing.T) {
			if schema != "public" {
				fixture.SchemaReferenceCleanup(t, schema)
			}
			xml2SetsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				qualified := `"` + strings.ReplaceAll(schema, `"`, `""`) + `"`
				if schema != "public" {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SCHEMA "+qualified, 0))
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION xml2 WITH SCHEMA "+qualified, 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path = "+qualified+",public", 0))
				t.Run("MixedIndexConfiguration", func(t *testing.T) { xml2SetsMixedIndexes(t, p, b, c, notices) })
				xml2SetsConfigure(t, p, b, c)
				xml2SetsConfigured(t, p, b, c, notices)
				xml2SetsConsumers(t, p, b, c)
				xml2SetsSaved(t, p, b, c)
				t.Run("CHECKDiagnostics", func(t *testing.T) { xml2SetsGuard(t, p, b, c) })
				xml2SetsControlReplay(t, p, b, c, qualified, notices)
			})
		})
	}
}

func xml2SetsControlReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, qualified string, notices *[]fixture.Diagnostic) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, xml2SetsViewQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: xml2SetsViewQuery}
	fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetsViewStep())
	if b.PG {
		xml2SetsDetach(t, p, b, c, qualified)
		xml2SetsConfigured(t, p, b, c, notices)
		xml2SetsSaved(t, p, b, c)
		xml2SetsGuard(t, p, b, c)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetsViewStep())
		return // Only the replacement API phase below is native-only.
	}
	old, err := c.QueryContext(p.Ctx, xml2SetsViewQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
	at := strings.Index(dump, "CREATE EXTENSION")
	if at < 0 || strings.Index(dump, "CREATE TABLE") <= at || strings.Index(dump, "CREATE VIEW") <= at {
		t.Fatal("provider must precede real consumers", dump)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.StringCoreRetainedRows(t, old, nil, []string{"id", "n2", "n3", "n4", "l2", "l3"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, xml2SetsViewRows(), false)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path = "+qualified+",public", 0))
	xml2SetsConfigured(t, p, b, c, notices)
	xml2SetsSaved(t, p, b, c)
	xml2SetsGuard(t, p, b, c)
	fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetsViewStep())
	before := fixture.ExtensionDump(t, p.Ctx, p.Native)
	ids := xml2IDs(t, p, c)
	readFailure := errors.New("xml2 sets replacement reader failure")
	for _, mode := range []string{"sql", "precanceled", "reader", "late-cancel"} {
		ctx, cancel := context.WithCancel(p.Ctx)
		var input io.Reader = strings.NewReader(dump + "\nINVALID SQL;")
		var ordinary error
		switch mode {
		case "precanceled":
			cancel()
			input, ordinary = strings.NewReader(dump), context.Canceled
		case "reader":
			input, ordinary = io.MultiReader(strings.NewReader(dump), &fixture.ReferenceRangeErrorReader{Err: readFailure}), readFailure
		case "late-cancel":
			input = io.MultiReader(strings.NewReader(dump), &xml2CancelReader{Reader: strings.NewReader("\n"), cancel: cancel})
			ordinary = context.Canceled
		}
		err := stuntgres.Restore(ctx, p.Native, input)
		cancel()
		if err == nil || ordinary != nil && !errors.Is(err, ordinary) {
			t.Fatalf("rejected %s replacement=%v want=%v", mode, err, ordinary)
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before || !reflect.DeepEqual(ids, xml2IDs(t, p, c)) {
			t.Fatal("failed replacement changed dump or identities")
		}
		xml2SetsConfigured(t, p, b, c, notices)
		xml2SetsSaved(t, p, b, c)
		xml2SetsGuard(t, p, b, c)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetsViewStep())
	}
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector("xml2_sets_destination")
	if err != nil {
		t.Fatal(err)
	}
	control := sql.OpenDB(connector)
	defer control.Close()
	native, ok := connector.(*stuntgres.Connector)
	if !ok {
		t.Fatal("missing public native connector")
	}
	var destinationNotices []fixture.Diagnostic
	pool, recorder := fixture.RecordedNativePool(t, native.WithNoticeHandler(func(n stuntgres.Error) {
		destinationNotices = append(destinationNotices, fixture.ExtractDiagnostic(t, &n))
	}))
	dst, err := pool.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	destination := fixture.Backend{Name: "destination", DB: pool, Recorder: recorder}
	if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, dst, recorder, fixture.Exec("SET search_path = "+qualified+",public", 0))
	xml2SetsConfigured(t, p, destination, dst, &destinationNotices)
	xml2SetsSaved(t, p, destination, dst)
	xml2SetsGuard(t, p, destination, dst)
	xml2SetsDetach(t, p, b, c, qualified)
	xml2SetsConfigured(t, p, b, c, notices)
	xml2SetsSaved(t, p, b, c)
	xml2SetsGuard(t, p, b, c)
	fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetsViewStep())
	detached := fixture.ExtensionDump(t, p.Ctx, p.Native)
	// A missing header is reconstructed transiently from the authentic cohort,
	// then retired. Final absence must not prohibit real recipe creation.
	prefix := "CREATE EXTENSION \"xml2\" WITH SCHEMA "
	create, creation := "", ""
	for _, line := range strings.SplitAfter(detached, "\n") {
		if strings.HasPrefix(line, prefix) {
			if create != "" || !strings.HasSuffix(line, " VERSION '1.2';\n") {
				t.Fatal("detached replay repeated or changed genuine cohort creation", detached)
			}
			create = line
			creation = strings.TrimSuffix(strings.TrimPrefix(line, prefix), " VERSION '1.2';\n")
		}
	}
	created := strings.Index(detached, "CREATE SCHEMA "+creation+" AUTHORIZATION \"postgres\";\n")
	headerDrop := strings.Index(detached, "DROP EXTENSION \"xml2\";\n")
	retire := "DROP SCHEMA " + creation + ";\n"
	if !strings.HasPrefix(creation, "\"__stuntgres_install_") || create == "" || created < 0 || created >= strings.Index(detached, create) || headerDrop <= strings.Index(detached, create) || strings.Index(detached, retire) <= headerDrop || strings.Count(detached, retire) != 1 || strings.Contains(detached, "ALTER EXTENSION \"xml2\" ADD ") {
		t.Fatal("detached replay lost authentic scratch/header retirement", detached)
	}
	for _, call := range xml2SetCalls() {
		name, signature, _ := strings.Cut(call.signature, "(")
		// The native recipe renders canonical type lists with comma-space.
		signature = strings.ReplaceAll(strings.ReplaceAll(signature, "text", "pg_catalog.text"), ",", ", ")
		function := creation + ".\"" + name + "\"(" + signature
		detach := strings.Index(detached, "ALTER EXTENSION \"xml2\" DROP FUNCTION "+function+";\n")
		if detach <= strings.Index(detached, create) || detach >= headerDrop {
			t.Fatal("detached survivor lost ordered genuine declaration detach", call.name, detached)
		}
		current := ""
		switch call.name {
		case "nodes4":
			current = "xml2_sets_nodes_inner"
		case "list3":
			current = "xml2_sets_list_inner"
		}
		if current != "" {
			rename := strings.Index(detached, "ALTER FUNCTION "+function+" RENAME TO \""+current+"\";\n")
			if rename <= detach || rename >= headerDrop {
				t.Fatal("detached kernel lost current-name placement", call.name, detached)
			}
		}
	}
	for _, target := range []struct{ control *sql.DB; conn *sql.Conn; backend fixture.Backend; notices *[]fixture.Diagnostic }{{p.Native, c, b, notices}, {control, dst, destination, &destinationNotices}} {
		if err := stuntgres.Reset(p.Ctx, target.control); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, target.control, strings.NewReader(detached)); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, target.conn, target.backend.Recorder, fixture.Exec("SET search_path = "+qualified+",public", 0))
		xml2Both(t, p, target.backend, target.conn, fixture.Select("SELECT count(*) AS count FROM pg_extension WHERE extname='xml2'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		xml2SetsConfigured(t, p, target.backend, target.conn, target.notices)
		xml2SetsSaved(t, p, target.backend, target.conn)
		xml2SetsGuard(t, p, target.backend, target.conn)
	}
	fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2SetsViewStep())
}

func xml2SetsDetach(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, qualified string) {
	t.Helper()
	// A missing header must not discard genuine wrapper-to-kernel edges.
	// Detach every selected set declaration, not just the kernels; leave the
	// scalar members attached and unused so dropping the header is ordinary.
	for _, q := range []string{
		"ALTER FUNCTION " + qualified + ".xpath_nodeset(text,text,text,text) RENAME TO xml2_sets_nodes_inner",
		"ALTER FUNCTION " + qualified + ".xpath_list(text,text,text) RENAME TO xml2_sets_list_inner",
		"ALTER EXTENSION xml2 DROP FUNCTION " + qualified + ".xml2_sets_nodes_inner(text,text,text,text)",
		"ALTER EXTENSION xml2 DROP FUNCTION " + qualified + ".xml2_sets_list_inner(text,text,text)",
		"ALTER EXTENSION xml2 DROP FUNCTION " + qualified + ".xpath_nodeset(text,text)",
		"ALTER EXTENSION xml2 DROP FUNCTION " + qualified + ".xpath_nodeset(text,text,text)",
		"ALTER EXTENSION xml2 DROP FUNCTION " + qualified + ".xpath_list(text,text)",
		"DROP EXTENSION xml2",
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	xml2Both(t, p, b, c, fixture.Select("SELECT count(*) AS count FROM pg_extension WHERE extname='xml2'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
}
