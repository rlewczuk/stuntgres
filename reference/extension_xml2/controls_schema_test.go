package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const xml2ControlSchema = "XML2 control 雪"
const xml2ControlQualified = `"XML2 control 雪"`

func xml2SchemaControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, q := range []string{
		"CREATE SCHEMA " + xml2ControlQualified,
		"CREATE EXTENSION xml2 SCHEMA " + xml2ControlQualified,
		"SET search_path TO " + xml2ControlQualified + ",public",
		"CREATE ROLE xml2_control_reader", "CREATE ROLE xml2_control_owner",
		"GRANT USAGE ON SCHEMA " + xml2ControlQualified + " TO xml2_control_reader",
		"REVOKE EXECUTE ON FUNCTION xpath_number(text,text) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION xpath_number(text,text) TO xml2_control_reader",
		"ALTER FUNCTION xpath_number(text,text) OWNER TO xml2_control_owner",
	} {
		run(fixture.Exec(q, 0))
	}
	xml2Consumers(t, p, b, c)
	run(fixture.Exec("GRANT SELECT ON xml2_values TO xml2_control_reader", 0))
	xml2SchemaBaseline(t, p, b, c)
	q := "SELECT id,v,s,n,b FROM xml2_values ORDER BY id"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	want := fixture.Select(q, []string{"id", "v", "s", "n", "b"}, []string{"INT4", "BOOL", "TEXT", "FLOAT4", "BOOL"}, [][]any{{int64(1), true, "12", float64(12), true}})
	fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
	var destination fixture.Backend
	var dst *sql.Conn
	var destinationControl *sql.DB
	if !b.PG {
		old, err := c.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		xml2Retained(t, old)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		xml2SchemaBaseline(t, p, b, c)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		ids := xml2IDs(t, p, c)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"\nINVALID SQL;")); err == nil {
			t.Fatal("late invalid custom-schema replacement succeeded")
		}
		if after := fixture.ExtensionDump(t, p.Ctx, p.Native); after != before {
			t.Fatal("rejected replacement changed custom-schema dump")
		}
		if after := xml2IDs(t, p, c); !reflect.DeepEqual(after, ids) {
			t.Fatal("rejected replacement changed custom-schema identities", ids, after)
		}
		xml2SchemaBaseline(t, p, b, c)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("xml2_schema_destination")
		if err != nil {
			t.Fatal(err)
		}
		destinationControl = sql.OpenDB(connector)
		defer destinationControl.Close()
		pool, recorder := fixture.RecordedNativePool(t, connector)
		dst, err = pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer dst.Close()
		destination = fixture.Backend{Name: "schema_destination", DB: pool, Recorder: recorder}
		for _, role := range []string{"xml2_control_reader", "xml2_control_owner"} {
			fixture.RunStep(t, p.Ctx, dst, recorder, fixture.Exec("CREATE ROLE "+role, 0))
		}
		if err := stuntgres.Restore(p.Ctx, destinationControl, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		xml2SchemaBaseline(t, p, destination, dst)
	}
	for _, q := range []string{
		"ALTER FUNCTION xpath_string(text,text) RENAME TO xml2_control_text",
		"ALTER EXTENSION xml2 DROP FUNCTION xml2_control_text(text,text)",
		"ALTER FUNCTION xml2_control_text(text,text) OWNER TO xml2_control_owner",
		"REVOKE EXECUTE ON FUNCTION xml2_control_text(text,text) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION xml2_control_text(text,text) TO xml2_control_reader",
		"CREATE VIEW xml2_only_text AS SELECT xml2_control_text('<r>12</r>','.') AS s",
		"GRANT SELECT ON xml2_only_text TO xml2_control_reader",
	} {
		run(fixture.Exec(q, 0))
	}
	xml2SchemaAlias(t, p, b, c, true)
	if !b.PG {
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		xml2SchemaAlias(t, p, b, c, true)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		if err := stuntgres.Restore(p.Ctx, destinationControl, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		xml2SchemaAlias(t, p, destination, dst, true)
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	// The detached function, its view and its row-bound CHECK table survive
	// header removal. Drop consumers of the three still-attached declarations.
	for _, q := range []string{"DROP VIEW xml2_values", "DROP TABLE xml2_saved", "DROP EXTENSION xml2"} {
		run(fixture.Exec(q, 0))
	}
	xml2SchemaAlias(t, p, b, c, false)
	if !b.PG {
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		xml2SchemaAlias(t, p, b, c, false)
		if err := stuntgres.Restore(p.Ctx, destinationControl, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		xml2SchemaAlias(t, p, destination, dst, false)
	}
}

func xml2SchemaHeader(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, present bool) {
	t.Helper()
	var rows [][]any
	if present {
		rows = [][]any{{"1.2", false, xml2ControlSchema}}
	}
	xml2Both(t, p, b, c, fixture.Select("SELECT e.extversion,e.extrelocatable,n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='xml2'", []string{"extversion", "extrelocatable", "nspname"}, []string{"TEXT", "BOOL", "NAME"}, rows))
}

func xml2SchemaBaseline(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path TO "+xml2ControlQualified+",public", 0))
	xml2SchemaHeader(t, p, b, c, true)
	xml2Inventory(t, p, b, c)
	xml2Saved(t, p, b, c)
	xml2Guard(t, p, b, c, xml2ControlSchema, "xpath_string")
	xml2Both(t, p, b, c, xml2Scalars("<r>12</r>", ".", "12", float64(12), true))
	xml2Both(t, p, b, c, xml2Scalars("<r/>", "/missing", "", nil, false))
	xml2Both(t, p, b, c, fixture.Select("SELECT r.rolname FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.proname='xpath_number'", []string{"rolname"}, []string{"NAME"}, [][]any{{"xml2_control_owner"}}, xml2ControlSchema))
	run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("SET ROLE xml2_control_reader", 0))
	xml2Both(t, p, b, c, fixture.Select("SELECT n FROM xml2_values", []string{"n"}, []string{"FLOAT4"}, [][]any{{float64(12)}}))
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("REVOKE EXECUTE ON FUNCTION xpath_number(text,text) FROM xml2_control_reader", 0))
	run(fixture.Exec("SET ROLE xml2_control_reader", 0))
	xml2Failure(t, p, b, c, "SELECT n FROM xml2_values", xml2Error("42501", "permission denied for function xpath_number"), false)
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("GRANT EXECUTE ON FUNCTION xpath_number(text,text) TO xml2_control_reader", 0))
}

