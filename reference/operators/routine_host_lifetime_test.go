package reference_test

import (
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoutineHostLifetime(t *testing.T) {
	hostNativeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, native *hostNativeRuntime) {
		hostFunction(t, p, b, c, "host_lifetime", "retain-v1", hostIncrementPython, "", true)
		hostFunction(t, p, b, c, "host_lifetime_double", "double-v1", hostDoublePython, "", true)
		hostExec(t, p, b, c, "CREATE TABLE host_lifetime_input(v hstore)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO host_lifetime_input VALUES ('a=>1,b=>NULL')", 1))
		rows, err := c.QueryContext(p.Ctx, "SELECT host_lifetime(v) AS v FROM host_lifetime_input")
		if err != nil {
			t.Fatal(err)
		}
		metadata, err := rows.ColumnTypes()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if !rows.Next() {
			rows.Close()
			t.Fatal("missing lifetime row")
		}
		var first any
		if err = rows.Scan(&first); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
		var saved string
		switch value := first.(type) {
		case string:
			saved = value
		case []byte:
			saved = string(value)
		default:
			t.Fatalf("hstore transport=%T", first)
		}
		if !b.PG {
			native.mu.Lock()
			retained := native.retained
			if retained == nil {
				native.mu.Unlock()
				t.Fatal("program did not receive an actual retained host map")
			}
			retained["a"] = stuntgres.HostHstoreValue{Text: "999"}
			delete(retained, "b")
			retained["leak"] = stuntgres.HostHstoreValue{Text: "bad"}
			native.mu.Unlock()
		}
		hostBag(t, p, b, c, "$1::hstore", [][]any{{"a", "2"}, {"b", nil}, {"c", "3"}}, saved)
		hostBag(t, p, b, c, "(SELECT v FROM host_lifetime_input)", [][]any{{"a", "1"}, {"b", nil}})
		q := "SELECT key,value FROM each(host_lifetime($1::hstore)) ORDER BY key"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, input := range []struct{ text, a string }{{"a=>7,b=>NULL", "8"}, {"a=>1,b=>NULL", "2"}, {"a=>7,b=>NULL", "8"}} {
			hostRows(t, p, b, held, q, []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", input.a}, {"b", nil}, {"c", "3"}}, input.text)
			hostBag(t, p, b, c, "host_lifetime_double('a=>7,b=>NULL'::hstore)", [][]any{{"a", "14"}, {"b", nil}, {"d", "second"}})
		}
		if len(metadata) != 1 || metadata[0].Name() != "v" {
			t.Fatal("retained column metadata changed")
		}
		hostExec(t, p, b, c, "ALTER TYPE hstore RENAME TO host_lifetime_type")
		hostBag(t, p, b, c, "host_lifetime('a=>7,b=>NULL'::host_lifetime_type)", [][]any{{"a", "8"}, {"b", nil}, {"c", "3"}})
		if !b.PG {
			hostExec(t, p, b, c, "DROP EXTENSION hstore CASCADE")
			hostExec(t, p, b, c, "CREATE EXTENSION hstore")
			hostRows(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname IN ('host_lifetime','host_lifetime_double')", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
			hostFunction(t, p, b, c, "host_lifetime_fresh", "increment-v1", hostIncrementPython, "", true)
			hostBag(t, p, b, c, "host_lifetime_fresh('a=>7,b=>NULL'::hstore)", [][]any{{"a", "8"}, {"b", nil}, {"c", "3"}})
		}
	})
}
