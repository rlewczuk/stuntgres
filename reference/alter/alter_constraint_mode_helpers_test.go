package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Source-derived release expectations are deliberately independent of native
// results. The root resolves remaining field/phase questions with pinned 18.6.
func constraintModeScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func constraintModeNN(table, name string, noinherit, local bool, count int64, valid bool) fixture.Step {
	return fixture.Select("SELECT conname,connoinherit,conislocal,coninhcount,convalidated FROM pg_constraint WHERE conrelid=$1::regclass AND conname=$2", []string{"conname", "connoinherit", "conislocal", "coninhcount", "convalidated"}, []string{"NAME", "BOOL", "BOOL", "INT2", "BOOL"}, [][]any{{name, noinherit, local, count, valid}}, table, name)
}

func constraintModeOID(t *testing.T, p *fixture.Pair, c *sql.Conn, table, name string) int64 {
	t.Helper()
	var id int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conrelid=$1::regclass AND conname=$2", table, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id <= 0 {
		t.Fatalf("nonpositive constraint identity %d", id)
	}
	return id
}

func constraintModeSameOID(t *testing.T, p *fixture.Pair, c *sql.Conn, table, name string, want int64) {
	t.Helper()
	if got := constraintModeOID(t, p, c, table, name); got != want {
		t.Fatalf("%s.%s OID = %d, want %d", table, name, got, want)
	}
}

type constraintModeState struct {
	base                              enforcementState
	nn, attributes, inheritance, data [][]any
}

func constraintModeSnapshot(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) constraintModeState {
	t.Helper()
	return constraintModeState{
		base:        enforcementSnapshot(t, p, b, c),
		nn:          enforcementQuery(t, p, c, "SELECT oid,conrelid,conname,connoinherit,conislocal,coninhcount,convalidated FROM pg_constraint WHERE connamespace='public'::regnamespace ORDER BY oid"),
		attributes:  enforcementQuery(t, p, c, "SELECT attrelid,attnum,attname,attnotnull FROM pg_attribute WHERE attrelid IN (SELECT oid FROM pg_class WHERE relnamespace='public'::regnamespace) AND attnum>0 ORDER BY attrelid,attnum"),
		inheritance: enforcementQuery(t, p, c, "SELECT inhrelid,inhparent,inhseqno FROM pg_inherits WHERE inhrelid IN (SELECT oid FROM pg_class WHERE relnamespace='public'::regnamespace) ORDER BY inhrelid,inhseqno"),
		data:        enforcementQuery(t, p, c, "SELECT tableoid::regclass::text,a FROM t ORDER BY 1,2 NULLS LAST"),
	}
}

func constraintModeUnchanged(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, before constraintModeState) {
	t.Helper()
	if after := constraintModeSnapshot(t, p, b, c); !reflect.DeepEqual(before, after) {
		t.Fatalf("failed ALTER published state\nbefore %#v\nafter %#v", before, after)
	}
}

func constraintModeGraph(run func(fixture.Step)) {
	for _, q := range []string{"CREATE TABLE t(a INT CONSTRAINT nn NOT NULL)", "CREATE TABLE child() INHERITS(t)", "CREATE TABLE grandchild() INHERITS(child)", "INSERT INTO t VALUES(1)", "INSERT INTO child VALUES(2)", "INSERT INTO grandchild VALUES(3)"} {
		n := int64(0)
		if len(q) > 6 && q[:6] == "INSERT" {
			n = 1
		}
		run(fixture.Exec(q, n))
	}
}

func constraintModeMissing(table, name string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic("42704", `constraint "`+name+`" of relation "`+table+`" does not exist`)
}

func constraintModeScanNull(table string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("23502", `column "a" of relation "`+table+`" contains null values`)
	d.Schema, d.Table, d.Column = "public", table, "a"
	return d
}
