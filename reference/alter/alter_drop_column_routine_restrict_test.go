package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterDropColumnRoutineRestrict(t *testing.T) {
	dcrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		dcrSetup(t, p, b, c)
		before := dcrRows(t, p, c, dcrSnapshotSQL)
		identitySQL := "SELECT 'dcr_t'::regclass::oid::bigint,'dcr_body()'::regprocedure::oid::bigint"
		identity := dcrRows(t, p, c, identitySQL)
		dcrAttributeEdges(t, p, b, c, "dcr_body()", "dcr_t", 1)
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, false, "SELECT * FROM dcr_t", []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, []bool{true, true, true}, 1)
		retained, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM dcr_t")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		consume := func() {
			labels, err := retained.Columns()
			if err != nil || !reflect.DeepEqual(labels, []string{"a", "b", "c"}) {
				t.Fatal(labels, err)
			}
			if !retained.Next() {
				t.Fatal("retained original row missing", retained.Err())
			}
			var a, bv, cv int64
			if err := retained.Scan(&a, &bv, &cv); err != nil || a != 7 || bv != 8 || cv != 9 {
				t.Fatal(a, bv, cv, err)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained row cardinality", retained.Err())
			}
			if err := retained.Close(); err != nil {
				t.Fatal(err)
			}
		}
		// pq streams and retains relation AS until the portal closes; native
		// materializes a detached immutable result before the later mutation.
		if b.PG {
			consume()
		}
		selectStmt, err := c.PrepareContext(p.Ctx, "SELECT dcr_body() AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer selectStmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: selectStmt, SQL: "SELECT dcr_body() AS v"}
		for _, prepared := range []bool{false, true} {
			for _, suffix := range []string{"", " RESTRICT"} {
				dcrError(t, p, b, c, "ALTER TABLE dcr_t DROP COLUMN a"+suffix, dcrBodyRestrict(), prepared, 2)
				dcrUnchanged(t, p, b, c, before)
				dcrValue(t, p, b, held, "SELECT dcr_body() AS v", int64(7))
				dcrNotices(t, notices)
			}
		}
		if got := dcrRows(t, p, c, identitySQL); !reflect.DeepEqual(got, identity) {
			t.Fatal("RESTRICT replaced object identities", got, identity)
		}
		dcrRun(t, p, b, c, fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid='dcr_t'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, [][]any{{"a", int64(1)}, {"b", int64(2)}, {"c", int64(3)}}))
		dcrRun(t, p, b, c, fixture.Exec("UPDATE dcr_t SET b=18", 1))
		dcrValue(t, p, b, c, "SELECT b+0 AS v FROM dcr_t", int64(18))
		if !b.PG {
			consume()
		}
	})
}
