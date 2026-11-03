package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatGSSAPIMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a := gssapiConn(t, p, b.DB)
			peer := gssapiConn(t, p, b.DB)
			aPID, peerPID := gssapiPID(t, p, a), gssapiPID(t, p, peer)
			if aPID == peerPID {
				t.Fatal("physical sessions share PID", aPID)
			}
			for _, query := range []string{
				"SELECT * FROM pg_stat_gssapi WHERE pid=-1",
				gssapiQuery + " WHERE pid=-1",
				"SELECT g.pid,g.gss_authenticated,g.principal,g.encrypted,g.credentials_delegated FROM pg_catalog.pg_stat_gssapi AS g WHERE g.pid=-1",
			} {
				step := fixture.Select(query, gssapiNames, gssapiTypes, [][]any{})
				step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { gssapiMetadata(t, rows, b.PG) }
				fixture.RunStep(t, p.Ctx, a, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Prepared(step))
			}
			gssapiBoth(t, p, b, a, gssapiPIDQuery, gssapiTuples(peerPID), peerPID)
			gssapiBoth(t, p, b, a, gssapiPIDQuery, gssapiTuples(), int64(-1))
			pairQuery := gssapiQuery + " WHERE pid IN ($1,$2)"
			gssapiBoth(t, p, b, a, pairQuery, gssapiTuples(aPID, peerPID), aPID, peerPID)
			for _, prepared := range []bool{false, true} {
				step := fixture.Select("SELECT count(*) FROM pg_catalog.pg_stat_gssapi WHERE pid IN ($1,$2)", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, aPID, peerPID)
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, a, b.Recorder, step)
				for _, tc := range []struct {
					query string
					count int64
					args  []any
				}{
					{pairQuery, 2, []any{aPID, peerPID}},
					{gssapiPIDQuery, 1, []any{peerPID}},
					{gssapiPIDQuery, 0, []any{int64(-1)}},
				} {
					s := fixture.Exec(tc.query, tc.count, tc.args...)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
				}
			}
			statement, err := a.PrepareContext(p.Ctx, pairQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			gssapiBoth(t, p, b, a, pairQuery, gssapiTuples(aPID, peerPID), aPID, peerPID)
			var relationOID, compositeOID, arrayOID, namespaceOID int64
			if err := a.QueryRowContext(p.Ctx, "SELECT 'pg_catalog.pg_stat_gssapi'::regclass::oid::bigint,'pg_catalog.pg_stat_gssapi'::regtype::oid::bigint,'pg_catalog.pg_stat_gssapi[]'::regtype::oid::bigint").Scan(&relationOID, &compositeOID, &arrayOID); err != nil {
				t.Fatal(err)
			}
			if err := a.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_namespace WHERE nspname='pg_catalog'").Scan(&namespaceOID); err != nil {
				t.Fatal(err)
			}
			if relationOID <= 0 || compositeOID <= 0 || arrayOID <= 0 || relationOID == compositeOID || relationOID == arrayOID || compositeOID == arrayOID {
				t.Fatal("invalid observed relation/composite/array identities", relationOID, compositeOID, arrayOID)
			}
			if !b.PG && (relationOID != 12257 || compositeOID != 12259 || arrayOID != 12258) {
				t.Fatal("native catalog inventory identities", relationOID, compositeOID, arrayOID)
			}
			query := "SELECT c.oid::bigint,c.relkind::text,c.relnamespace::bigint,c.reltype::bigint,t.typrelid::bigint,t.typarray::bigint,a.typelem::bigint,a.typrelid::bigint FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_gssapi'::regclass"
			step := fixture.Select(query, []string{"oid", "relkind", "relnamespace", "reltype", "typrelid", "typarray", "typelem", "typrelid"}, []string{"INT8", "TEXT", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{relationOID, "v", namespaceOID, compositeOID, relationOID, arrayOID, compositeOID, int64(0)}})
			fixture.RunStep(t, p.Ctx, a, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Prepared(step))
			ids := []int64{23, 16, 25, 16, 16}
			var attrs [][]any
			for i, name := range gssapiNames {
				collation := int64(0)
				if ids[i] == 25 {
					collation = 100
				}
				attrs = append(attrs, []any{int64(i + 1), name, ids[i], false, int64(-1), int64(0), collation})
			}
			step = fixture.Select("SELECT attnum::integer,attname::text,atttypid::bigint,attnotnull,atttypmod::integer,attndims::integer,attcollation::bigint FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_gssapi'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attndims", "attcollation"}, []string{"INT4", "TEXT", "INT8", "BOOL", "INT4", "INT4", "INT8"}, attrs)
			fixture.RunStep(t, p.Ctx, a, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Prepared(step))
			step = fixture.Select("SELECT has_table_privilege('pg_signal_backend','pg_catalog.pg_stat_gssapi','SELECT') AS readable", []string{"readable"}, []string{"BOOL"}, [][]any{{true}})
			fixture.RunStep(t, p.Ctx, a, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Prepared(step))
		})
	}
}

func TestReferencePGStatGSSAPILifecycle(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"gss_life"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			gssapiCreateRoles(t, p, b, "gss_life")
			a := gssapiConn(t, p, b.DB)
			pool, peer := gssapiLogin(t, p, b, "gss_life", 1)
			aPID, peerPID := gssapiPID(t, p, a), gssapiPID(t, p, peer)
			if aPID == peerPID {
				t.Fatal("physical sessions share PID", aPID)
			}
			query := gssapiQuery + " WHERE pid IN ($1,$2)"
			gssapiBoth(t, p, b, a, query, gssapiTuples(aPID, peerPID), aPID, peerPID)
			gssapiBoth(t, p, b, peer, gssapiPIDQuery, gssapiTuples(peerPID), peerPID)
			gssapiExec(t, p, a, "SET track_activities=off")
			gssapiBoth(t, p, b, a, query, gssapiTuples(aPID, peerPID), aPID, peerPID)
			gssapiExec(t, p, a, "RESET track_activities")
			if err := peer.Close(); err != nil {
				t.Fatal(err)
			}
			// Returning a borrower leaves the physical peer alive in this pool.
			gssapiBoth(t, p, b, a, gssapiPIDQuery, gssapiTuples(peerPID), peerPID)
			peer = gssapiConn(t, p, pool)
			if got := gssapiPID(t, p, peer); got != peerPID {
				t.Fatalf("pooled PID=%d want %d", got, peerPID)
			}
			pool.SetMaxIdleConns(0)
			gssapiRetire(t, p, b, a, pool, peer, peerPID)
			gssapiBoth(t, p, b, a, query, gssapiTuples(aPID), aPID, peerPID)
			gssapiBoth(t, p, b, a, gssapiPIDQuery, gssapiTuples(), peerPID)
		})
	}
}
