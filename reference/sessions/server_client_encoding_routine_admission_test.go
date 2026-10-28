//go:build !noservertests

package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceClientEncodingRoutineAdmission(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			pool := f.RawPool(t, b)
			for _, candidate := range []struct{ name, encoding, seed string }{
				{"unsupported", "MULE_INTERNAL", ""},
				{"missing_incoming", "LATIN1", "iso_8859_1_to_utf8"},
				{"missing_outgoing", "LATIN1", "utf8_to_iso_8859_1"},
			} {
				t.Run(candidate.name, func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					clientEncodingCommand(t, w, "SET client_encoding='LATIN1'", "SET")
					clientEncodingCommand(t, w, "CREATE FUNCTION ce_routine_existing() RETURNS integer LANGUAGE SQL SET client_encoding='UTF8' AS 'SELECT 1'", "CREATE FUNCTION")
					defer clientEncodingCommand(t, w, "DROP FUNCTION ce_routine_existing()", "DROP FUNCTION")
					clientEncodingCommand(t, w, "CREATE PROCEDURE ce_routine_proc_existing(OUT answer integer) LANGUAGE SQL SET client_encoding='UTF8' AS 'SELECT 1'", "CREATE PROCEDURE")
					defer clientEncodingCommand(t, w, "DROP PROCEDURE ce_routine_proc_existing()", "DROP PROCEDURE")
					if candidate.seed != "" {
						// Store the encoding while its actual pair is available. Neither
						// removal route may validate that retired pair later.
						for _, removal := range []string{"reset", "reset_all"} {
							clientEncodingCommand(t, w, "CREATE FUNCTION ce_routine_remove_"+removal+"() RETURNS integer LANGUAGE SQL SET client_encoding='LATIN1' AS 'SELECT 7'", "CREATE FUNCTION")
							clientEncodingCommand(t, w, "CREATE PROCEDURE ce_routine_proc_remove_"+removal+"(OUT answer integer) LANGUAGE SQL SET client_encoding='LATIN1' AS 'SELECT 7'", "CREATE PROCEDURE")
							serverScalar(t, w, "SELECT proconfig::text FROM pg_proc WHERE proname='ce_routine_remove_"+removal+"'", "{client_encoding=LATIN1}")
							serverScalar(t, w, "SELECT proconfig::text FROM pg_proc WHERE proname='ce_routine_proc_remove_"+removal+"'", "{client_encoding=LATIN1}")
						}
					}
					if candidate.seed != "" {
						clientEncodingRetireSeed(t, f.Ctx, pool, candidate.seed, "ce_routine_"+candidate.name)
					}
					var identity int64
					if err := pool.QueryRowContext(f.Ctx, "SELECT oid::bigint FROM pg_proc WHERE proname='ce_routine_existing'").Scan(&identity); err != nil {
						t.Fatal(err)
					}
					var procedureIdentity int64
					if err := pool.QueryRowContext(f.Ctx, "SELECT oid::bigint FROM pg_proc WHERE proname='ce_routine_proc_existing'").Scan(&procedureIdentity); err != nil {
						t.Fatal(err)
					}
					for _, routine := range []struct {
						name, rejected, kind, signature, returns, invoke string
						identity                                         int64
					}{
						{"ce_routine_existing", "ce_routine_rejected", "FUNCTION", "()", " RETURNS integer", "SELECT ce_routine_existing()", identity},
						{"ce_routine_proc_existing", "ce_routine_proc_rejected", "PROCEDURE", "(OUT answer integer)", "", "CALL ce_routine_proc_existing(NULL)", procedureIdentity},
					} {
						rejected := routine.rejected
						routes := []struct{ name, sql string }{
							{"alter", "ALTER " + routine.kind + " " + routine.name + "() SET client_encoding='" + candidate.encoding + "'"},
							{"replace", "CREATE OR REPLACE " + routine.kind + " " + routine.name + routine.signature + routine.returns + " LANGUAGE SQL SET client_encoding='" + candidate.encoding + "' AS 'SELECT 2'"},
							{"create", "CREATE " + routine.kind + " " + rejected + routine.signature + routine.returns + " LANGUAGE SQL SET client_encoding='" + candidate.encoding + "' AS 'SELECT 3'"},
						}
						// FROM CURRENT supplies a declaration, not permission to
						// reuse the issuer's previously admitted LATIN1 capability.
						if candidate.seed != "" {
							routes = append(routes, struct{ name, sql string }{"alter_from_current", "ALTER " + routine.kind + " " + routine.name + "() SET client_encoding FROM CURRENT"},
								struct{ name, sql string }{"replace_from_current", "CREATE OR REPLACE " + routine.kind + " " + routine.name + routine.signature + routine.returns + " LANGUAGE SQL SET client_encoding FROM CURRENT AS 'SELECT 2'"},
								struct{ name, sql string }{"create_from_current", "CREATE " + routine.kind + " " + rejected + routine.signature + routine.returns + " LANGUAGE SQL SET client_encoding FROM CURRENT AS 'SELECT 3'"})
						}
						for _, route := range routes {
							t.Run(routine.kind+"/"+route.name, func(t *testing.T) {
								frames := w.query(t, route.sql)
								serverWantTags(t, frames, "EZ")
								serverWantReady(t, frames, 'I')
								clientEncodingDiagnostic(t, frames, 'E', map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': "0A000", 'M': `invalid value for parameter "client_encoding": "` + candidate.encoding + `"`, 'D': "Conversion between " + candidate.encoding + " and UTF8 is not supported."})
								clientEncodingStatus(t, frames, "client_encoding")
								serverScalar(t, w, "SHOW client_encoding", "LATIN1")
								serverScalar(t, w, "SELECT '\xe9'::text", "\xe9")
								serverScalar(t, w, "SELECT proconfig::text FROM pg_proc WHERE proname='"+routine.name+"'", "{client_encoding=UTF8}")
								serverScalar(t, w, routine.invoke, "1")
								serverScalar(t, w, "SELECT count(*) FROM pg_proc WHERE proname='"+rejected+"'", "0")
								var current int64
								if err := pool.QueryRowContext(f.Ctx, "SELECT oid::bigint FROM pg_proc WHERE proname=$1", routine.name).Scan(&current); err != nil || current != routine.identity {
									t.Fatal("rejected declaration replaced routine identity", current, routine.identity, err)
								}
								var healthy int
								if err := pool.QueryRowContext(f.Ctx, "SELECT 42").Scan(&healthy); err != nil || healthy != 42 {
									t.Fatal("declaration peer recovery", healthy, err)
								}
								serverScalar(t, w, "SHOW client_encoding", "LATIN1")
								serverScalar(t, w, "SELECT 42", "42")
							})
						}
					}
					if candidate.seed != "" {
						for _, removal := range []struct{ name, clause string }{{"reset", "RESET client_encoding"}, {"reset_all", "RESET ALL"}} {
							for _, routine := range []struct{ name, kind, invoke, tag string }{
								{"ce_routine_remove_" + removal.name, "FUNCTION", "SELECT ce_routine_remove_" + removal.name + "()", "ALTER FUNCTION"},
								{"ce_routine_proc_remove_" + removal.name, "PROCEDURE", "CALL ce_routine_proc_remove_" + removal.name + "(NULL)", "ALTER PROCEDURE"},
							} {
								frames := clientEncodingCommand(t, w, "ALTER "+routine.kind+" "+routine.name+"() "+removal.clause, routine.tag)
								clientEncodingStatus(t, frames, "client_encoding")
								serverWantReady(t, frames, 'I')
								serverScalar(t, w, "SELECT proconfig IS NULL FROM pg_proc WHERE proname='"+routine.name+"'", "t")
								serverScalar(t, w, routine.invoke, "7")
								serverScalar(t, w, "SHOW client_encoding", "LATIN1")
								serverScalar(t, w, "SELECT '\xe9'::text", "\xe9")
								serverScalar(t, w, "SELECT 42", "42")
								clientEncodingCommand(t, w, "DROP "+routine.kind+" "+routine.name+"()", "DROP "+routine.kind)
							}
						}
					}
					// A valid pair remains admissible through each declaration route,
					// without assigning the issuer's active or reset encoding.
					frames := clientEncodingCommand(t, w, "ALTER FUNCTION ce_routine_existing() SET client_encoding='SQL_ASCII'", "ALTER FUNCTION")
					clientEncodingStatus(t, frames, "client_encoding")
					serverScalar(t, w, "SHOW client_encoding", "LATIN1")
					serverScalar(t, w, "SELECT proconfig::text FROM pg_proc WHERE proname='ce_routine_existing'", "{client_encoding=SQL_ASCII}")
					frames = clientEncodingCommand(t, w, "CREATE OR REPLACE FUNCTION ce_routine_existing() RETURNS integer LANGUAGE SQL SET client_encoding='UTF8' AS 'SELECT 2'", "CREATE FUNCTION")
					clientEncodingStatus(t, frames, "client_encoding")
					serverScalar(t, w, "SHOW client_encoding", "LATIN1")
					serverScalar(t, w, "SELECT ce_routine_existing()", "2")
					frames = clientEncodingCommand(t, w, "CREATE FUNCTION ce_routine_valid() RETURNS integer LANGUAGE SQL SET client_encoding='UTF8' AS 'SELECT 3'", "CREATE FUNCTION")
					clientEncodingStatus(t, frames, "client_encoding")
					serverScalar(t, w, "SHOW client_encoding", "LATIN1")
					serverScalar(t, w, "SELECT ce_routine_valid()", "3")
					clientEncodingCommand(t, w, "DROP FUNCTION ce_routine_valid()", "DROP FUNCTION")
					frames = clientEncodingCommand(t, w, "ALTER PROCEDURE ce_routine_proc_existing() SET client_encoding='SQL_ASCII'", "ALTER PROCEDURE")
					clientEncodingStatus(t, frames, "client_encoding")
					serverScalar(t, w, "SHOW client_encoding", "LATIN1")
					serverScalar(t, w, "SELECT proconfig::text FROM pg_proc WHERE proname='ce_routine_proc_existing'", "{client_encoding=SQL_ASCII}")
					frames = clientEncodingCommand(t, w, "CREATE OR REPLACE PROCEDURE ce_routine_proc_existing(OUT answer integer) LANGUAGE SQL SET client_encoding='UTF8' AS 'SELECT 2'", "CREATE PROCEDURE")
					clientEncodingStatus(t, frames, "client_encoding")
					serverScalar(t, w, "SHOW client_encoding", "LATIN1")
					serverScalar(t, w, "CALL ce_routine_proc_existing(NULL)", "2")
					frames = clientEncodingCommand(t, w, "CREATE PROCEDURE ce_routine_proc_valid(OUT answer integer) LANGUAGE SQL SET client_encoding='UTF8' AS 'SELECT 3'", "CREATE PROCEDURE")
					clientEncodingStatus(t, frames, "client_encoding")
					serverScalar(t, w, "SHOW client_encoding", "LATIN1")
					serverScalar(t, w, "CALL ce_routine_proc_valid(NULL)", "3")
					clientEncodingCommand(t, w, "DROP PROCEDURE ce_routine_proc_valid()", "DROP PROCEDURE")
					serverScalar(t, w, "SHOW client_encoding", "LATIN1")
					clientEncodingCommand(t, w, "ALTER FUNCTION ce_routine_existing() RESET client_encoding", "ALTER FUNCTION")
					serverScalar(t, w, "SELECT proconfig IS NULL FROM pg_proc WHERE proname='ce_routine_existing'", "t")
					clientEncodingCommand(t, w, "RESET client_encoding", "RESET")
					serverScalar(t, w, "SHOW client_encoding", "UTF8")
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
		})
	}
}
