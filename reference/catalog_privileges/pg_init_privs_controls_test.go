package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGInitPrivsNativeControls(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		oid, owner, reader := ipMemberSetup(t, p, b)
		ipExec(t, p, b, b.DB, "CREATE EXTENSION amcheck", "ALTER EXTENSION pageinspect ADD TABLE ip_member", "REVOKE SELECT ON ip_member FROM ip_reader", "GRANT INSERT ON ip_member TO ip_reader", "GRANT EXECUTE ON FUNCTION bt_index_check(regclass) TO ip_reader")
		ipTableACL(t, p, b, b.DB, oid, owner, reader, "SELECT")
		ipAmcheckACLs(t, p, b)
		ipExec(t, p, b, b.DB, "CREATE ROLE ip_owner", "CREATE TABLE ip_history(a INT)", "GRANT SELECT ON ip_history TO ip_owner", "ALTER EXTENSION pageinspect ADD TABLE ip_history", "REVOKE SELECT ON ip_history FROM ip_owner", "ALTER ROLE ip_owner RENAME TO ip_renamed", "REASSIGN OWNED BY ip_renamed TO ip_reader", "DROP ROLE ip_renamed", "CREATE TABLE ip_readded(a INT)", "GRANT SELECT ON ip_readded TO ip_reader", "ALTER EXTENSION pageinspect ADD TABLE ip_readded", "ALTER EXTENSION pageinspect DROP TABLE ip_readded", "REVOKE SELECT ON ip_readded FROM ip_reader", "GRANT UPDATE ON ip_readded TO ip_reader", "ALTER EXTENSION pageinspect ADD TABLE ip_readded")
		ipControlExtraHistory(t, p, b)
		if b.PG {
			return
		}
		ipControlRoundTrip(t, p, b, oid, owner, reader, "SELECT")
	})
}

