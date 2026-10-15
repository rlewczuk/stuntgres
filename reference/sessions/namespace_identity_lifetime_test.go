package reference_test

import (
	"bytes"
	"database/sql/driver"
	"errors"
	"runtime"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNamespaceIdentityOwnership(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"namespace_identity_actor"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := namespaceConn(t, p, b), namespaceConn(t, p, b)
			getter, err := a.PrepareContext(p.Ctx, "SELECT pg_my_temp_schema()")
			if err != nil {
				t.Fatal(err)
			}
			defer getter.Close()
			checkGetter := func(want int64) {
				t.Helper()
				var got int64
				if err := getter.QueryRowContext(p.Ctx).Scan(&got); err != nil || got != want {
					t.Fatal("held getter", got, want, err)
				}
			}
			checkGetter(0)
			namespaceExec(t, p, b, a, "CREATE TEMP TABLE namespace_identity_owner(i INT)")
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO namespace_identity_owner VALUES(18)", 1))
			namespaceExec(t, p, b, peer, "CREATE TEMP TABLE namespace_identity_peer(i INT)")
			id, pid := namespaceOID(t, p, a), namespaceOID(t, p, peer)
			name, toast := namespacePair(t, p, a, id)
			_, peerToast := namespacePair(t, p, peer, pid)
			if id == pid {
				t.Fatal("shared physical identity", id)
			}
			checkGetter(id)
			namespaceExec(t, p, b, a, "CREATE ROLE namespace_identity_actor", "SET ROLE namespace_identity_actor")
			checkGetter(id)
			namespaceValue(t, p, b, a, "SELECT pg_is_other_temp_schema($1)", "pg_is_other_temp_schema", "BOOL", false, toast)
			namespaceExec(t, p, b, a, "RESET ROLE")
			// The table's namespace is independently catalog-owned, not just the getter.
			namespaceValue(t, p, b, a, "SELECT relnamespace FROM pg_class WHERE relname='namespace_identity_owner'", "relnamespace", "OID", id)
			for _, oid := range []int64{id, toast, pid, peerToast} {
				namespaceValue(t, p, b, a, "SELECT pg_is_other_temp_schema($1)", "pg_is_other_temp_schema", "BOOL", oid == pid || oid == peerToast, oid)
				namespaceValue(t, p, b, peer, "SELECT pg_is_other_temp_schema($1)", "pg_is_other_temp_schema", "BOOL", oid == id || oid == toast, oid)
			}
			namespaceValue(t, p, b, a, "SELECT i FROM "+name+".namespace_identity_owner", "i", "INT4", int64(18))
			namespaceExec(t, p, b, a, "DROP TABLE namespace_identity_owner", "DISCARD TEMP")
			if got := namespaceOID(t, p, a); got != id {
				t.Fatal("drop/discard lost identity", got, id)
			}
			checkGetter(id)
			namespaceValue(t, p, b, a, "SELECT pg_is_other_temp_schema($1)", "pg_is_other_temp_schema", "BOOL", false, toast)
			namespaceValue(t, p, b, peer, "SELECT pg_is_other_temp_schema($1)", "pg_is_other_temp_schema", "BOOL", true, id)
		})
	}
}

func TestReferenceNamespaceIdentityRetirement(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			// Force borrower reuse before acquiring the concurrent observer.
			b.DB.SetMaxOpenConns(1)
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			namespaceExec(t, p, b, c, "CREATE TEMP TABLE namespace_identity_retired(i INT)")
			id := namespaceOID(t, p, c)
			_, toast := namespacePair(t, p, c, id)
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			owner := namespaceConn(t, p, b)
			if got := namespaceOID(t, p, owner); got != id {
				t.Fatal("borrower changed physical identity", got, id)
			}
			b.DB.SetMaxOpenConns(0)
			peer := namespaceConn(t, p, b)
			var pid int
			if b.PG {
				if err := owner.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
					t.Fatal(err)
				}
			}
			if err := owner.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatal("physical retirement", err)
			}
			if b.PG {
				for {
					var live bool
					if err := peer.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)", pid).Scan(&live); err != nil {
						t.Fatal(err)
					}
					if !live {
						break
					}
					runtime.Gosched()
				}
			}
			for _, oid := range []int64{id, toast} {
				namespaceValue(t, p, b, peer, "SELECT pg_is_other_temp_schema($1)", "pg_is_other_temp_schema", "BOOL", true, oid)
			}
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT relname FROM pg_class WHERE relnamespace=$1", []string{"relname"}, []string{"NAME"}, nil, id))
			replacement := namespaceConn(t, p, b)
			if got := namespaceOID(t, p, replacement); got != 0 {
				t.Fatal("replacement inherited TEMP", got)
			}
		})
	}
}

func TestReferenceNamespaceIdentityStoredControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := namespaceConn(t, p, b), namespaceConn(t, p, b)
			namespaceExec(t, p, b, a,
				"CREATE VIEW public.namespace_identity_runtime AS SELECT current_schema() AS schema_name,current_schemas(false) AS paths,pg_my_temp_schema() AS temp_oid,pg_is_other_temp_schema(0) AS other",
				"CREATE TEMP TABLE namespace_identity_dump_excluded(i INT)")
			id := namespaceOID(t, p, a)
			if id == 0 {
				t.Fatal("missing source identity")
			}
			check := func(c fixture.Executor, want int64, schema string, paths string) {
				s := fixture.Select("SELECT schema_name,paths,temp_oid,other FROM public.namespace_identity_runtime", []string{"schema_name", "paths", "temp_oid", "other"}, []string{"NAME", "_NAME", "OID", "BOOL"}, [][]any{{schema, paths, namespaceOIDCarrier(b, want), false}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			}
			namespaceExec(t, p, b, a, "SET search_path TO public")
			check(a, id, "public", "{public}")
			namespaceExec(t, p, b, peer, "SET search_path TO pg_catalog")
			check(peer, 0, "pg_catalog", "{pg_catalog}")
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(dump.String(), "namespace_identity_dump_excluded") {
					t.Fatal("Dump serialized TEMP object")
				}
				target := fixture.NewFixture(t, false, true)
				if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT schema_name,paths,temp_oid,other FROM public.namespace_identity_runtime", []string{"schema_name", "paths", "temp_oid", "other"}, []string{"NAME", "_NAME", "OID", "BOOL"}, [][]any{{"public", "{public}", int64(0), false}})
					s.Prepared = prepared
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s)
				}
			}
		})
	}
}
