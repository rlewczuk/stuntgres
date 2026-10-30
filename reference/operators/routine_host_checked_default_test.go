package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The entry-list default is deliberately a different real object ABI from the
// explicitly selected map conversion. Its program computes from the supplied
// entries rather than forwarding an unchanged SQL value.
func hostCheckedDefaultDefinition() stuntgres.HostLanguageDefinition {
	d := new(hostNativeRuntime).definition()
	d.Programs = append(d.Programs, stuntgres.HostProgramDefinition{
		ID: "checked-entries-v1", Version: "1", Inputs: []stuntgres.HostType{stuntgres.HostHstore}, Result: stuntgres.HostHstore,
		Execute: func(_ context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
			entries, ok := args[0].Value.([]stuntgres.HostHstoreEntry)
			if !ok || args[0].Null {
				return stuntgres.HostObject{}, fmt.Errorf("checked default requires genuine entry-list object")
			}
			out := append([]stuntgres.HostHstoreEntry(nil), entries...)
			for i := range out {
				if out[i].Key == "a" {
					n, err := strconv.Atoi(out[i].Value)
					if err != nil || out[i].Null {
						return stuntgres.HostObject{}, fmt.Errorf("checked default requires numeric a")
					}
					out[i].Value = strconv.Itoa(n + 1)
				}
			}
			out = append(out, stuntgres.HostHstoreEntry{Key: "default", Value: "entries"})
			return stuntgres.HostObject{Value: out}, nil
		},
	})
	return d
}

func hostCheckedDefaultRegister(d *stuntgres.Driver) error {
	if err := d.RegisterHostLanguage(hostCheckedDefaultDefinition()); err != nil {
		return err
	}
	return d.RegisterHostLanguage(hostPrimitiveDefinition())
}

func TestReferenceRoutineHostCheckedDefaultReplay(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"host_default_reader"})
	fixture.SchemaReferenceCleanup(t, "host_calls", "host_types")
	p := fixture.NewScenarioWithDriverConfig(t, hostCheckedDefaultRegister)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer routineLanguagesCleanup(t, c, "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS host_calls CASCADE", "DROP EXTENSION IF EXISTS hstore_plpython3u CASCADE", "DROP EXTENSION IF EXISTS plpython3u CASCADE", "DROP SCHEMA IF EXISTS host_types CASCADE", "REVOKE USAGE ON SCHEMA public FROM host_default_reader", "DROP ROLE host_default_reader")()
			for _, q := range []string{
				"CREATE ROLE host_default_reader",
				"CREATE SCHEMA host_types",
				"CREATE SCHEMA host_calls AUTHORIZATION host_default_reader",
				"CREATE EXTENSION hstore SCHEMA host_types",
				"SET search_path=host_types,public,pg_catalog",
			} {
				hostExec(t, p, b, c, q)
			}
			if b.PG {
				hostExec(t, p, b, c, "CREATE EXTENSION plpython3u")
				hostExec(t, p, b, c, "CREATE EXTENSION hstore_plpython3u")
			}
			hostRows(t, p, b, c, "SELECT count(*) AS v FROM pg_cast WHERE castsource='host_types.hstore'::regtype AND casttarget='text'::regtype", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
			language, nullBody, entriesBody, entriesTransform := "go_host", "null-v1", "checked-entries-v1", ""
			if b.PG {
				language, nullBody = "plpython3u", "return {'n':'handled'} if v is None else v"
				entriesBody = "assert isinstance(v, dict)\nv['a']=str(int(v['a'])+1)\nv['default']='entries'\nreturn v"
				entriesTransform = " TRANSFORM FOR TYPE host_types.hstore"
			}
			for _, q := range []string{
				"CREATE FUNCTION host_calls.compared(v host_types.hstore DEFAULT NULLIF('a=>1'::host_types.hstore,'a=>1'::host_types.hstore)) RETURNS host_types.hstore LANGUAGE " + language + " TRANSFORM FOR TYPE host_types.hstore AS '" + strings.ReplaceAll(nullBody, "'", "''") + "'",
				"CREATE FUNCTION host_calls.entries(v host_types.hstore DEFAULT 'a=>7,b=>NULL'::host_types.hstore) RETURNS host_types.hstore LANGUAGE " + language + entriesTransform + " AS '" + strings.ReplaceAll(entriesBody, "'", "''") + "'",
				"CREATE VIEW host_calls.explicit AS SELECT host_calls.compared(NULLIF('a=>2'::host_types.hstore,'a=>1'::host_types.hstore)) AS v",
				"GRANT USAGE ON SCHEMA public TO host_default_reader",
				"SET SESSION AUTHORIZATION host_default_reader",
				"CREATE VIEW host_calls.omitted AS SELECT host_calls.compared() AS v",
				"RESET SESSION AUTHORIZATION",
				"CREATE CAST (host_types.hstore AS text) WITH INOUT AS ASSIGNMENT",
			} {
				hostExec(t, p, b, c, q)
			}
			hostPrimitiveFunction(t, p, b, c, "host_calls.old_text", "v text DEFAULT 'a=>7'::host_types.hstore", "text", "text-mark-v1", "return v+'!'", "")
			hostExec(t, p, b, c, "CREATE VIEW host_calls.old_omitted AS SELECT host_calls.old_text() AS v")
			for _, q := range []string{
				"DROP CAST (host_types.hstore AS text)",
				"CREATE FUNCTION host_calls.cast_now(v host_types.hstore) RETURNS text LANGUAGE SQL RETURN 'new:'||host_types.fetchval(v,'a')",
				"CREATE CAST (host_types.hstore AS text) WITH FUNCTION host_calls.cast_now(host_types.hstore) AS IMPLICIT",
			} {
				hostExec(t, p, b, c, q)
			}
			hostPrimitiveFunction(t, p, b, c, "host_calls.new_text", "v text DEFAULT 'a=>7'::host_types.hstore", "text", "text-mark-v1", "return v+'!'", "")
			hostExec(t, p, b, c, "CREATE VIEW host_calls.new_omitted AS SELECT host_calls.new_text() AS v")
			hostExec(t, p, b, c, "SET search_path=host_calls,pg_catalog")
			hostCheckedDefaultResults(t, p, b, c)
			hostCheckedDefaultHeld(t, p, b, c)
			if !b.PG {
				hostCheckedDefaultReplay(t, p)
			}
		})
	}
}