func xml2SchemaAlias(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, present bool) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path TO "+xml2ControlQualified+",public", 0))
	xml2SchemaHeader(t, p, b, c, present)
	if present {
		xml2Saved(t, p, b, c)
	}
	xml2Guard(t, p, b, c, xml2ControlSchema, "xml2_control_text")
	xml2Both(t, p, b, c, fixture.Select("SELECT p.prosrc,p.probin,r.rolname FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.proname='xml2_control_text'", []string{"prosrc", "probin", "rolname"}, []string{"TEXT", "TEXT", "NAME"}, [][]any{{"xpath_string", "$libdir/pgxml", "xml2_control_owner"}}, xml2ControlSchema))
	xml2Both(t, p, b, c, fixture.Select("SELECT s FROM xml2_only_text", []string{"s"}, []string{"TEXT"}, [][]any{{"12"}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE xml2_control_reader", 0))
	xml2Both(t, p, b, c, fixture.Select("SELECT xml2_control_text('<r>12</r>','.') AS s", []string{"s"}, []string{"TEXT"}, [][]any{{"12"}}))
	xml2Both(t, p, b, c, fixture.Select("SELECT s FROM xml2_only_text", []string{"s"}, []string{"TEXT"}, [][]any{{"12"}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
	if !present {
		xml2Both(t, p, b, c, fixture.Select("SELECT p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 ORDER BY p.proname", []string{"proname"}, []string{"NAME"}, [][]any{{"xml2_control_text"}}, xml2ControlSchema))
	}
	xml2Both(t, p, b, c, fixture.Select("SELECT nspname FROM pg_namespace WHERE nspname LIKE '__stuntgres_%' ORDER BY nspname", []string{"nspname"}, []string{"NAME"}, nil))
}
