package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDeparsePartitionControls(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE TABLE dp_control(k INT,v TEXT) PARTITION BY RANGE((k+1))",
			"CREATE TABLE dp_control_mid PARTITION OF dp_control FOR VALUES FROM(0) TO(10) PARTITION BY LIST(v)",
			"CREATE TABLE dp_control_null PARTITION OF dp_control_mid FOR VALUES IN(NULL)",
			"CREATE TABLE dp_control_default PARTITION OF dp_control_mid DEFAULT",
			"CREATE TABLE dp_control_hash(k INT) PARTITION BY HASH(k)",
			"CREATE TABLE dp_control_hash_leaf PARTITION OF dp_control_hash FOR VALUES WITH(MODULUS 1,REMAINDER 0)",
			`ALTER TABLE dp_control RENAME k TO "K renamed"`,
			"CREATE VIEW dp_control_view AS SELECT pg_get_partkeydef('dp_control'::regclass) AS keydef,pg_get_partition_constraintdef('dp_control_null'::regclass) AS constraintdef",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO dp_control VALUES(1,NULL),(2,'default')", 2))
		run(fixture.Exec("INSERT INTO dp_control_hash VALUES(NULL),(7)", 2))
		key := `RANGE ((("K renamed" + 1)))`
		bound := `((("K renamed" + 1) IS NOT NULL) AND (("K renamed" + 1) >= 0) AND (("K renamed" + 1) < 10) AND (v IS NULL))`
		check := func() {
			run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partkeydef('dp_control'::regclass) AS definition", key))
			run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('dp_control_null'::regclass) AS definition", bound))
			run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('dp_control_default'::regclass) AS definition", `((("K renamed" + 1) IS NOT NULL) AND (("K renamed" + 1) >= 0) AND (("K renamed" + 1) < 10) AND (NOT (v IS NULL)))`))
			id := fixture.DeparseDefinitionID(t, c, "SELECT 'dp_control_hash'::regclass::oid")
			run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('dp_control_hash_leaf'::regclass) AS definition", fmt.Sprintf("satisfies_hash_partition('%d'::oid, 1, 0, k)", id)))
			run(fixture.Select(`SELECT "K renamed",v FROM dp_control ORDER BY "K renamed"`, []string{"K renamed", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), "default"}}))
			run(fixture.Select("SELECT k FROM dp_control_hash ORDER BY k NULLS FIRST", []string{"k"}, []string{"INT4"}, [][]any{{nil}, {int64(7)}}))
			run(fixture.Prepared(fixture.Select("SELECT keydef,constraintdef FROM dp_control_view", []string{"keydef", "constraintdef"}, []string{"TEXT", "TEXT"}, [][]any{{key, bound}})))
		}
		check()
		if b.PG {
			return
		} // Public native-control APIs have no PostgreSQL counterpart.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		// Keep the actual eager native cursor over replacement, without fetching it.
		old, err := c.QueryContext(p.Ctx, "SELECT pg_get_partkeydef('dp_control'::regclass) AS definition")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		fixture.DeparseRelationsMetadata(t, false, old)
		run(fixture.Exec(`ALTER TABLE dp_control RENAME "K renamed" TO "K replacement"`, 0))
		replacementKey := `RANGE ((("K replacement" + 1)))`
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partkeydef('dp_control'::regclass) AS definition", replacementKey))
		replaced, err := c.QueryContext(p.Ctx, "SELECT pg_get_partkeydef('dp_control'::regclass) AS definition")
		if err != nil {
			t.Fatal(err)
		}
		defer replaced.Close()
		fixture.DeparseRelationsMetadata(t, false, replaced)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		// Populate the destination allocator with live unrelated identities before replacement.
		for _, q := range []string{"CREATE TABLE dp_collision_a(a INT)", "CREATE TABLE dp_collision_b(b TEXT)", "CREATE TABLE dp_collision_c(c INT) PARTITION BY HASH(c)"} {
			fixture.RunStep(t, p.Ctx, p.Native, p.MemoryRecord, fixture.Exec(q, 0))
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check()
		if !old.Next() {
			t.Fatal("retained cursor lost row", old.Err())
		}
		var got string
		if err := old.Scan(&got); err != nil || got != key {
			t.Fatal("retained definition", got, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained cursor tail", old.Err())
		}
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
		if !replaced.Next() {
			t.Fatal("replacement cursor lost row", replaced.Err())
		}
		if err := replaced.Scan(&got); err != nil || got != replacementKey {
			t.Fatal("replacement cursor was rebound to restored definitions", got, err)
		}
		if replaced.Next() || replaced.Err() != nil {
			t.Fatal("replacement cursor tail", replaced.Err())
		}
		if err := replaced.Close(); err != nil {
			t.Fatal(err)
		}
		retained, err := c.QueryContext(p.Ctx, "SELECT pg_get_partition_constraintdef('dp_control_null'::regclass) AS definition")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		fixture.DeparseRelationsMetadata(t, false, retained)
		for _, script := range []string{"CREATE TABLE broken(", "CREATE TABLE broken(k INT CHECK(k>0));INSERT INTO broken VALUES(0)"} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
				t.Fatal("invalid Restore succeeded")
			}
			check()
		}
		if !retained.Next() {
			t.Fatal("failed restore lost retained cursor", retained.Err())
		}
		if err := retained.Scan(&got); err != nil || got != bound {
			t.Fatal(got, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained cursor tail", retained.Err())
		}
	})
}
