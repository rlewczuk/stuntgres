package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type hstoreDropIdentities struct{ typ, array, input, output, handler, jsonb, table int64 }

func hstoreDropIdentitiesOf(t *testing.T, p *fixture.Pair, db fixture.Executor) hstoreDropIdentities {
	t.Helper()
	rows, err := db.QueryContext(p.Ctx, `SELECT 'hstore'::regtype::oid::bigint,'hstore[]'::regtype::oid::bigint,'hstore_in(cstring)'::regprocedure::oid::bigint,'hstore_out(hstore)'::regprocedure::oid::bigint,'hstore_subscript_handler(internal)'::regprocedure::oid::bigint,'hstore_to_jsonb(hstore)'::regprocedure::oid::bigint,'hs_jsonb_dependent'::regclass::oid::bigint`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var id hstoreDropIdentities
	if !rows.Next() {
		t.Fatal("missing witness identities", rows.Err())
	}
	if err := rows.Scan(&id.typ, &id.array, &id.input, &id.output, &id.handler, &id.jsonb, &id.table); err != nil {
		t.Fatal(err)
	}
	return id
}

// detail applies PostgreSQL's findDependentObjects rule to the observed
// identities: members are walked newest first, depth first, and the view is
// reported through its first-reached parent. Type hstore is entered at the
// newest of itself, the functions it references (typinput, typoutput,
// typsubscript) and its generated array, which INTERNAL ownership redirects
// to the type. From the type the newer of table and cast function is walked
// first.
func (id hstoreDropIdentities) detail() string {
	entry := max(id.typ, id.array, id.input, id.output, id.handler)
	if entry > id.jsonb && id.table > id.jsonb {
		return hstoreDropWitnessDetail
	}
	return "column v of table hs_jsonb_dependent depends on type hstore\nview hs_jsonb_dependent_view depends on function hstore_to_jsonb(hstore)"
}

func hstoreDropRestricted(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, detail string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		hstoreError(t, p, b, db, "DROP EXTENSION hstore", prepared, false, false, hstoreDropRestrict(detail))
	}
	hstoreDropWitnessIntact(t, p, b, db)
}

func TestReferenceHstoreDropRestoredIdentities(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreDropWitnessSeed(t, p, b, c)
		hstoreDropRestricted(t, p, b, c, hstoreDropWitnessDetail)
		if b.PG {
			return
		} // Reset/Dump/Restore are native public APIs only.
		dump := hstoreDump(t, p, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		plain := hstoreDropIdentitiesOf(t, p, c)
		hstoreDropRestricted(t, p, b, c, plain.detail())
		if after := hstoreDump(t, p, p.Native); !bytes.Equal(dump, after) {
			t.Fatalf("restore changed deterministic dump\nbefore=%s\nafter=%s", dump, after)
		}
		// Live TEMP objects own the destination's first identities, so the
		// restored extension and consumers collide and are given new ones.
		destination := fixture.NewFixture(t, false, true)
		session, err := destination.Native.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		for i := range 12 {
			if _, err := session.ExecContext(p.Ctx, fmt.Sprintf("CREATE TEMP TABLE hs_drop_collision_%d(v integer)", i)); err != nil {
				t.Fatal(err)
			}
		}
		if err := stuntgres.Restore(p.Ctx, destination.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		target := fixture.Backend{DB: destination.Memory, Recorder: destination.MemoryRecord}
		remapped := hstoreDropIdentitiesOf(t, p, destination.Memory)
		if remapped == plain {
			t.Fatalf("restore kept every colliding identity %+v", remapped)
		}
		hstoreDropRestricted(t, p, target, destination.Memory, remapped.detail())
		if after := hstoreDump(t, p, destination.Native); !bytes.Equal(dump, after) {
			t.Fatalf("remapped restore changed deterministic dump\nbefore=%s\nafter=%s", dump, after)
		}
	})
}
