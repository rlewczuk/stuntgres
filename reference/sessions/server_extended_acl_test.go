//go:build !noservertests

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func serverRoutineACL(t *testing.T, f *fixture.ServerFixture, b fixture.Backend) {
	t.Helper()
	admin := serverAuthenticated(t, f.DSNFor(b))
	serverWantCode(t, admin.query(t, "CREATE ROLE server_wire_function_reader"), "")
	defer func() {
		// Restore effective PostgreSQL authority and remove the actual role.
		// Native optional dumps are disabled by this fixture's owning test;
		// compensating GRANT is not claimed to restore a canonical descriptor.
		serverWantCode(t, admin.query(t, "GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO PUBLIC"), "")
		serverWantCode(t, admin.query(t, "REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM server_wire_function_reader"), "")
		serverWantCode(t, admin.query(t, "DROP ROLE server_wire_function_reader"), "")
	}()
	serverWantCode(t, admin.query(t, "GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO server_wire_function_reader"), "")
	serverWantCode(t, admin.query(t, "REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC"), "")
	w := serverAuthenticated(t, f.DSNFor(b))
	defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), ""); w.Close() }()
	serverWantCode(t, w.query(t, "SET ROLE server_wire_function_reader"), "")
	serverWantCode(t, w.query(t, "BEGIN"), "")
	serverWantCode(t, serverCycle(t, w, serverParse("s", "SELECT pg_catalog.abs(v) FROM (VALUES (-2),(-1)) AS q(v) ORDER BY v"), serverBind("p", "s", nil, nil)), "")
	serverWantCode(t, admin.query(t, "REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM server_wire_function_reader"), "")
	serverWantValues(t, serverCycle(t, w, serverExecute("p", 1)), [][]string{{"2"}})
	serverWantValues(t, serverCycle(t, w, serverExecute("p", 0)), [][]string{{"1"}})
	serverNotificationError(t, serverCycle(t, w, serverBind("next", "s", nil, nil)), "42501", "permission denied for function abs", "")
	serverWantReady(t, w.query(t, "ROLLBACK"), 'I')
}
