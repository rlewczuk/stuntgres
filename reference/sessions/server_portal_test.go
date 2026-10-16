//go:build !noservertests

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerPortalSnapshot(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			w := serverAuthenticated(t, dsn)
			peer := serverAuthenticated(t, dsn)
			serverWantCode(t, w.query(t, "CREATE TABLE server_snapshot(v int); INSERT INTO server_snapshot VALUES (1)"), "")
			serverWantCode(t, w.query(t, "BEGIN"), "")
			serverWantTags(t, serverCycle(t, w, serverParse("s", "SELECT v FROM server_snapshot ORDER BY v"), serverBind("old", "s", nil, nil)), "12Z")
			serverWantCode(t, peer.query(t, "INSERT INTO server_snapshot VALUES (2)"), "")
			serverWantValues(t, serverCycle(t, w, serverExecute("old", 0)), [][]string{{"1"}})
			serverWantValues(t, serverCycle(t, w, serverBind("new", "s", nil, nil), serverExecute("new", 0)), [][]string{{"1"}, {"2"}})
			serverWantCode(t, w.query(t, "ROLLBACK"), "")
		})
	}
}
func TestReferenceServerPortalResume(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			w := serverAuthenticated(t, dsn)
			peer := serverAuthenticated(t, dsn)
			serverWantCode(t, w.query(t, "CREATE SEQUENCE server_demand; CREATE TABLE server_demand_rows(v int); INSERT INTO server_demand_rows VALUES (1),(2),(3)"), "")
			serverWantCode(t, w.query(t, "BEGIN"), "")
			serverWantTags(t, serverCycle(t, w, serverParse("s", "SELECT nextval('server_demand') FROM server_demand_rows ORDER BY v"), serverBind("p", "s", nil, nil), serverDescribe('P', "p")), "12TZ")
			serverScalar(t, peer, "SELECT is_called FROM server_demand", "f")
			for _, want := range []string{"1", "2", "3"} {
				frames := serverCycle(t, w, serverExecute("p", 1))
				serverWantTags(t, frames, "DsZ")
				serverWantValues(t, frames, [][]string{{want}})
				serverScalar(t, peer, "SELECT last_value FROM server_demand", want)
			}
			frames := serverCycle(t, w, serverExecute("p", 1))
			serverWantTags(t, frames, "CZ")
			serverWantCompletions(t, frames, "SELECT 0")
			frames = serverCycle(t, w, serverExecute("p", 0))
			serverWantTags(t, frames, "CZ")
			serverWantCompletions(t, frames, "SELECT 0")
			serverScalar(t, peer, "SELECT last_value FROM server_demand", "3")
			serverWantCode(t, w.query(t, "ROLLBACK; BEGIN"), "")
			serverWantCode(t, serverCycle(t, w, serverBind("short", "s", nil, nil)), "")
			serverWantTags(t, serverCycle(t, w, serverExecute("short", 2)), "DDsZ")
			frames = serverCycle(t, w, serverExecute("short", 2))
			serverWantTags(t, frames, "DCZ")
			serverWantCompletions(t, frames, "SELECT 1")
			serverWantCode(t, serverCycle(t, w, serverBind("discard", "s", nil, nil), serverExecute("discard", 1), serverClose('P', "discard")), "")
			serverScalar(t, peer, "SELECT last_value FROM server_demand", "7")
			serverWantCode(t, w.query(t, "ROLLBACK"), "")
		})
	}
}
func TestReferenceServerPortalLifetime(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			serverWantCode(t, w.query(t, "BEGIN; DECLARE same CURSOR FOR SELECT 7"), "")
			serverNotificationError(t, serverCycle(t, w, serverParse("collision", "SELECT 42"), serverBind("same", "collision", nil, nil)), "42P03", "cursor \"same\" already exists", "")
			serverWantCode(t, w.query(t, "ROLLBACK"), "")
			serverWantCode(t, w.query(t, "BEGIN; DECLARE same CURSOR FOR SELECT 7"), "")
			serverWantCode(t, serverCycle(t, w, serverParse("s", "SELECT 42"), serverBind("wire", "s", nil, nil), serverClose('S', "s")), "")
			serverWantValues(t, serverCycle(t, w, serverExecute("wire", 0)), [][]string{{"42"}})
			serverScalar(t, w, "FETCH same", "7")
			serverWantCode(t, w.query(t, "COMMIT"), "")
			serverNotificationError(t, serverCycle(t, w, serverExecute("wire", 0)), "34000", "portal \"wire\" does not exist", "")
			serverWantCode(t, serverCycle(t, w, serverParse("s", "SELECT 42"), serverBind("implicit", "s", nil, nil)), "")
			serverNotificationError(t, serverCycle(t, w, serverExecute("implicit", 0)), "34000", "portal \"implicit\" does not exist", "")
			serverWantCode(t, w.query(t, "BEGIN"), "")
			serverWantCode(t, serverCycle(t, w, serverBind("p", "s", nil, nil)), "")
			serverWantCode(t, w.query(t, "DEALLOCATE s"), "")
			serverWantValues(t, serverCycle(t, w, serverExecute("p", 0)), [][]string{{"42"}})
			serverWantCode(t, w.query(t, "ROLLBACK"), "")
		})
	}
}
func TestReferenceServerPortalAtomicity(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			w := serverAuthenticated(t, dsn)
			peer := serverAuthenticated(t, dsn)
			serverWantCode(t, w.query(t, "CREATE TABLE server_returning(v int)"), "")
			serverWantCode(t, w.query(t, "BEGIN"), "")
			serverWantCode(t, serverCycle(t, w, serverParse("s", "INSERT INTO server_returning VALUES (1),(2),(3) RETURNING v"), serverBind("p", "s", nil, nil)), "")
			frames := serverCycle(t, w, serverExecute("p", 1))
			serverWantTags(t, frames, "DsZ")
			serverWantValues(t, frames, [][]string{{"1"}})
			serverScalar(t, w, "SELECT count(*) FROM server_returning", "3")
			serverScalar(t, peer, "SELECT count(*) FROM server_returning", "0")
			frames = serverCycle(t, w, serverExecute("p", 0))
			serverWantTags(t, frames, "DDCZ")
			serverWantCompletions(t, frames, "INSERT 0 2")
			serverWantCode(t, serverCycle(t, w, serverExecute("p", 0)), "")
			serverScalar(t, w, "SELECT count(*) FROM server_returning", "3")
			serverWantCode(t, w.query(t, "COMMIT"), "")
			serverScalar(t, peer, "SELECT count(*) FROM server_returning", "3")
		})
	}
}
