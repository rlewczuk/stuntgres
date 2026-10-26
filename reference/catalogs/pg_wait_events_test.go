package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const pgWaitEventsSelect = "SELECT type,name,description FROM pg_catalog.pg_wait_events"
const pgWaitEventsPairs = " WHERE (type='IPC' AND name='SafeSnapshot') OR (type='Lock' AND name='advisory') OR (type='Lock' AND name='object') OR (type='Lock' AND name='relation') OR (type='Lock' AND name='transactionid') OR (type='Lock' AND name='virtualxid') OR (type='Timeout' AND name='PgSleep')"

var pgWaitEventsColumns = []string{"type", "name", "description"}
var pgWaitEventsTypes = []string{"TEXT", "TEXT", "TEXT"}

// Independent PostgreSQL 18.6 SQL descriptions: markup, cross-references and
// final periods are absent from the exposed definition text.
func pgWaitEventsExpected() [][]any {
	return [][]any{
		{"IPC", "SafeSnapshot", "Waiting to obtain a valid snapshot for a READ ONLY DEFERRABLE transaction"},
		{"Lock", "advisory", "Waiting to acquire an advisory user lock"},
		{"Lock", "object", "Waiting to acquire a lock on a non-relation database object"},
		{"Lock", "relation", "Waiting to acquire a lock on a relation"},
		{"Lock", "transactionid", "Waiting for a transaction to finish"},
		{"Lock", "virtualxid", "Waiting to acquire a virtual transaction ID lock"},
		{"Timeout", "PgSleep", "Waiting due to a call to pg_sleep or a sibling function"},
	}
}

func pgWaitEventsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func pgWaitEventsMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, pgWaitEventsColumns) {
		t.Fatal("column names", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 3 {
		t.Fatal("column descriptors", types, err)
	}
	for i, typ := range types {
		if typ.Name() != pgWaitEventsColumns[i] || typ.DatabaseTypeName() != "TEXT" {
			t.Fatal("TEXT descriptor", i, typ)
		}
		if nullable, known := typ.Nullable(); (pg && known) || (!pg && (!known || !nullable)) {
			t.Fatal("nullable view descriptor", i, nullable, known)
		}
		if length, known := typ.Length(); length != math.MaxInt64 || !known {
			t.Fatal("TEXT length", i, length, known)
		}
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("TEXT decimal metadata", i, precision, scale, known)
		}
	}
}

func pgWaitEventsStep(b fixture.Backend, query string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, pgWaitEventsColumns, pgWaitEventsTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgWaitEventsMetadata(t, rows, b.PG) }
	return s
}

func pgWaitEventsQuery(b fixture.Backend) string {
	if b.PG {
		return pgWaitEventsSelect + pgWaitEventsPairs + " ORDER BY type,name"
	}
	return pgWaitEventsSelect + " ORDER BY type,name"
}

func pgWaitEventsRead(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, b.Recorder, pgWaitEventsStep(b, pgWaitEventsQuery(b), pgWaitEventsExpected()))
}

func pgWaitEventsInventory(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := pgWaitEventsStep(b, pgWaitEventsSelect+pgWaitEventsPairs+" ORDER BY type,name", pgWaitEventsExpected())
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		// This unfiltered native witness rejects every extra/unsupported family.
		if !b.PG {
			s.Query = pgWaitEventsSelect + " ORDER BY type,name"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		selected := fixture.Select("SELECT name,type FROM pg_wait_events WHERE type='Lock' AND name='relation'", []string{"name", "type"}, []string{"TEXT", "TEXT"}, [][]any{{"relation", "Lock"}})
		selected.Prepared = prepared
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, selected)
	}
}

func pgWaitEventsHeld(t *testing.T, p *fixture.Pair, x fixture.Executor, query string) fixture.HeldStatement {
	t.Helper()
	stmt, err := x.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stmt.Close() })
	return fixture.HeldStatement{Executor: x, Statement: stmt, SQL: query}
}

func pgWaitEventsPrepared(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, pgWaitEventsStep(b, pgWaitEventsSelect+" WHERE type='Lock' AND name='relation'", [][]any{pgWaitEventsExpected()[3]}))
	held := pgWaitEventsHeld(t, p, conn, pgWaitEventsSelect+" WHERE type=$1 AND name=$2")
	for _, tc := range []struct {
		typ, name string
		want      [][]any
	}{
		{"Lock", "relation", [][]any{pgWaitEventsExpected()[3]}},
		{"Timeout", "PgSleep", [][]any{pgWaitEventsExpected()[6]}},
		{"Unknown", "relation", nil},
		{"Lock", "not_an_event", nil},
		{"Lock", "relation", [][]any{pgWaitEventsExpected()[3]}},
	} {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgWaitEventsStep(b, held.SQL, tc.want, tc.typ, tc.name))
	}
	if !b.PG {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgWaitEventsStep(b, held.SQL, nil, "Lock", "tuple"))
	}
}

func pgWaitEventsEmptyMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, query := range []string{"SELECT * FROM pg_catalog.pg_wait_events WHERE FALSE", "SELECT * FROM pg_catalog.pg_wait_events LIMIT 0"} {
		for _, prepared := range []bool{false, true} {
			s := pgWaitEventsStep(b, query, nil)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
	}
}

func TestReferencePGWaitEventsInventory(t *testing.T) {
	pgWaitEventsScenario(t, pgWaitEventsInventory)
}

func TestReferencePGWaitEventsPrepared(t *testing.T) {
	pgWaitEventsScenario(t, pgWaitEventsPrepared)
}

func TestReferencePGWaitEventsMetadata(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pwe_reader"})
	pgWaitEventsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgWaitEventsEmptyMetadata(t, p, b)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Select("SELECT attnum::int4,attname::text,atttypid::bigint,attnotnull FROM pg_catalog.pg_attribute WHERE attrelid='pg_catalog.pg_wait_events'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull"}, []string{"INT4", "TEXT", "INT8", "BOOL"}, [][]any{{int64(1), "type", int64(25), false}, {int64(2), "name", int64(25), false}, {int64(3), "description", int64(25), false}}))
		run(fixture.Select("SELECT c.relkind::text,c.relnatts::int4,t.typname::text,t.typtype::text,t.typrelid=c.oid AS owns_relation,a.typelem=t.oid AS owns_element FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_wait_events'::regclass", []string{"relkind", "relnatts", "typname", "typtype", "owns_relation", "owns_element"}, []string{"TEXT", "INT4", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{"v", int64(3), "pg_wait_events", "c", true, true}}))
		run(fixture.Select("SELECT schemaname::text,viewname::text,definition<>'' AS has_definition FROM pg_catalog.pg_views WHERE schemaname='pg_catalog' AND viewname='pg_wait_events'", []string{"schemaname", "viewname", "has_definition"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{"pg_catalog", "pg_wait_events", true}}))
		run(fixture.Select("SELECT pg_get_viewdef('pg_catalog.pg_wait_events'::regclass)<>'' AS has_definition", []string{"has_definition"}, []string{"BOOL"}, [][]any{{true}}))
		conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		fixture.PGClassExec(t, p, b, conn, "CREATE ROLE pwe_reader", "SET ROLE pwe_reader")
		t.Cleanup(func() { conn.ExecContext(p.Ctx, "RESET ROLE") })
		pgWaitEventsRead(t, p, b, conn)
		held := pgWaitEventsHeld(t, p, conn, pgWaitEventsQuery(b))
		pgWaitEventsRead(t, p, b, held)
		fixture.PGClassExec(t, p, b, conn, "RESET ROLE", "DROP ROLE pwe_reader")
	})
}
