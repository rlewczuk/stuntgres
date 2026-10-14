package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Exercise the shared routine-container persistence consumer with both object
// alias kinds. Named-OID equality is independent of allocator numbers and cannot
// pass merely because an old numeric payload has become a different live object.
func TestReferenceRegCollationMixedRoutinePersistence(t *testing.T) {
	var emitted []byte
	regObjectScenario(t, []string{"rc_routine_store"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const tuple = `("abs(integer)",rc_routine_store.identity,rc_routine_store)`
		literal := regObjectLiteral(tuple)
		value := literal + "::rc_routine_store.mixed"
		regObjectExec(t, p, b, c,
			"CREATE SCHEMA rc_routine_store",
			`CREATE COLLATION rc_routine_store.identity FROM pg_catalog."C"`,
			"CREATE TYPE rc_routine_store.mixed AS (p regprocedure,c regcollation,n regnamespace)",
			"CREATE DOMAIN rc_routine_store.mixed_domain AS rc_routine_store.mixed",
			"CREATE TABLE public.rc_routine_values(id integer,c rc_routine_store.mixed DEFAULT "+value+
				",d rc_routine_store.mixed_domain DEFAULT "+literal+"::rc_routine_store.mixed_domain)",
			"CREATE VIEW public.rc_routine_constant AS SELECT "+value+" AS c")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.rc_routine_values(id) VALUES(1)", 1))

		check := func(t *testing.T, pair *fixture.Pair, backend fixture.Backend, db fixture.Executor) {
			for _, query := range []string{
				"SELECT c AS m FROM public.rc_routine_values WHERE id=1",
				"SELECT d AS m FROM public.rc_routine_values WHERE id=1",
				"SELECT c AS m FROM public.rc_routine_constant",
			} {
				regObjectBoth(t, pair, backend, db, regObjectSelect(
					"SELECT (m).p::text AS p,(m).c AS c,(m).n AS n,"+
						"(m).p::oid='abs(integer)'::regprocedure::oid AS routine_identity,"+
						"(m).c::oid='rc_routine_store.identity'::regcollation::oid AS collation_identity,"+
						"(m).n::oid='rc_routine_store'::regnamespace::oid AS namespace_identity FROM ("+query+") AS source",
					[]string{"p", "c", "n", "routine_identity", "collation_identity", "namespace_identity"},
					[]string{"TEXT", "REGCOLLATION", "REGNAMESPACE", "BOOL", "BOOL", "BOOL"},
					[][]any{{"abs(integer)", "rc_routine_store.identity", "rc_routine_store", true, true, true}}))
			}
		}
		if !t.Run("sql-contract", func(t *testing.T) { check(t, p, b, c) }) {
			return
		}
		t.Run("logical-replay", func(t *testing.T) {
			const identities = "SELECT 'rc_routine_store.identity'::regcollation::oid::bigint,'rc_routine_store'::regnamespace::oid::bigint"
			var originalCollation, originalNamespace int64
			if err := c.QueryRowContext(p.Ctx, identities).Scan(&originalCollation, &originalNamespace); err != nil {
				t.Fatal(err)
			}
			proofPair, proofBackend, proofConn := p, b, c
			if b.PG {
				if len(emitted) == 0 {
					t.Fatal("actual native mixed routine/object dump required for PostgreSQL replay")
				}
				regObjectExec(t, p, b, c, "DROP SCHEMA public CASCADE", "CREATE SCHEMA public", "DROP SCHEMA rc_routine_store CASCADE")
				if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
					t.Fatal("actual mixed routine/object PostgreSQL replay", err)
				}
			} else {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				emitted = bytes.Clone(dump.Bytes())
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				// Restore stages in a fresh catalog; only retained TEMP identities
				// force imported-ID remapping. Use a fresh target with several real
				// TEMP namespace/relation/rowtype allocations, not permanent spacers.
				target := fixture.NewFixture(t, false, true)
				conn, err := target.Memory.Conn(target.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				proofPair, proofBackend, proofConn = target, target.Backends()[0], conn
				regObjectExec(t, proofPair, proofBackend, proofConn,
					"CREATE TEMP TABLE rc_routine_displace_a(v integer)",
					"CREATE TEMP TABLE rc_routine_displace_b(v integer)",
					"CREATE TEMP TABLE rc_routine_displace_c(v integer)")
				if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(emitted)); err != nil {
					t.Fatal("displaced mixed routine/object Restore", err)
				}
			}
			var restoredCollation, restoredNamespace int64
			if err := proofConn.QueryRowContext(proofPair.Ctx, identities).Scan(&restoredCollation, &restoredNamespace); err != nil {
				t.Fatal(err)
			}
			if originalCollation == restoredCollation || originalNamespace == restoredNamespace {
				t.Fatalf("mixed identity replay did not exercise both displaced catalog classes: original collation=%d namespace=%d; restored collation=%d namespace=%d", originalCollation, originalNamespace, restoredCollation, restoredNamespace)
			}
			check(t, proofPair, proofBackend, proofConn)
			// Exercise replayed defaults for both supported shapes, not only saved rows.
			fixture.RunStep(t, proofPair.Ctx, proofConn, proofBackend.Recorder, fixture.Exec("DELETE FROM public.rc_routine_values", 1))
			fixture.RunStep(t, proofPair.Ctx, proofConn, proofBackend.Recorder, fixture.Exec("INSERT INTO public.rc_routine_values(id) VALUES(1)", 1))
			check(t, proofPair, proofBackend, proofConn)
		})
	})
}
