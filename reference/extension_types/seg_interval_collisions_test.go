package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Restore replays into an isolated engine: a permanent spacer is replaced,
// not an allocator perturbation. Retain actual TEMP identities on a pinned
// owner and prove that all selected source IDs collide before importing.
func segIntervalRetainCollisions(t *testing.T, p *fixture.Pair, c *sql.Conn, targets [3]int64) {
	t.Helper()
	remaining := map[int64]bool{}
	var maximum int64
	for _, id := range targets {
		if id <= 0 {
			t.Fatalf("invalid collision target %d", id)
		}
		remaining[id] = true
		maximum = max(maximum, id)
	}
	for i := 0; ; i++ {
		name := "seg_interval_allocator_" + strconv.Itoa(i)
		fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, fixture.Exec("CREATE TEMP TABLE "+name+"(v integer)", 0))
		var relation, rowType, array int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,reltype::bigint FROM pg_class WHERE oid="+queryIntLiteral("pg_temp."+name)+"::regclass::oid").Scan(&relation, &rowType); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT typarray::bigint FROM pg_type WHERE oid=$1::oid", rowType).Scan(&array); err != nil {
			t.Fatal(err)
		}
		for _, id := range []int64{relation, rowType, array} {
			if id <= 0 {
				t.Fatalf("invalid retained TEMP identity %d", id)
			}
			delete(remaining, id)
		}
		if i == 0 {
			// TEMP allocation starts with a retained ordinary/toast namespace
			// pair. These IDs can collide with the first imported seg identity.
			var namespace string
			var namespaceOwner int64
			if err := c.QueryRowContext(p.Ctx, "SELECT nspname,nspowner::bigint FROM pg_namespace WHERE oid=(SELECT relnamespace FROM pg_class WHERE oid="+queryIntLiteral("pg_temp."+name)+"::regclass::oid)").Scan(&namespace, &namespaceOwner); err != nil {
				t.Fatal(err)
			}
			suffix, ok := strings.CutPrefix(namespace, "pg_temp_")
			if !ok || suffix == "" || namespaceOwner <= 0 {
				t.Fatalf("unexpected owned TEMP namespace %q/%d", namespace, namespaceOwner)
			}
			namespaces, err := c.QueryContext(p.Ctx, "SELECT oid::bigint,nspowner::bigint FROM pg_namespace WHERE nspname IN ($1,$2)", namespace, "pg_toast_temp_"+suffix)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for namespaces.Next() {
				var id, owner int64
				if err := namespaces.Scan(&id, &owner); err != nil {
					namespaces.Close()
					t.Fatal(err)
				}
				if id <= 0 || owner != namespaceOwner {
					namespaces.Close()
					t.Fatalf("invalid retained TEMP namespace identity/owner %d/%d", id, owner)
				}
				delete(remaining, id)
				count++
			}
			readErr, closeErr := namespaces.Err(), namespaces.Close()
			if readErr != nil || closeErr != nil || count != 2 {
				t.Fatalf("retained TEMP namespace pair %d: %v/%v", count, readErr, closeErr)
			}
			fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, fixture.Exec("INSERT INTO pg_temp.seg_interval_allocator_0 VALUES (42)", 1))
		}
		if len(remaining) == 0 {
			return
		}
		if max(relation, rowType, array) >= maximum {
			t.Fatalf("retained TEMP allocator passed uncovered source identities %v", remaining)
		}
	}
}