func hostCheckedDefaultResults(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, expression := range []string{"host_calls.compared()", "(SELECT v FROM host_calls.omitted)"} {
		hostRows(t, p, b, c, "SELECT key,value FROM host_types.each("+expression+") ORDER BY key", []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"n", "handled"}})
	}
	hostRows(t, p, b, c, "SELECT key,value FROM host_types.each((SELECT v FROM host_calls.explicit)) ORDER BY key", []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "2"}})
	for _, input := range []struct{ call, a string }{{"host_calls.entries()", "8"}, {"host_calls.entries('a=>11,b=>NULL'::host_types.hstore)", "12"}} {
		hostRows(t, p, b, c, "SELECT key,value FROM host_types.each("+input.call+") ORDER BY key", []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", input.a}, {"b", nil}, {"default", "entries"}})
	}
	for _, tc := range []struct{ expression, want string }{
		{"host_calls.old_text()", `"a"=>"7"!`},
		{"(SELECT v FROM host_calls.old_omitted)", `"a"=>"7"!`},
		{"host_calls.new_text()", "new:7!"},
		{"(SELECT v FROM host_calls.new_omitted)", "new:7!"},
		{"host_calls.old_text('x')", "x!"},
		{"host_calls.new_text('a=>9'::host_types.hstore)", "new:9!"},
	} {
		hostRows(t, p, b, c, "SELECT "+tc.expression+" AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}})
	}
	hostRows(t, p, b, c, "SELECT castfunc='host_calls.cast_now(host_types.hstore)'::regprocedure::oid AS adapter,castcontext::text AS context,castmethod::text AS method FROM pg_cast WHERE castsource='host_types.hstore'::regtype AND casttarget='text'::regtype", []string{"adapter", "context", "method"}, []string{"BOOL", "TEXT", "TEXT"}, [][]any{{true, "i", "f"}})
	hostRows(t, p, b, c, "SELECT pronargdefaults,prorettype='host_types.hstore'::regtype AS result FROM pg_proc WHERE oid='host_calls.compared(host_types.hstore)'::regprocedure::oid", []string{"pronargdefaults", "result"}, []string{"INT2", "BOOL"}, [][]any{{int64(1), true}})
	hostMetadata(t, p, b, c, "SELECT host_calls.compared() AS v")
	hostMetadata(t, p, b, c, "SELECT host_calls.entries() AS v WHERE false")
	hostPrimitiveMetadata(t, p, b, c, "SELECT host_calls.old_text() AS v", "TEXT")
}

func hostCheckedDefaultHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct {
		expression string
		want       [][]any
	}{
		{"host_calls.compared()", [][]any{{"n", "handled"}}},
		{"host_calls.entries()", [][]any{{"a", "8"}, {"b", nil}, {"default", "entries"}}},
	} {
		q := "SELECT key,value FROM host_types.each(" + tc.expression + ") ORDER BY key"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for range 2 {
			hostRows(t, p, b, held, q, []string{"key", "value"}, []string{"TEXT", "TEXT"}, tc.want)
		}
		stmt.Close()
	}
	// The reader cannot see the type namespace. Omitted defaults carry only
	// their own checked authority; no comparison window transfers to the caller.
	hostExec(t, p, b, c, "SET SESSION AUTHORIZATION host_default_reader")
	defer hostExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
	for _, tc := range []struct {
		query        string
		names, types []string
		rows         [][]any
	}{
		{"SELECT host_calls.compared() IS NULL AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}},
		{"SELECT host_calls.old_text() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{`"a"=>"7"!`}}},
		{"SELECT host_calls.new_text() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"new:7!"}}},
	} {
		stmt, err := c.PrepareContext(p.Ctx, tc.query)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.query}
		for range 2 {
			hostRows(t, p, b, c, tc.query, tc.names, tc.types, tc.rows)
			hostRows(t, p, b, held, tc.query, tc.names, tc.types, tc.rows)
		}
		stmt.Close()
	}
	d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema host_types")
	d.Position = "13"
	hostErrorAtStage(t, p, c, "SELECT '1'::host_types.hstore", d, "prepare")
	hostExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
	q := "SELECT host_calls.compared() IS NULL AS v"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	hostExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION host_calls.compared(host_types.hstore) FROM PUBLIC")
	hostExec(t, p, b, c, "SET SESSION AUTHORIZATION host_default_reader")
	var value any
	err = stmt.QueryRowContext(p.Ctx).Scan(&value)
	denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function compared")
	fixture.AssertDiagnostic(t, err, &denied)
	hostExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
	hostExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION host_calls.compared(host_types.hstore) TO PUBLIC")
	hostExec(t, p, b, c, "SET SESSION AUTHORIZATION host_default_reader")
	hostRows(t, p, b, held, q, []string{"v"}, []string{"BOOL"}, [][]any{{false}})
}

func hostCheckedDefaultReplay(t *testing.T, p *fixture.Pair) {
	t.Helper()
	var stream bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &stream); err != nil {
		t.Fatal("checked default Dump", err)
	}
	hostCheckedDefaultWindows(t, stream.String())
	for round := range 2 {
		d := stuntgres.NewDriver()
		if err := hostCheckedDefaultRegister(d); err != nil {
			t.Fatal(err)
		}
		connector, err := d.OpenConnector("checked-default-replay")
		if err != nil {
			t.Fatal(err)
		}
		raw, b := hostNativePools(t, connector)
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		hostExec(t, p, b, c, "CREATE ROLE host_default_reader")
		if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(stream.Bytes())); err != nil {
			t.Fatalf("fresh matching checked-default replay round%d: %v", round, err)
		}
		hostCheckedDefaultResults(t, p, b, c)
		hostCheckedDefaultHeld(t, p, b, c)
		q := "SELECT host_calls.old_text() AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		bad := append(append([]byte(nil), stream.Bytes()...), []byte("\nCREATE FUNCTION host_calls.broken(v text DEFAULT missing_checked_default()) RETURNS text LANGUAGE go_host_primitive AS 'text-mark-v1';\n")...)
		if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(bad)); err == nil {
			t.Fatal("invalid checked default replaced live destination")
		}
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		hostRows(t, p, b, held, q, []string{"v"}, []string{"TEXT"}, [][]any{{`"a"=>"7"!`}})
		stmt.Close()
		hostCheckedDefaultResults(t, p, b, c)
		var next bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, raw, &next); err != nil {
			t.Fatal(err)
		}
		stream.Reset()
		stream.Write(next.Bytes())
	}
}

