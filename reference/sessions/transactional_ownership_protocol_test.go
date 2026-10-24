//go:build !noservertests

package reference_test

import (
	"fmt"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalOwnershipProtocol(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"wire_own_source", "wire_own_target", "wire_own_middle", "wire_own_reader", "wire_own_actor", "wire_own_downstream"})
	fixture.SchemaReferenceCleanup(t, "wire_own_schema")
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w, peer := serverAuthenticated(t, f.DSNFor(b)), serverAuthenticated(t, f.DSNFor(b))
			base := func(q string) { serverWantCode(t, w.query(t, q), "") }
			base("ROLLBACK")
			for _, role := range []string{"wire_own_source", "wire_own_target", "wire_own_middle", "wire_own_reader", "wire_own_actor", "wire_own_downstream"} {
				base("CREATE ROLE " + role)
			}
			database := serverValues(t, w.query(t, "SELECT current_database()"))[0][0]
			bootstrap := serverValues(t, w.query(t, "SELECT current_user"))[0][0]
			defer func() {
				base("ROLLBACK")
				base("RESET ROLE")
				base("ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO " + pq.QuoteIdentifier(bootstrap))
				base("DROP OWNED BY wire_own_source,wire_own_target CASCADE")
			}()
			for i, k := range []struct {
				name, create, prefix, catalog, key, ownerColumn, tag string
				drop                                                 bool
			}{
				{"table", "CREATE TABLE wire_own_object(id INT)", "ALTER TABLE wire_own_object", "pg_class", "relname='wire_own_object'", "relowner", "ALTER TABLE", false},
				{"enum", "CREATE TYPE wire_own_object AS ENUM('a')", "ALTER TYPE wire_own_object", "pg_type", "typname='wire_own_object'", "typowner", "ALTER TYPE", false},
				{"domain", "CREATE DOMAIN wire_own_object AS INT", "ALTER DOMAIN wire_own_object", "pg_type", "typname='wire_own_object'", "typowner", "ALTER DOMAIN", false},
				{"composite", "CREATE TYPE wire_own_object AS(id INT)", "ALTER TYPE wire_own_object", "pg_type", "typname='wire_own_object'", "typowner", "ALTER TYPE", false},
				{"view", "CREATE VIEW wire_own_object AS SELECT 7::INT AS id", "ALTER VIEW wire_own_object", "pg_class", "relname='wire_own_object'", "relowner", "ALTER VIEW", false},
				{"materialized", "CREATE MATERIALIZED VIEW wire_own_object AS SELECT 7::INT AS id", "ALTER MATERIALIZED VIEW wire_own_object", "pg_class", "relname='wire_own_object'", "relowner", "ALTER MATERIALIZED VIEW", false},
				{"database", "", "ALTER DATABASE " + pq.QuoteIdentifier(database), "pg_database", "datname=current_database()", "datdba", "ALTER DATABASE", false},
				{"schema", "CREATE SCHEMA wire_own_schema", "ALTER SCHEMA wire_own_schema", "pg_namespace", "nspname='wire_own_schema'", "nspowner", "ALTER SCHEMA", false},
				{"sequence", "CREATE SEQUENCE wire_own_object", "ALTER SEQUENCE wire_own_object", "pg_class", "relname='wire_own_object'", "relowner", "ALTER SEQUENCE", false},
				{"reassign", "CREATE TABLE wire_own_object(id INT)", "ALTER TABLE wire_own_object", "pg_class", "relname='wire_own_object'", "relowner", "REASSIGN OWNED", false},
				{"restrict", "CREATE TABLE wire_own_object(id INT)", "ALTER TABLE wire_own_object", "pg_class", "relname='wire_own_object'", "relowner", "DROP OWNED", true},
				{"cascade", "CREATE TABLE wire_own_object(id INT)", "ALTER TABLE wire_own_object", "pg_class", "relname='wire_own_object'", "relowner", "DROP OWNED", true},
			} {
				q := k.prefix + " OWNER TO wire_own_target"
				if k.name == "reassign" {
					q = "REASSIGN OWNED BY wire_own_source TO wire_own_target"
				}
				if k.drop {
					q = "DROP OWNED BY wire_own_source RESTRICT"
					if k.name == "cascade" {
						q = "DROP OWNED BY wire_own_source CASCADE"
					}
				}
				probe := "SELECT count(*) FROM " + k.catalog + " c JOIN pg_roles r ON r.oid=c." + k.ownerColumn + " WHERE c." + k.key + " AND r.rolname='wire_own_target'"
				old, newValue := "0", "1"
				if k.drop {
					probe = "SELECT count(*) FROM " + k.catalog + " WHERE " + k.key
					old, newValue = "1", "0"
				}
				seed := func() {
					if k.create != "" {
						base(k.create)
					}
					base(k.prefix + " OWNER TO wire_own_source")
				}
				seed()
				name := fmt.Sprintf("ownership_%d", i)
				frames := serverCycle(t, w, serverParse(name, q), serverDescribe('S', name))
				serverWantTags(t, frames, "1tnZ")
				serverWantReady(t, frames, 'I')
				serverScalar(t, peer, probe, old)
				// Flush, not Sync, keeps the implicit transaction and its named
				// portal alive while the client inspects Bind/Describe responses.
				w.send(t, append(append(serverBind(name+"_auto", name, nil, nil), serverDescribe('P', name+"_auto")...), serverPacket('H', nil)...))
				frames = serverThrough(t, w, 'n')
				serverWantTags(t, frames, "2n")
				serverScalar(t, peer, probe, old)
				frames = serverCycle(t, w, serverExecute(name+"_auto", 0))
				serverWantTags(t, frames, "CZ")
				serverWantReady(t, frames, 'I')
				transactionalOwnershipWireTag(t, frames, k.tag)
				serverScalar(t, peer, probe, newValue)
				if k.drop {
					seed()
				} else {
					base(k.prefix + " OWNER TO wire_own_source")
				}
				for _, commit := range []bool{false, true} {
					base("BEGIN")
					frames = serverCycle(t, w, serverDescribe('S', name))
					serverWantTags(t, frames, "tnZ")
					serverWantReady(t, frames, 'T')
					frames = serverCycle(t, w, serverBind(name+"_tx", name, nil, nil), serverDescribe('P', name+"_tx"))
					serverWantTags(t, frames, "2nZ")
					serverWantReady(t, frames, 'T')
					serverScalar(t, w, probe, old)
					base("SAVEPOINT before_owner")
					frames = serverCycle(t, w, serverExecute(name+"_tx", 0))
					serverWantTags(t, frames, "CZ")
					serverWantReady(t, frames, 'T')
					transactionalOwnershipWireTag(t, frames, k.tag)
					serverScalar(t, w, probe, newValue)
					serverScalar(t, peer, probe, old)
					base("ROLLBACK TO before_owner")
					serverScalar(t, w, probe, old)
					base("ROLLBACK TO before_owner")
					base("RELEASE before_owner")
					base(q)
					base("SAVEPOINT recovery")
					frames = w.query(t, "ALTER TABLE wire_own_missing OWNER TO wire_own_target")
					serverNotificationError(t, frames, "42P01", `relation "wire_own_missing" does not exist`, "")
					serverWantReady(t, frames, 'E')
					frames = w.query(t, "SELECT 1")
					serverNotificationError(t, frames, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
					serverWantReady(t, frames, 'E')
					base("ROLLBACK TO recovery")
					serverScalar(t, w, probe, newValue)
					ending, want := "ROLLBACK", old
					if commit {
						ending, want = "COMMIT", newValue
					}
					frames = w.query(t, ending)
					serverWantCode(t, frames, "")
					serverWantReady(t, frames, 'I')
					serverScalar(t, peer, probe, want)
				}
				if k.drop {
					seed()
				} else {
					base(k.prefix + " OWNER TO wire_own_source")
				}
				frames = w.query(t, q+"; ALTER TABLE wire_own_missing OWNER TO wire_own_target")
				serverWantCode(t, frames, "42P01")
				serverWantReady(t, frames, 'I')
				serverScalar(t, peer, probe, old)
				base("BEGIN; " + q + "; ROLLBACK")
				serverScalar(t, peer, probe, old)
				base(q)
				serverScalar(t, peer, probe, newValue)
				// Restore the attachment before bulk cleanup; no shared DB deletion.
				base("ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO " + pq.QuoteIdentifier(bootstrap))
				base("DROP OWNED BY wire_own_source,wire_own_target CASCADE")
			}
			for _, q := range []string{"GRANT wire_own_middle TO wire_own_source WITH ADMIN TRUE", "GRANT wire_own_middle TO wire_own_actor WITH ADMIN TRUE", "GRANT wire_own_middle TO wire_own_reader GRANTED BY wire_own_source", "GRANT wire_own_middle TO wire_own_downstream GRANTED BY wire_own_actor"} {
				base(q)
			}
			probe := "SELECT pg_has_role('wire_own_reader','wire_own_middle','MEMBER')::TEXT || ',' || pg_has_role('wire_own_downstream','wire_own_middle','MEMBER')::TEXT"
			serverWantCode(t, w.query(t, "COMMENT ON ROLE wire_own_source IS 'private predecessor'; DROP OWNED BY wire_own_source; DROP OWNED BY wire_own_actor; ALTER TABLE wire_own_missing OWNER TO wire_own_target"), "42P01")
			serverScalar(t, peer, probe, "true,true")
			base("COMMENT ON ROLE wire_own_source IS 'private predecessor'; DROP OWNED BY wire_own_source; DROP OWNED BY wire_own_actor")
			serverScalar(t, peer, probe, "false,false")
		})
	}
}

func transactionalOwnershipWireTag(t *testing.T, frames []serverFrame, want string) {
	t.Helper()
	count := 0
	for _, frame := range frames {
		if frame.tag == 'C' {
			count++
			if string(frame.body) != want+"\x00" {
				t.Fatalf("command tag %q want %q", frame.body, want)
			}
		}
	}
	if count != 1 {
		t.Fatalf("ownership completions %d want 1", count)
	}
}
