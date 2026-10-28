//go:build !noservertests

package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceClientEncodingAuthority(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ce_hidden", "ce_pair", "ce_moved")
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			admin := serverAuthenticated(t, dsn)
			pool := f.RawPool(t, b)
			clientEncodingCommand(t, admin, "CREATE SCHEMA ce_hidden; CREATE SCHEMA ce_pair; CREATE SCHEMA ce_moved", "CREATE SCHEMA", "CREATE SCHEMA", "CREATE SCHEMA")
			// Cleanup is registered before mutation and resolves moved/renamed pinned
			// seeds by OID; never depend on a canceled owner or session search path.
			for _, seed := range []string{"iso_8859_1_to_utf8", "utf8_to_iso_8859_1"} {
				var oid int64
				if err := pool.QueryRowContext(f.Ctx, "SELECT oid FROM pg_conversion WHERE conname=$1", seed).Scan(&oid); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					var name string
					var namespace int64
					if err := pool.QueryRowContext(ctx, "SELECT conname,connamespace::bigint FROM pg_conversion WHERE oid=$1", oid).Scan(&name, &namespace); err != nil {
						t.Error("locate seed", err)
						return
					}
					if namespace != 11 {
						// Resolve namespace through a real relation, not missing pg_namespace SQL.
						var hidden int64
						if _, err := pool.ExecContext(ctx, "CREATE TABLE ce_hidden.namespace_witness(v int)"); err != nil {
							t.Error(err)
							return
						}
						defer pool.ExecContext(ctx, "DROP TABLE ce_hidden.namespace_witness")
						if err := pool.QueryRowContext(ctx, "SELECT relnamespace::bigint FROM pg_class WHERE oid='ce_hidden.namespace_witness'::regclass::oid").Scan(&hidden); err != nil {
							t.Error(err)
							return
						}
						if namespace != hidden {
							t.Errorf("unexpected seed namespace %d want %d", namespace, hidden)
							return
						}
						if _, err := pool.ExecContext(ctx, fmt.Sprintf("ALTER CONVERSION ce_hidden.%s SET SCHEMA pg_catalog", name)); err != nil {
							t.Error("repair seed", err)
							return
						}
					}
					if name != seed {
						if _, err := pool.ExecContext(ctx, "ALTER CONVERSION pg_catalog."+name+" RENAME TO "+seed); err != nil {
							t.Error(err)
						}
					}
				})
				clientEncodingCommand(t, admin, "ALTER CONVERSION pg_catalog."+seed+" SET SCHEMA ce_hidden", "ALTER CONVERSION")
			}
			createPair := func() {
				clientEncodingCommand(t, admin, "CREATE DEFAULT CONVERSION ce_pair.incoming FOR 'LATIN1' TO 'UTF8' FROM pg_catalog.iso8859_1_to_utf8", "CREATE CONVERSION")
				clientEncodingCommand(t, admin, "CREATE DEFAULT CONVERSION ce_pair.outgoing FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1", "CREATE CONVERSION")
			}
			missing := func(t *testing.T, w *serverWire) {
				frames := w.query(t, "SET client_encoding='LATIN1'")
				serverWantTags(t, frames, "EZ")
				clientEncodingDiagnostic(t, frames, 'E', map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': "0A000", 'M': `invalid value for parameter "client_encoding": "LATIN1"`, 'D': `Conversion between LATIN1 and UTF8 is not supported.`})
				clientEncodingStatus(t, frames, "client_encoding")
			}
			createPair()
			active := serverAuthenticated(t, dsn)
			clientEncodingCommand(t, active, "SET search_path=ce_pair,public,pg_catalog; SET client_encoding='LATIN1'", "SET", "SET")
			serverScalar(t, active, "SELECT '\xe9'::text", "\xe9")
			// A real journal is held while another physical owner retires defaults.
			clientEncodingCommand(t, active, "BEGIN; SAVEPOINT s; SET LOCAL client_encoding='SJIS'", "BEGIN", "SAVEPOINT", "SET")
			clientEncodingCommand(t, admin, "DROP CONVERSION ce_pair.incoming", "DROP CONVERSION")
			fresh := serverAuthenticated(t, dsn)
			clientEncodingCommand(t, fresh, "SET search_path=ce_pair,public,pg_catalog", "SET")
			missing(t, fresh)
			serverScalar(t, fresh, "SHOW client_encoding", "UTF8")
			// COPY's file policy resolves its actual direction, not a session pair.
			// The opposite direction is absent here; UTF8 session remains unchanged.
			clientEncodingCopyBytes(t, fresh.query(t, `COPY (SELECT U&'\00E9') TO STDOUT (ENCODING 'LATIN1')`), []byte{0xe9, '\n'}, "COPY 1")
			serverScalar(t, fresh, "SHOW client_encoding", "UTF8")
			// No usable incoming pair must also reject a new physical startup.
			failed := serverDial(t, dsn)
			frames := failed.login(t, dsn, 196608, "options", "-c search_path=ce_pair,public,pg_catalog", "client_encoding", "LATIN1")
			clientEncodingDiagnostic(t, frames, 'E', map[byte]string{'S': "FATAL", 'V': "FATAL", 'C': "0A000", 'M': "conversion between LATIN1 and UTF8 is not supported"})
			clientEncodingCommand(t, admin, "DROP CONVERSION ce_pair.outgoing", "DROP CONVERSION")
			clientEncodingCommand(t, active, "ROLLBACK TO s", "ROLLBACK")
			serverScalar(t, active, "SHOW client_encoding", "LATIN1")
			serverScalar(t, active, "SELECT '\xe9'::text", "\xe9")
			clientEncodingCommand(t, active, "ROLLBACK", "ROLLBACK")
			serverScalar(t, active, "SELECT '\xe9'::text", "\xe9")
			// Explicit same-ID refresh MUST recheck, unlike journal restoration.
			missing(t, active)
			serverScalar(t, active, "SHOW client_encoding", "LATIN1")
			serverScalar(t, active, "SELECT '\xe9'::text", "\xe9")
			// Reverse-only absence, too: never prepare a one-direction session pair.
			clientEncodingCommand(t, admin, "CREATE DEFAULT CONVERSION ce_pair.incoming FOR 'LATIN1' TO 'UTF8' FROM pg_catalog.iso8859_1_to_utf8", "CREATE CONVERSION")
			missing(t, fresh)
			clientEncodingCommand(t, fresh, "CREATE TEMP TABLE ce_direction(v text)", "CREATE TABLE")
			serverCopyStart(t, fresh, "COPY ce_direction FROM STDIN (ENCODING 'LATIN1')", false, 0, 1)
			fresh.send(t, serverPacket('d', []byte{0xe9, '\n'}))
			serverWantCompletions(t, serverCopyFinish(t, fresh, false), "COPY 1")
			serverScalar(t, fresh, "SELECT v FROM ce_direction", "é")
			serverScalar(t, fresh, "SHOW client_encoding", "UTF8")
			clientEncodingCommand(t, admin, "CREATE DEFAULT CONVERSION ce_pair.outgoing FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1", "CREATE CONVERSION")
			clientEncodingCommand(t, fresh, "SET client_encoding='LATIN1'", "SET")
			clientEncodingCommand(t, admin, "ALTER CONVERSION ce_pair.outgoing RENAME TO renamed; ALTER CONVERSION ce_pair.renamed SET SCHEMA ce_moved", "ALTER CONVERSION", "ALTER CONVERSION")
			serverScalar(t, fresh, "SELECT '\xe9'::text", "\xe9")
			missing(t, fresh)
			clientEncodingCommand(t, fresh, "SET search_path=ce_moved,ce_pair,public,pg_catalog; SET client_encoding='LATIN1'", "SET", "SET")
			// New same-ID admission then rollback/DEFAULT must select the newest
			// cached capability for that ID. U2 distinguishes equal-output generations.
			clientEncodingCommand(t, fresh, "BEGIN; SET LOCAL client_encoding='SJIS'; SAVEPOINT newest; SET LOCAL client_encoding='LATIN1'; ROLLBACK TO newest; ROLLBACK", "BEGIN", "SET", "SAVEPOINT", "SET", "ROLLBACK", "ROLLBACK")
			serverScalar(t, fresh, "SHOW client_encoding", "LATIN1")
			serverScalar(t, fresh, "SELECT '\xe9'::text", "\xe9")
			clientEncodingCommand(t, admin, "DROP CONVERSION ce_pair.incoming; DROP CONVERSION ce_moved.renamed", "DROP CONVERSION", "DROP CONVERSION")
			serverScalar(t, fresh, "SELECT '\xe9'::text", "\xe9")
			// A startup reset ID survives retirement without any relookup.
			createPair()
			reset := serverDial(t, dsn)
			serverWantCode(t, reset.login(t, dsn, 196608, "options", "-c search_path=ce_pair,public,pg_catalog", "client_encoding", "LATIN1"), "")
			clientEncodingCommand(t, reset, "SET client_encoding='SJIS'", "SET")
			clientEncodingCommand(t, admin, "DROP CONVERSION ce_pair.incoming; DROP CONVERSION ce_pair.outgoing", "DROP CONVERSION", "DROP CONVERSION")
			for _, q := range []string{"RESET client_encoding", "SET client_encoding=DEFAULT", "RESET ALL", "DISCARD ALL"} {
				serverWantCode(t, reset.query(t, q), "")
				serverScalar(t, reset, "SHOW client_encoding", "LATIN1")
				serverScalar(t, reset, "SELECT '\xe9'::text", "\xe9")
			}
			// Restore actual seed objects, not fabricated kernel fallbacks.
			for _, seed := range []string{"iso_8859_1_to_utf8", "utf8_to_iso_8859_1"} {
				clientEncodingCommand(t, admin, "ALTER CONVERSION ce_hidden."+seed+" SET SCHEMA pg_catalog", "ALTER CONVERSION")
			}
			newest := clientEncodingLogin(t, dsn, "LATIN1")
			serverScalar(t, newest, "SELECT '\xe9'::text", "\xe9")
		})
	}
	t.Run("native_control_replacement", clientEncodingAuthorityReplacement)
}
