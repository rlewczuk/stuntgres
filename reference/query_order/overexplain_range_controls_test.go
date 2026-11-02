package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceOverexplainRangeControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"range_control_reader"})
	overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		overexplainRangeProviderSetup(t, p, b, c)
		for _, q := range []string{"CREATE ROLE range_control_reader", "GRANT USAGE ON SCHEMA public TO range_control_reader", "GRANT SELECT ON overexplain_translated TO range_control_reader"} {
			overexplainExec(t, p, b, c, q)
		}
		before := overexplainProvider(t, p, c)
		overexplainProviderValues(t, p, b, c)
		overexplainRangeUnloaded(t, p, b, c)
		overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
		q := overexplainSQL("JSON", "RANGE_TABLE, VERBOSE, COSTS FALSE", "SELECT id,v FROM overexplain_translated ORDER BY id")
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		executor := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		retained := overexplainQuery(t, p, executor, q, "JSON", false)
		snapshot := fmt.Sprint(retained)
		overexplainRangeProviderPlan(t, p, b, c, retained)
		if after := overexplainProvider(t, p, c); !reflect.DeepEqual(before, after) {
			t.Fatal("LOAD/range inspection changed installed identities", before, after)
		}
		overexplainRangeProviderACL(t, p, b, c)
		if b.PG {
			return
		} // Native controls have no PostgreSQL SQL equivalent.
		overexplainExec(t, p, b, c, "PREPARE range_control_handle AS SELECT id FROM overexplain_provider_data")
		overexplainExec(t, p, b, c, "BEGIN")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO overexplain_provider_data VALUES(99,'range_transaction_private')", 1))
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		overexplainExec(t, p, b, c, "ROLLBACK")
		for _, excluded := range []string{"pg_overexplain", "range_control_handle", "range_transaction_private", "Embedded Range Table"} {
			if strings.Contains(dump.String(), excluded) {
				t.Fatal("dump transported physical/borrowed/private state", excluded)
			}
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		overexplainProvider(t, p, c)
		overexplainProviderValues(t, p, b, c)
		overexplainRangeProviderACL(t, p, b, c)
		overexplainRangeProviderPlan(t, p, b, c, overexplainQuery(t, p, executor, q, "JSON", false))
		if fmt.Sprint(retained) != snapshot {
			t.Fatal("Restore mutated retained document")
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		overexplainRange(t, b, overexplainQuery(t, p, c, overexplainSQL("JSON", "RANGE_TABLE", "SELECT 1"), "JSON", true), "JSON", true)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		overexplainProvider(t, p, c)
		overexplainProviderValues(t, p, b, c)
		overexplainRangeProviderACL(t, p, b, c)
		overexplainRangeProviderPlan(t, p, b, c, overexplainQuery(t, p, c, q, "JSON", true))
		overexplainRangeDestination(t, p, b, dump.Bytes(), true)
		if fmt.Sprint(retained) != snapshot {
			t.Fatal("old document changed after Reset/Restore/destination teardown")
		}
	})
}

func overexplainRangeProviderSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{"CREATE EXTENSION unaccent", "CREATE TABLE overexplain_provider_data(id integer,v text)", "CREATE VIEW overexplain_translated AS SELECT id,unaccent(v) AS v FROM overexplain_provider_data"} {
		overexplainExec(t, p, b, c, q)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO overexplain_provider_data VALUES(1,'Hôtel'),(2,NULL)", 2))
}

func overexplainRangeProviderPlan(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r overexplainResult) {
	t.Helper()
	entries := overexplainRange(t, b, r, "JSON", true)
	found := false
	for _, e := range entries {
		if b.PG {
			if e["Relation"] == "public.overexplain_provider_data" {
				if overexplainInteger(t, e["Permission Info Index"], false) <= 0 {
					t.Fatal("PG base lost permission reference", e)
				}
				found = true
			}
		} else if e["Kind"] == "relation" && e["Relation Name"] == "overexplain_provider_data" {
			overexplainRangeRelation(t, p, c, e, "public.overexplain_provider_data", "JSON")
			found = true
		}
	}
	if !found {
		t.Fatal("missing destination-bound provider view base identity", entries)
	}
}

func overexplainRangeProviderACL(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	overexplainExec(t, p, b, c, "SET ROLE range_control_reader")
	s := fixture.Select("SELECT id,v FROM overexplain_translated ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "Hotel"}, {int64(2), nil}})
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	overexplainRangeProviderPlan(t, p, b, c, overexplainQuery(t, p, c, overexplainSQL("JSON", "RANGE_TABLE, VERBOSE", "SELECT id,v FROM overexplain_translated"), "JSON", true))
	q := overexplainSQL("JSON", "RANGE_TABLE", "SELECT v FROM overexplain_provider_data")
	overexplainError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table overexplain_provider_data"), "execute", true)
	overexplainExec(t, p, b, c, "RESET ROLE")
}

func overexplainRangeDestination(t *testing.T, p *fixture.Pair, b fixture.Backend, dump []byte, withACL bool) {
	t.Helper()
	connector, err := stuntgres.NewDriver().OpenConnector("range-independent-destination")
	if err != nil {
		t.Fatal(err)
	}
	recorded, raw, recorder := fixture.DatabaseConnectorScenarioPool(t, connector, false)
	destination, err := recorded.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	b.DB, b.Recorder = recorded, recorder
	if withACL {
		overexplainExec(t, p, b, destination, "CREATE ROLE range_control_reader")
	}
	overexplainRangeUnloaded(t, p, b, destination)
	if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	overexplainProvider(t, p, destination)
	overexplainProviderValues(t, p, b, destination)
	overexplainRangeUnloaded(t, p, b, destination)
	fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Select("SELECT count(*) FROM pg_prepared_statements WHERE name='range_control_handle'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	overexplainExec(t, p, b, destination, "LOAD 'pg_overexplain'")
	q := overexplainSQL("JSON", "RANGE_TABLE, VERBOSE", "SELECT id,v FROM overexplain_translated")
	overexplainRangeProviderPlan(t, p, b, destination, overexplainQuery(t, p, destination, q, "JSON", true))
	if withACL {
		overexplainRangeProviderACL(t, p, b, destination)
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
	if err := recorded.Close(); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
}