func ipControlRoundTrip(t *testing.T, p *fixture.Pair, b fixture.Backend, oid, owner, reader int64, right string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO ip_member(a,b) VALUES(7,9)", 1))
	const q = "SELECT * FROM pg_init_privs WHERE classoid=1259 AND objoid=$1 AND objsubid=0"
	var originalACL string
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT initprivs::text FROM pg_init_privs WHERE classoid=1259 AND objoid=$1 AND objsubid=0", oid).Scan(&originalACL); err != nil {
		t.Fatal(err)
	}
	old, err := b.DB.QueryContext(p.Ctx, q, oid)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	metadata, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	text := dump.String()
	if !strings.Contains(text, "stuntgres-initial-privileges-v1") {
		t.Fatal("missing historical inventory metadata")
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	ipAbsent(t, p, b, b.DB, 1259, oid)
	ipACL(t, p, b, b.DB, 1259, 3394, 0, "i", ipGrant{owner, owner, ipTableRights, false}, ipGrant{0, owner, []string{"SELECT"}, false})
	publicOwner := ipRole(t, p, b, "pg_database_owner")
	ipACL(t, p, b, b.DB, 2615, 2200, 0, "i", ipGrant{publicOwner, publicOwner, []string{"USAGE", "CREATE"}, false}, ipGrant{0, publicOwner, []string{"USAGE"}, false})
	if !old.Next() {
		t.Fatal("Reset lost exported row", old.Err())
	}
	var gotOID, gotClass, gotSub int64
	var kind, acl string
	if err := old.Scan(&gotOID, &gotClass, &gotSub, &kind, &acl); err != nil || gotOID != oid || gotClass != 1259 || gotSub != 0 || kind != "e" || acl != originalACL {
		t.Fatal("retained row", gotOID, gotClass, gotSub, kind, acl, err)
	}
	if old.Next() || old.Err() != nil {
		t.Fatal("retained EOF", old.Err())
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	ipMetadata(t, metadata, false)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(text)); err != nil {
		t.Fatal(err)
	}
	check := func(target fixture.Backend) {
		id := ipOID(t, p, target, "SELECT oid FROM pg_class WHERE relname='ip_member' AND relnamespace='public'::regnamespace")
		own := ipOwner(t, p, target)
		read := ipRole(t, p, target, "ip_reader")
		ipTableACL(t, p, target, target.DB, id, own, read, right)
		ipACL(t, p, target, target.DB, 1259, id, 2, "e", ipGrant{read, own, []string{"UPDATE"}, false})
		ipCheck(t, p, target, target.DB, "SELECT has_table_privilege('ip_reader','ip_member','SELECT') AS readable,has_table_privilege('ip_reader','ip_member','INSERT') AS writable", []string{"readable", "writable"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}})
		ipCheck(t, p, target, target.DB, "SELECT e.extname FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1259 AND d.objid=$1 AND d.deptype='e'", []string{"extname"}, []string{"NAME"}, [][]any{{"pageinspect"}}, id)
		ipAbsent(t, p, target, target.DB, 2615, 2200)
		ipCheck(t, p, target, target.DB, "SELECT a,b FROM ip_member", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}})
		if right == "SELECT" {
			ipAmcheckACLs(t, p, target)
			ipCheck(t, p, target, target.DB, "SELECT has_function_privilege('ip_reader','bt_index_check(regclass)','EXECUTE') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{true}})
			ipControlExtraHistory(t, p, target)
		}
	}
	check(b)
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector("initprivs/destination/" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	raw := sql.OpenDB(connector)
	defer raw.Close()
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	destination := fixture.Backend{DB: recorded, Recorder: recorder}
	ipExec(t, p, destination, recorded, "CREATE ROLE ip_other", "CREATE ROLE ip_reader", "CREATE TABLE ip_collision(x INT)", "CREATE TEMP TABLE ip_temp(x INT)")
	if ipRole(t, p, destination, "ip_reader") == reader {
		t.Fatal("destination did not exercise principal remapping")
	}
	if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(text)); err != nil {
		t.Fatal(err)
	}
	check(destination)
	check(b)
	for _, bad := range []string{strings.Replace(text, "stuntgres-initial-privileges-v1", "stuntgres-initial-privileges-v2", 1), text + "\nSELECT nonexistent FROM ip_member;"} {
		if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(bad)); err == nil {
			t.Fatal("malformed or late failing replay succeeded")
		}
		check(destination)
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, raw, strings.NewReader(text)); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled Restore", err)
	}
	check(destination)
	failure := errors.New("initprivs writer failed")
	if err := stuntgres.Dump(p.Ctx, raw, ipFailWriter{failure}); !errors.Is(err, failure) {
		t.Fatal("writer failure", err)
	}
	check(destination)
	if err := stuntgres.Restore(p.Ctx, raw, ipFailReader{failure}); !errors.Is(err, failure) {
		t.Fatal("reader failure", err)
	}
	check(destination)
}

type ipFailWriter struct{ err error }

func (w ipFailWriter) Write([]byte) (int, error) { return 0, w.err }

type ipFailReader struct{ err error }

func (r ipFailReader) Read([]byte) (int, error) { return 0, r.err }

func ipControlExtraHistory(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	owner := ipOwner(t, p, b)
	reader := ipRole(t, p, b, "ip_reader")
	for _, tc := range []struct{ name, right string }{{"ip_history", "SELECT"}, {"ip_readded", "UPDATE"}} {
		oid := ipOID(t, p, b, "SELECT oid FROM pg_class WHERE relname=$1 AND relnamespace='public'::regnamespace", tc.name)
		ipTableACL(t, p, b, b.DB, oid, owner, reader, tc.right)
	}
	ipCheck(t, p, b, b.DB, "SELECT has_table_privilege('ip_reader','ip_history','SELECT') AS readable", []string{"readable"}, []string{"BOOL"}, [][]any{{false}})
}
