//go:build !noservertests

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerPortalSavepoints(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			t.Run("birth_and_position", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
				serverWantCode(t, w.query(t, "BEGIN"), "")
				serverWantTags(t, serverCycle(t, w, serverParse("rows", "SELECT v FROM (VALUES (1),(2),(3)) AS q(v) ORDER BY v"), serverBind("prior", "rows", nil, nil)), "12Z")
				serverWantValues(t, serverCycle(t, w, serverExecute("prior", 1)), [][]string{{"1"}})
				serverWantCode(t, w.query(t, "SAVEPOINT mark"), "")
				serverWantValues(t, serverCycle(t, w, serverExecute("prior", 1)), [][]string{{"2"}})
				serverWantTags(t, serverCycle(t, w, serverBind("born", "rows", nil, nil)), "2Z")
				serverWantCode(t, w.query(t, "ROLLBACK TO mark"), "")
				frames := serverCycle(t, w, serverExecute("prior", 1))
				serverWantTags(t, frames, "DsZ")
				serverWantValues(t, frames, [][]string{{"3"}})
				for _, packet := range [][]byte{serverDescribe('P', "born"), serverExecute("born", 0)} {
					frames = serverCycle(t, w, packet)
					serverWantTags(t, frames, "EZ")
					serverNotificationError(t, frames, "34000", "portal \"born\" does not exist", "")
					serverWantReady(t, frames, 'E')
					serverWantCode(t, w.query(t, "ROLLBACK TO mark"), "")
				}
				// Rolling back did not rewind the previously advanced outer portal.
				frames = serverCycle(t, w, serverExecute("prior", 1))
				serverWantTags(t, frames, "CZ")
				serverWantCompletions(t, frames, "SELECT 0")
			})
			t.Run("released_child_still_belongs_to_parent", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
				serverWantCode(t, w.query(t, "BEGIN; SAVEPOINT parent; SAVEPOINT child"), "")
				serverWantTags(t, serverCycle(t, w, serverParse("s", "SELECT 42"), serverBind("nested", "s", nil, nil)), "12Z")
				serverWantCode(t, w.query(t, "RELEASE child; ROLLBACK TO parent"), "")
				frames := serverCycle(t, w, serverExecute("nested", 0))
				serverWantTags(t, frames, "EZ")
				serverNotificationError(t, frames, "34000", "portal \"nested\" does not exist", "")
				serverWantCode(t, w.query(t, "ROLLBACK TO parent"), "")
				serverScalar(t, w, "SELECT 42", "42")
			})
			t.Run("error_restoration_releases_producer_resources", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				peer := serverAuthenticated(t, dsn)
				serverWantCode(t, w.query(t, "CREATE TABLE server_savepoint_resource(v integer); INSERT INTO server_savepoint_resource VALUES (1),(2)"), "")
				defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
				serverWantCode(t, w.query(t, "BEGIN; SAVEPOINT resource_mark"), "")
				frames := serverCycle(t, w, serverParse("resource", "SELECT v FROM server_savepoint_resource ORDER BY v"), serverBind("resource", "resource", nil, nil), serverExecute("resource", 1))
				serverWantTags(t, frames, "12DsZ")
				serverWantValues(t, frames, [][]string{{"1"}})
				serverNotificationError(t, w.query(t, "SELECT 1/0"), "22012", "division by zero", "")
				serverWantCode(t, w.query(t, "ROLLBACK TO resource_mark"), "")
				frames = serverCycle(t, w, serverDescribe('P', "resource"))
				serverWantTags(t, frames, "EZ")
				serverNotificationError(t, frames, "34000", "portal \"resource\" does not exist", "")
				serverWantCode(t, w.query(t, "ROLLBACK TO resource_mark"), "")
				// A separate physical session must acquire the real relation lock before
				// the outer transaction ends. Timeout bounds a retained-resource defect.
				serverWantCode(t, peer.query(t, "SET lock_timeout='1000ms'"), "")
				serverWantCode(t, peer.query(t, "BEGIN; LOCK TABLE server_savepoint_resource IN ACCESS EXCLUSIVE MODE; ROLLBACK"), "")
				serverScalar(t, peer, "SELECT count(*) FROM server_savepoint_resource", "2")
				serverScalar(t, w, "SELECT 42", "42")
			})
		})
	}
}
