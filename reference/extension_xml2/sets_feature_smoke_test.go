package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceXML2SetsFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"xml2_sets_smoke_reader"})
	xml2SetsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		xml2Install(t, p, b, c)
		run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
		q := "SELECT xpath_nodeset($1,$2,'o','i') AS nodes,xpath_nodeset($1,$2,'i') AS items,xpath_list($1,$2) AS list"
		step := func(doc, path, nodes, items, list any) fixture.Step {
			return fixture.Select(q, []string{"nodes", "items", "list"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{nodes, items, list}}, doc, path)
		}
		healthy := step(xml2SetsDocument, "/r/a", "<o>"+xml2SetsItems+"</o>", xml2SetsItems, "ABC,D&E")
		xml2Both(t, p, b, c, healthy)
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, s := range []fixture.Step{
			healthy,
			step("<r/>", "/missing", "<o></o>", "", ""),
			step(nil, "[", nil, nil, nil),
			step("<r>", "[", nil, nil, nil),
		} {
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
		}
		var badNodes, badItems, badList any
		err = stmt.QueryRowContext(p.Ctx, "<r/>", "[").Scan(&badNodes, &badItems, &badList)
		d := xml2Compile("Invalid expression") // First projection is the C kernel.
		fixture.AssertDiagnostic(t, err, &d)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, healthy)
		listCall := xml2SetCalls()[3]
		listQ := "SELECT " + listCall.expression + " AS value"
		listStmt, err := c.PrepareContext(p.Ctx, listQ)
		if err != nil {
			t.Fatal(err)
		}
		defer listStmt.Close()
		lh := fixture.HeldStatement{Executor: c, Statement: listStmt, SQL: listQ}
		fixture.RunStep(t, p.Ctx, lh, b.Recorder, xml2SetValue(listQ, "<unsupported/>", xml2SetsDocument, "count(/r/a)"))
		xml2SetsUnsupported(t, notices, listCall, true)
		fixture.RunStep(t, p.Ctx, lh, b.Recorder, xml2SetValue(listQ, "ABC,D&E", listCall.args...))
		xml2SetsNotices(t, notices)
		xml2SetsConsumers(t, p, b, c)
		xml2Both(t, p, b, c, fixture.Select("SELECT id FROM public.xml2_sets_source WHERE xpath_list(doc,path)='ABC,D&E' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		for _, sql := range []string{
			"CREATE ROLE xml2_sets_smoke_reader", "GRANT USAGE ON SCHEMA public TO xml2_sets_smoke_reader",
			"REVOKE EXECUTE ON FUNCTION xpath_list(text,text,text) FROM PUBLIC",
			"SET ROLE xml2_sets_smoke_reader",
		} {
			run(sql)
		}
		var value any
		err = listStmt.QueryRowContext(p.Ctx, listCall.args...).Scan(&value)
		denied := xml2SetsSQLContext(xml2Error("42501", "permission denied for function xpath_list"), "xpath_list")
		fixture.AssertDiagnostic(t, err, &denied)
		run("RESET ROLE")
		run("GRANT EXECUTE ON FUNCTION xpath_list(text,text,text) TO xml2_sets_smoke_reader")
		run("SET ROLE xml2_sets_smoke_reader")
		fixture.RunStep(t, p.Ctx, lh, b.Recorder, xml2SetValue(listQ, "ABC,D&E", listCall.args...))
		run("RESET ROLE")
		run("ALTER FUNCTION xpath_list(text,text,text) RENAME TO xml2_sets_smoke_inner")
		run("ALTER EXTENSION xml2 DROP FUNCTION xml2_sets_smoke_inner(text,text,text)")
		fixture.RunStep(t, p.Ctx, lh, b.Recorder, xml2SetValue(listQ, "ABC,D&E", listCall.args...))
		xml2SetsSaved(t, p, b, c)
		xml2SetsGuard(t, p, b, c)
		xml2SetsConfigure(t, p, b, c)
		xml2SetsConfigured(t, p, b, c, notices)
		if b.PG {
			return
		}
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		xml2SetsConfigured(t, p, b, c, notices)
		fixture.RunStep(t, p.Ctx, lh, b.Recorder, xml2SetValue(listQ, "ABC,D&E", listCall.args...))
		xml2SetsSaved(t, p, b, c)
		xml2SetsGuard(t, p, b, c)
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("xml2_sets_smoke_destination")
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
		fixture.RunStep(t, p.Ctx, dst, recorder, fixture.Exec("CREATE ROLE xml2_sets_smoke_reader", 0))
		if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		xml2SetsConfigured(t, p, destination, dst, &destinationNotices)
		xml2Both(t, p, destination, dst, xml2SetValue(listQ, "ABC,D&E", listCall.args...))
		xml2SetsSaved(t, p, destination, dst)
		xml2SetsGuard(t, p, destination, dst)
	})
}