// Check the existing single-definition replay protocol as well as executing
// restored declarations. An omitted default is not a written caller expression.
func hostCheckedDefaultWindows(t *testing.T, text string) {
	t.Helper()
	comparison, inout := false, false
	seen := make(map[string]bool)
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "-- stuntgres:comparison-binding ") {
			comparison = line != "-- stuntgres:comparison-binding end"
		}
		if strings.HasPrefix(line, "CREATE CAST (") && strings.Contains(line, `"host_types"."hstore" AS `) {
			inout = strings.Contains(line, "WITH INOUT")
		}
		if strings.HasPrefix(line, "DROP CAST (") && strings.Contains(line, `"host_types"."hstore" AS `) {
			inout = false
		}
		for _, tc := range []struct {
			prefix            string
			comparison, inout bool
		}{
			{`CREATE FUNCTION "host_calls"."compared"(`, true, false},
			{`CREATE FUNCTION "host_calls"."old_text"(`, false, true},
			{`CREATE VIEW "host_calls"."omitted"`, false, false},
			{`CREATE VIEW "host_calls"."old_omitted"`, false, false},
			{`CREATE VIEW "host_calls"."new_omitted"`, false, false},
		} {
			if strings.HasPrefix(line, tc.prefix) {
				if seen[tc.prefix] || comparison != tc.comparison || inout != tc.inout {
					t.Errorf("checked-default declaration window %s: comparison=%v inout=%v", tc.prefix, comparison, inout)
				}
				seen[tc.prefix] = true
			}
		}
	}
	if len(seen) != 5 {
		t.Fatalf("missing checked-default replay declarations: %v", seen)
	}
}
