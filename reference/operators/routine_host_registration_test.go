package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func hostOpenNative(t *testing.T, definition *stuntgres.HostLanguageDefinition) (*sql.DB, fixture.Backend) {
	t.Helper()
	d := stuntgres.NewDriver()
	if definition != nil {
		if err := d.RegisterHostLanguage(*definition); err != nil {
			t.Fatal(err)
		}
	}
	connector, err := d.OpenConnector("host-native-control")
	if err != nil {
		t.Fatal(err)
	}
	return hostNativePools(t, connector)
}

// Native controls need the raw pool; SQL assertions need recording on the same
// real connector and the recorder belonging to that pool, not the source fixture.
func hostNativePools(t *testing.T, connector driver.Connector) (*sql.DB, fixture.Backend) {
	t.Helper()
	db := sql.OpenDB(connector)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	return db, fixture.Backend{DB: recorded, Recorder: recorder}
}
func TestReferenceRoutineHostRegistration(t *testing.T) {
	hostScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hostFunction(t, p, b, c, "host_registration_inc", "increment-v1", hostIncrementPython, "", true)
		hostBag(t, p, b, c, "host_registration_inc('a=>1,b=>NULL'::hstore)", [][]any{{"a", "2"}, {"b", nil}, {"c", "3"}})
		if b.PG {
			return
		} // The Go registration ABI is explicitly native, not Python.
		runtime := new(hostNativeRuntime)
		definition := runtime.definition()
		driver := stuntgres.NewDriver()
		invalid := []struct {
			name   string
			change func(*stuntgres.HostLanguageDefinition)
		}{
			{"handler", func(d *stuntgres.HostLanguageDefinition) { d.Handler = nil }},
			{"program", func(d *stuntgres.HostLanguageDefinition) { d.Programs[0].Execute = nil }},
			{"logical result", func(d *stuntgres.HostLanguageDefinition) { d.Programs[0].Result = stuntgres.HostType("unsupported") }},
			{"provider", func(d *stuntgres.HostLanguageDefinition) { d.Provider = "" }},
			{"ABI", func(d *stuntgres.HostLanguageDefinition) { d.ABIVersion = "" }},
			{"version", func(d *stuntgres.HostLanguageDefinition) { d.Programs[0].Version = "" }},
			{"logical input", func(d *stuntgres.HostLanguageDefinition) { d.Programs[0].Inputs[0] = stuntgres.HostType("unsupported") }},
			{"duplicate program", func(d *stuntgres.HostLanguageDefinition) { d.Programs = append(d.Programs, d.Programs[0]) }},
			{"duplicate conversion", func(d *stuntgres.HostLanguageDefinition) { d.Conversions = append(d.Conversions, d.Conversions[0]) }},
			{"empty directions", func(d *stuntgres.HostLanguageDefinition) {
				d.Conversions[0].FromSQL = nil
				d.Conversions[0].ToSQL = nil
			}},
			{"unimplemented named direction", func(d *stuntgres.HostLanguageDefinition) { d.Conversions[0].FromSQL = nil }},
			{"unsupported conversion type", func(d *stuntgres.HostLanguageDefinition) { d.Conversions[0].Type = stuntgres.HostType("unsupported") }},
			{"duplicate procedure name", func(d *stuntgres.HostLanguageDefinition) { d.Conversions[0].FromName = d.HandlerName }},
			{"missing default", func(d *stuntgres.HostLanguageDefinition) { d.Defaults[0].FromID = "absent" }},
		}
		if err := driver.RegisterHostLanguage(stuntgres.HostLanguageDefinition{}); err == nil {
			t.Fatal("empty registration accepted")
		}
		for _, tc := range invalid {
			t.Run(tc.name, func(t *testing.T) {
				bad := runtime.definition()
				tc.change(&bad)
				if err := driver.RegisterHostLanguage(bad); err == nil {
					t.Fatal("invalid registration accepted")
				}
			})
		}
		if err := driver.RegisterHostLanguage(definition); err != nil {
			t.Fatal(err)
		}
		if err := driver.RegisterHostLanguage(definition); err == nil {
			t.Fatal("duplicate registration accepted")
		}
		definition.Programs[0].Inputs[0] = stuntgres.HostText
		definition.Conversions[0].ID = "caller mutation"
		definition.Defaults[0].FromID = "caller mutation"
		definition.Programs[0].Execute = hostDouble
		definition.Conversions[0].FromSQL = nil
		definition.Conversions[0].ToSQL = nil
		definition.Conversions[0].FromName = stuntgres.HostName{Schema: "public", Name: "caller_mutation"}
		connector, err := driver.OpenConnector("copy-proof")
		if err != nil {
			t.Fatal(err)
		}
		_, b = hostNativePools(t, connector)
		if err := driver.RegisterHostLanguage(runtime.definition()); err == nil {
			t.Fatal("late registration accepted")
		}
		native, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer native.Close()
		hostRows(t, p, b, native, "SELECT count(*) AS v FROM pg_type WHERE typname='hstore'", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
		hostRows(t, p, b, native, "SELECT count(*) AS v FROM pg_transform", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
		hostExec(t, p, b, native, "CREATE EXTENSION hstore")
		hostFunction(t, p, b, native, "host_registered_copy", "increment-v1", hostIncrementPython, "", true)
		hostBag(t, p, b, native, "host_registered_copy('a=>7'::hstore)", [][]any{{"a", "8"}, {"c", "3"}})
		hostFunction(t, p, b, native, "host_registered_default", "text-default-v1", "", "", false)
		hostBag(t, p, b, native, "host_registered_default('a=>7,b=>NULL'::hstore)", [][]any{{"a", "7"}, {"b", nil}})
		const query = "SELECT key,value FROM each(host_registered_copy($1::hstore)) ORDER BY key"
		statement, err := native.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		held := fixture.HeldStatement{Executor: native, Statement: statement, SQL: query}
		for _, q := range []string{"CREATE FUNCTION host_bad_program(v hstore) RETURNS hstore LANGUAGE go_host AS 'missing'", "CREATE OR REPLACE FUNCTION host_registered_copy(v hstore) RETURNS hstore LANGUAGE go_host AS 'missing'"} {
			hostNativeError(t, p.Ctx, native, q, `host program "missing" is not registered`)
		}
		for _, tc := range []struct{ signature, message string }{
			{"(v text) RETURNS hstore", "incompatible host program input/signature/direction"},
			{"(v hstore) RETURNS text", "incompatible host program result/signature/direction"},
			{"(v hstore,w hstore) RETURNS hstore", "incompatible host program result/signature/direction"},
		} {
			hostNativeError(t, p.Ctx, native, "CREATE FUNCTION host_bad_signature"+tc.signature+" LANGUAGE go_host AS 'increment-v1'", tc.message)
		}
		// Capability validation cannot be disabled by the SQL body-check setting.
		hostExec(t, p, b, native, "SET check_function_bodies=off")
		hostNativeError(t, p.Ctx, native, "CREATE FUNCTION host_bad_unchecked(v hstore) RETURNS hstore LANGUAGE go_host AS 'not Go source'", `host program "not Go source" is not registered`)
		hostExec(t, p, b, native, "RESET check_function_bodies")
		for _, tc := range []struct{ q, code, message string }{
			{"CREATE FUNCTION host_bad_symbol(v hstore) RETURNS hstore LANGUAGE go_host AS 'increment-v1','symbol'", "42P13", `only one AS item needed for language "go_host"`},
			{"CREATE FUNCTION host_bad_inline(v hstore) RETURNS hstore LANGUAGE go_host RETURN v", "42P13", "inline SQL function body only valid for language SQL"},
			{"CREATE FUNCTION host_bad_set(v hstore) RETURNS SETOF hstore LANGUAGE go_host AS 'increment-v1'", "0A000", "host routines require a scalar result or one scalar procedure output"},
			{"CREATE FUNCTION host_bad_out(INOUT v hstore) LANGUAGE go_host AS 'increment-v1'", "0A000", "host routines require a scalar result or one scalar procedure output"},
		} {
			hostError(t, p, native, tc.q, fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
		}
		hostRows(t, p, b, native, "SELECT count(*) AS v FROM pg_proc WHERE proname IN ('host_bad_program','host_bad_signature','host_bad_unchecked','host_bad_symbol','host_bad_inline','host_bad_set','host_bad_out')", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
		hostRows(t, p, b, held, query, []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "8"}, {"c", "3"}}, "a=>7")
		_, isolated := hostOpenNative(t, nil)
		other, err := isolated.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		hostExec(t, p, isolated, other, "CREATE EXTENSION hstore")
		hostError(t, p, other, "CREATE FUNCTION host_cross_driver(v hstore) RETURNS hstore LANGUAGE go_host AS 'increment-v1'", fixture.ControlsReferenceDiagnostic("42704", `language "go_host" does not exist`))
		hostRows(t, p, isolated, other, "SELECT count(*) AS v FROM pg_proc WHERE proname='host_cross_driver'", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}
