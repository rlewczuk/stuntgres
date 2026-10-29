package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
)

func TestPGInitPrivsNativeMembershipReplay(t *testing.T) {
	for _, scenario := range []string{"installed current ACL", "installed owner and identity", "ordinary member current ACL"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			source := New()
			for _, sql := range []string{`CREATE ROLE ip_replay_owner SUPERUSER`, `CREATE ROLE ip_replay_reader`, `CREATE SCHEMA ip_replay_provider`, `CREATE SCHEMA ip_replay_moved`} {
				run(t, source, sql)
			}
			switch scenario {
			case "installed current ACL":
				run(t, source, `CREATE EXTENSION unaccent SCHEMA ip_replay_provider`)
				run(t, source, `REVOKE EXECUTE ON FUNCTION ip_replay_provider.unaccent(text) FROM PUBLIC`)
				run(t, source, `GRANT EXECUTE ON FUNCTION ip_replay_provider.unaccent(text) TO ip_replay_reader`)
			case "installed owner and identity":
				run(t, source, `CREATE EXTENSION pageinspect SCHEMA ip_replay_provider`)
				run(t, source, `ALTER FUNCTION ip_replay_provider.gin_metapage_info(bytea) OWNER TO ip_replay_owner`)
				run(t, source, `ALTER FUNCTION ip_replay_provider.gin_metapage_info(bytea) RENAME TO ip_replayed_gin`)
				run(t, source, `ALTER FUNCTION ip_replay_provider.ip_replayed_gin(bytea) SET SCHEMA ip_replay_moved`)
			case "ordinary member current ACL":
				run(t, source, `CREATE EXTENSION pageinspect SCHEMA ip_replay_provider`)
				run(t, source, `CREATE SCHEMA ip_replay_ordinary`)
				run(t, source, `ALTER EXTENSION pageinspect ADD SCHEMA ip_replay_ordinary`)
				run(t, source, `GRANT USAGE ON SCHEMA ip_replay_ordinary TO ip_replay_reader`)
			}
			if !control.InitialPrivilegesMatchBaseline(source.CatalogRead()) {
				t.Fatal("current-only mutation invented source history")
			}
			var dump bytes.Buffer
			if err := Dump(ctx, source, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), control.InitialPrivilegesMarker) {
				t.Fatal("canonical source history emitted metadata")
			}
			target := New()
			for _, sql := range []string{`CREATE ROLE ip_replay_allocator`, `CREATE ROLE ip_replay_owner SUPERUSER`, `CREATE ROLE ip_replay_reader`} {
				run(t, target, sql)
			}
			if err := Restore(ctx, target, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			if !control.InitialPrivilegesMatchBaseline(target.CatalogRead()) {
				t.Fatal("native membership reconstruction manufactured historical ACLs")
			}
			var again bytes.Buffer
			if err := Dump(ctx, target, &again); err != nil {
				t.Fatal(err)
			}
			if dump.String() != again.String() {
				t.Fatalf("native membership replay changed canonical dump\nsource:\n%s\nrestored:\n%s", dump.String(), again.String())
			}
		})
	}
}

func TestPGInitPrivsNativePlainMembershipReplay(t *testing.T) {
	// No native owner record declares this ADD as reconstruction. Supported
	// plain SQL Restore must therefore capture the actual explicit raw ACL.
	prefix := `CREATE EXTENSION pageinspect;
CREATE TABLE public.ip_plain_member (a integer);
UPDATE pg_catalog.pg_class SET relacl='{postgres=arwdDxtm/postgres}'::pg_catalog.aclitem[] WHERE oid='public.ip_plain_member'::pg_catalog.regclass;
ALTER EXTENSION pageinspect ADD TABLE public.ip_plain_member;
`
	for _, drop := range []bool{false, true} {
		script := prefix
		if drop {
			script += "ALTER EXTENSION pageinspect DROP TABLE public.ip_plain_member;\n"
		}
		e := New()
		if err := Restore(context.Background(), e, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		found := false
		for address, descriptor := range e.CatalogRead().Descriptors() {
			if address.ClassID != ids.RelationClassID || descriptor.Name() != "ip_plain_member" {
				continue
			}
			found = true
			record := e.Catalog.State.InitialPrivilege(address)
			if drop && record != nil || !drop && (record == nil || record.PrivType != 'e') {
				t.Fatalf("plain SQL membership history was suppressed: drop=%v record=%#v", drop, record)
			}
		}
		if !found {
			t.Fatal("plain replay table missing")
		}
	}
}
