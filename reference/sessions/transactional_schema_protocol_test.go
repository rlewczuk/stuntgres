//go:build !noservertests

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalSchemaProtocol(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "tx_wire_schema")
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			peer := serverAuthenticated(t, f.DSNFor(b))
			defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
			serverWantCode(t, w.query(t, "BEGIN"), "")
			frames := serverCycle(t, w, serverParse("create", "CREATE TABLE tx_wire(v INT DEFAULT 4)"), serverDescribe('S', "create"), serverBind("create_portal", "create", nil, nil), serverDescribe('P', "create_portal"))
			serverWantTags(t, frames, "1tn2nZ")
			serverWantReady(t, frames, 'T')
			serverScalar(t, w, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tx_wire'", "0")
			serverScalar(t, peer, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tx_wire'", "0")
			frames = serverCycle(t, w, serverExecute("create_portal", 0))
			serverWantTags(t, frames, "CZ")
			serverWantReady(t, frames, 'T')
			serverWantCode(t, w.query(t, "INSERT INTO tx_wire DEFAULT VALUES"), "")
			serverScalar(t, w, "SELECT v FROM tx_wire", "4")
			serverScalar(t, peer, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tx_wire'", "0")
			frames = serverCycle(t, w, serverParse("read", "SELECT v FROM tx_wire ORDER BY v"), serverDescribe('S', "read"))
			serverWantTags(t, frames, "1tTZ")
			serverWantCode(t, w.query(t, "SAVEPOINT before_alter"), "")
			frames = serverCycle(t, w, serverParse("alter", "ALTER TABLE tx_wire ALTER COLUMN v SET DEFAULT 9"), serverDescribe('S', "alter"), serverBind("alter_portal", "alter", nil, nil))
			serverWantTags(t, frames, "1tn2Z")
			serverWantCode(t, w.query(t, "INSERT INTO tx_wire DEFAULT VALUES"), "")
			frames = serverCycle(t, w, serverExecute("alter_portal", 0))
			serverWantTags(t, frames, "CZ")
			serverWantCode(t, w.query(t, "INSERT INTO tx_wire DEFAULT VALUES"), "")
			frames = serverCycle(t, w, serverBind("read_portal", "read", nil, nil), serverExecute("read_portal", 0))
			serverWantTags(t, frames, "2DDDCZ")
			serverWantValues(t, frames, [][]string{{"4"}, {"4"}, {"9"}})
			serverWantTags(t, serverCycle(t, w, serverClose('P', "read_portal")), "3Z")
			serverNotificationError(t, w.query(t, "ALTER TABLE tx_wire ADD COLUMN v INT"), "42701", `column "v" of relation "tx_wire" already exists`, "")
			serverNotificationError(t, w.query(t, "SELECT 1"), "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
			serverWantCode(t, w.query(t, "ROLLBACK TO before_alter"), "")
			serverScalar(t, w, "SELECT count(*) FROM tx_wire", "1")
			serverWantCode(t, w.query(t, "COMMIT"), "")
			serverScalar(t, peer, "SELECT v FROM tx_wire", "4")
			serverWantCode(t, w.query(t, "BEGIN"), "")
			serverWantCode(t, w.query(t, "ALTER TABLE tx_wire RENAME TO tx_wire_renamed"), "")
			serverWantCode(t, w.query(t, "ROLLBACK"), "")
			frames = serverCycle(t, w, serverBind("read_after", "read", nil, nil), serverExecute("read_after", 0))
			serverWantTags(t, frames, "2DCZ")
			serverWantValues(t, frames, [][]string{{"4"}})
			serverWantCode(t, w.query(t, "BEGIN"), "")
			serverWantCode(t, w.query(t, "CREATE SCHEMA tx_wire_schema"), "")
			serverWantCode(t, w.query(t, "ALTER TABLE tx_wire SET SCHEMA tx_wire_schema"), "")
			serverScalar(t, w, "SELECT v FROM tx_wire_schema.tx_wire", "4")
			serverScalar(t, peer, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tx_wire'", "1")
			serverWantCode(t, w.query(t, "ROLLBACK"), "")
			serverScalar(t, w, "SELECT v FROM tx_wire", "4")
			transactionalSchemaWireFlywayRoutes(t, w, peer)
		})
	}
}
