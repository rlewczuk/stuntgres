package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type createAggregateRetainedObject struct {
	name             string
	schema           bool
	namespace, owner int64
}

func createAggregateFreshPool(t *testing.T) *sql.DB {
	t.Helper()
	connector, err := stuntgres.NewDriver().OpenConnector("create-aggregate-remap/" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { db.Close() })
	return db
}

func createAggregateRoutineIDs(t *testing.T, p *fixture.Pair, c *sql.Conn) map[string]int64 {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT proname,oid::bigint FROM pg_proc WHERE proname='ca_add' OR proname='ca_sum'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := map[string]int64{}
	for rows.Next() {
		var name string
		var id int64
		if err = rows.Scan(&name, &id); err != nil {
			t.Fatal(err)
		}
		if id <= 0 {
			t.Fatal("invalid routine identity", id)
		}
		ids[name] = id
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatal("missing real declarations", ids)
	}
	return ids
}

func createAggregateForcedRemap(t *testing.T, p *fixture.Pair, script string) {
	t.Helper()
	// First observe an ordinary replay's actual candidate identities. TEMP
	// allocations below deliberately consume those numbers, not guessed OIDs.
	baseline := createAggregateFreshPool(t)
	if err := stuntgres.Restore(p.Ctx, baseline, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	source, err := baseline.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	candidate := createAggregateRoutineIDs(t, p, source)
	destination := createAggregateFreshPool(t)
	c, err := destination.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	maxID := max(candidate["ca_add"], candidate["ca_sum"])
	collisions := map[int64]createAggregateRetainedObject{}
	for i := range 128 {
		name := fmt.Sprintf("ca_collision_%d", i)
		result, err := c.ExecContext(p.Ctx, "CREATE TEMP SEQUENCE "+name)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 0 {
			t.Fatal(n, err)
		}
		var id, namespace, owner int64
		if err = c.QueryRowContext(p.Ctx, "SELECT oid::bigint,relnamespace::bigint,relowner::bigint FROM pg_class WHERE relname=$1 AND relkind='S' AND relpersistence='t'", name).Scan(&id, &namespace, &owner); err != nil || id <= 0 || namespace <= 0 || owner <= 0 {
			t.Fatal("missing actual TEMP sequence identity", id, namespace, owner, err)
		}
		collisions[id] = createAggregateRetainedObject{name: name, namespace: namespace, owner: owner}
		if id >= maxID {
			break
		}
	}
	// The first TEMP object also creates real table/TOAST TEMP namespaces.
	// They occupy the same native OID allocator as procedures; discover their
	// actual IDs and session ownership rather than assuming a sequence offset.
	namespaces, err := c.QueryContext(p.Ctx, "SELECT oid::bigint,nspname,nspowner::bigint FROM pg_namespace WHERE (nspname LIKE 'pg_temp_%' OR nspname LIKE 'pg_toast_temp_%') AND NOT pg_is_other_temp_schema(oid)")
	if err != nil {
		t.Fatal(err)
	}
	for namespaces.Next() {
		var id, owner int64
		var name string
		if err = namespaces.Scan(&id, &name, &owner); err != nil || id <= 0 || owner <= 0 {
			namespaces.Close()
			t.Fatal("missing actual TEMP namespace identity", id, name, owner, err)
		}
		collisions[id] = createAggregateRetainedObject{name: name, schema: true, owner: owner}
	}
	if err = namespaces.Err(); err != nil {
		namespaces.Close()
		t.Fatal(err)
	}
	namespaces.Close()
	for name, id := range candidate {
		if collisions[id].name == "" {
			t.Fatalf("did not force %s candidate collision%d: %v", name, id, collisions)
		}
	}
	if _, err = c.ExecContext(p.Ctx, "CREATE TEMP VIEW ca_collision_consumer AS SELECT 73 AS v"); err != nil {
		t.Fatal(err)
	}
	held, err := c.PrepareContext(p.Ctx, "SELECT v FROM ca_collision_consumer")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for range 2 {
		if err = stuntgres.Restore(p.Ctx, destination, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		remapped := createAggregateRoutineIDs(t, p, c)
		for name, id := range candidate {
			if remapped[name] == id {
				t.Fatal("import overwrote retained TEMP identity", name, id)
			}
		}
		for id, retained := range collisions {
			var name string
			var owner int64
			if retained.schema {
				var other bool
				if err = c.QueryRowContext(p.Ctx, "SELECT nspname,nspowner::bigint,pg_is_other_temp_schema(oid) FROM pg_namespace WHERE oid=$1::oid", id).Scan(&name, &owner, &other); err != nil || name != retained.name || owner != retained.owner || other {
					t.Fatal("retained TEMP namespace identity/ownership changed", id, name, owner, other, retained, err)
				}
			} else {
				var namespace int64
				var kind, persistence string
				if err = c.QueryRowContext(p.Ctx, "SELECT relname,relnamespace::bigint,relowner::bigint,relkind::text,relpersistence::text FROM pg_class WHERE oid=$1::oid", id).Scan(&name, &namespace, &owner, &kind, &persistence); err != nil || name != retained.name || namespace != retained.namespace || owner != retained.owner || kind != "S" || persistence != "t" {
					t.Fatal("retained TEMP sequence identity/ownership changed", id, name, namespace, owner, retained, err)
				}
			}
		}
		var total, retained int64
		if err = c.QueryRowContext(p.Ctx, "SELECT ca_sum(x) FROM ca_input").Scan(&total); err != nil || total != 6 {
			t.Fatal("remapped execution", total, err)
		}
		if err = held.QueryRowContext(p.Ctx).Scan(&retained); err != nil || retained != 73 {
			t.Fatal("retained consumer", retained, err)
		}
		var support, state bool
		if err = c.QueryRowContext(p.Ctx, "SELECT a.aggtransfn=f.oid,a.aggtranstype=p.prorettype FROM pg_aggregate a JOIN pg_proc p ON p.oid=a.aggfnoid JOIN pg_proc f ON f.oid=a.aggtransfn WHERE p.proname='ca_sum' AND f.proname='ca_add'").Scan(&support, &state); err != nil || !support || !state {
			t.Fatal("remapped support/type", support, state, err)
		}
	}
}
