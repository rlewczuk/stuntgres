//go:build reference

package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceSchemaPublicLifecycleControls(t *testing.T) {
	for _, tc := range []struct {
		name, setup, namespace, owner string
		table                         bool
	}{
		{"renamed", "CREATE TABLE public.t(id INT NOT NULL); INSERT INTO public.t VALUES(7); ALTER SCHEMA public RENAME TO replay_public;", "replay_public", "postgres", true},
		{"renamed_empty", "ALTER SCHEMA public RENAME TO replay_public;", "replay_public", "postgres", false},
		{"missing", "DROP SCHEMA public;", "", "", false},
		{"missing_nonpublic", "DROP SCHEMA public; CREATE SCHEMA replay_other_schema CREATE TABLE t(id INT NOT NULL); INSERT INTO replay_other_schema.t VALUES(9);", "replay_other_schema", "postgres", true},
		{"recreated", "DROP SCHEMA public; CREATE SCHEMA public; CREATE TABLE public.t(id INT NOT NULL); INSERT INTO public.t VALUES(7);", "public", "postgres", true},
		{"ordinary_empty", "DROP SCHEMA public; CREATE SCHEMA public AUTHORIZATION replay_owner;", "public", "replay_owner", false},
		{"ordinary", "DROP SCHEMA public; CREATE SCHEMA public AUTHORIZATION replay_owner; SET SESSION AUTHORIZATION replay_owner; CREATE TABLE public.t(id INT NOT NULL); INSERT INTO public.t VALUES(7); RESET SESSION AUTHORIZATION;", "public", "replay_owner", true},
		{"transferred_collision", "ALTER SCHEMA public RENAME TO replay_public; ALTER SCHEMA replay_public OWNER TO replay_owner; CREATE SCHEMA public AUTHORIZATION replay_other; SET SESSION AUTHORIZATION replay_other; CREATE TABLE public.t(id INT NOT NULL); INSERT INTO public.t VALUES(7); RESET SESSION AUTHORIZATION;", "public", "replay_other", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roleReferenceCleanup(t, []string{"replay_owner", "replay_other"})
			p := newReferencePair(t)
			schemaReferenceCleanup(t, "replay_public", "replay_other_schema")
			// Repair the controlled bootstrap name before newReferencePair's unconditional
			// cleanup reset. This cleanup also releases ordinary public ownership/ACLs.
			t.Cleanup(func() {
				if _, err := referenceAdmin.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;"); err != nil {
					t.Error(err)
				}
			})
			// The shared fixture supplies the native owner; keep the authenticator
			// stuntgres and replay SQL unchanged.
			for _, q := range []string{"ALTER SCHEMA public OWNER TO postgres"} {
				if _, err := referenceAdmin.ExecContext(p.ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			for _, q := range []string{"CREATE USER replay_owner", "CREATE USER replay_other"} {
				p.step(t, referenceExec(q, 0))
			}
			c, err := p.native.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, q := range strings.Split(tc.setup, ";") {
				if strings.TrimSpace(q) == "" {
					continue
				}
				if _, err := c.ExecContext(p.ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			oracle, err := p.postgres.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer oracle.Close()
			defer oracle.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			if _, err := oracle.ExecContext(p.ctx, "SET SESSION AUTHORIZATION postgres"); err != nil {
				t.Fatal(err)
			}
			if _, err := oracle.ExecContext(p.ctx, dump.String()); err != nil {
				t.Fatal(dump.String(), err)
			}
			if _, err := oracle.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Fatal(err)
			}
			if tc.namespace == "" {
				var exists bool
				if err := oracle.QueryRowContext(p.ctx, "SELECT EXISTS(SELECT FROM pg_namespace WHERE nspname='public')").Scan(&exists); err != nil || exists {
					t.Fatal("public invented", exists, err)
				}
			} else {
				var owner string
				if err := oracle.QueryRowContext(p.ctx, "SELECT nspowner::regrole::text FROM pg_namespace WHERE nspname=$1", tc.namespace).Scan(&owner); err != nil || owner != tc.owner {
					t.Fatal(owner, tc.owner, err)
				}
				if tc.table {
					for _, backend := range []struct {
						db     *sql.DB
						record *referenceRecorder
					}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
						want := int64(7)
						if tc.name == "missing_nonpublic" {
							want = 9
						}
						referenceRunStep(t, p.ctx, backend.db, backend.record, referencePrepared(referenceSelect("SELECT id FROM "+tc.namespace+".t", []string{"id"}, []string{"INT4"}, [][]any{{want}})))
					}
				}
			}
			if tc.name == "transferred_collision" {
				var owner string
				if err := oracle.QueryRowContext(p.ctx, "SELECT nspowner::regrole::text FROM pg_namespace WHERE nspname='replay_public'").Scan(&owner); err != nil || owner != "replay_owner" {
					t.Fatal(owner, err)
				}
			}
			// Bootstrap ACLs differ between native and PostgreSQL. Exact native ACL
			// fidelity is asserted internally; here assert ordinary recreated public
			// has no fabricated PUBLIC entry independently, not byte parity.
			if tc.namespace == "public" {
				var publicGrant bool
				if err := oracle.QueryRowContext(p.ctx, "SELECT EXISTS(SELECT FROM pg_namespace n CROSS JOIN LATERAL aclexplode(n.nspacl) a WHERE n.nspname='public' AND a.grantee=0)").Scan(&publicGrant); err != nil || publicGrant {
					t.Fatal("recreated PUBLIC grant", publicGrant, err)
				}
			}
		})
	}
}
