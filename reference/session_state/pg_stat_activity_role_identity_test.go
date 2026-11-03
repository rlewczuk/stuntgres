package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGStatActivityRoleIdentity(t *testing.T) {
	const login = "activity_identity_login"
	const renamed = "activity_identity_renamed"
	const reader = "activity_identity_reader"
	const unrelated = "activity_identity_unrelated"
	fixture.RoleReferenceCleanup(t, []string{login, renamed, reader, unrelated})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin *sql.Conn) {
		for _, name := range []string{login, reader, unrelated} {
			activityExec(t, p, admin, "CREATE ROLE "+name+" LOGIN PASSWORD 'role-attributes-fixture'")
		}
		activityExec(t, p, admin, "GRANT pg_read_all_stats TO "+reader)
		targetPool := fixture.RoleAttributesPool(t, p, b, login)
		target, err := targetPool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer target.Close()
		pid := statActivityPID(t, p, target)
		adminPID := statActivityPID(t, p, admin)
		var loginOID string
		if err := admin.QueryRowContext(p.Ctx, "SELECT oid::text FROM pg_catalog.pg_roles WHERE rolname=$1", login).Scan(&loginOID); err != nil {
			t.Fatal("independent login OID", err)
		}
		activityExec(t, p, target, "SET application_name='retained role identity'")
		activityExec(t, p, target, "SELECT 41 /* retained dropped principal */")
		original := statActivityGet(t, p, admin, pid, true)
		if original.text(5) != login || original.text(4) != loginOID || original[10] == nil || original[12] == nil || original[13] == nil || original.text(16) != "idle" || original.text(20) != "SELECT 41 /* retained dropped principal */" || original.text(21) != "client backend" {
			t.Fatal("positive role identity witness", original)
		}
		type cut struct {
			conn       *sql.Conn
			held       *sql.Stmt
			privileged bool
		}
		cuts := make([]cut, 0, 3)
		for _, name := range []string{"", reader, unrelated} {
			var observer *sql.Conn
			if name == "" {
				observer = statActivityConn(t, p, b)
			} else {
				pool := fixture.RoleAttributesPool(t, p, b, name)
				observer, err = pool.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer observer.Close()
			}
			held, err := observer.PrepareContext(p.Ctx, statActivitySelect)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			activityExec(t, p, observer, "BEGIN")
			defer observer.ExecContext(context.Background(), "ROLLBACK")
			cuts = append(cuts, cut{observer, held, name != unrelated})
		}
		read := func(c cut, targetPID int64) statActivityRow {
			rows, err := c.held.QueryContext(p.Ctx, targetPID)
			if err != nil {
				t.Fatal("retained role projection", err)
			}
			row, ok := statActivityRead(t, rows)
			if !ok {
				t.Fatal("retained physical membership lost", targetPID)
			}
			return row
		}
		for _, c := range cuts {
			row := read(c, pid)
			if c.privileged {
				if row.text(20) != original.text(20) {
					t.Fatal("positive observer privilege", row)
				}
			} else {
				statActivityDenied(t, original, row)
			}
		}
		activityExec(t, p, admin, "ALTER ROLE "+login+" RENAME TO "+renamed)
		current := original
		current[5] = renamed
		publishRole := func(c cut, want int) {
			// Receive current catalog invalidations without clearing any activity cut,
			// ending its transaction, or replacing its prepared activity handle.
			query := fmt.Sprintf("SELECT count(*) FROM pg_catalog.pg_roles WHERE oid=%s::oid AND rolname='%s'", loginOID, renamed)
			var count int
			if err := c.conn.QueryRowContext(p.Ctx, query).Scan(&count); err != nil || count != want {
				t.Fatal("current joined principal publication", count, want, err)
			}
		}
		for _, c := range cuts {
			publishRole(c, 1)
			row := read(c, pid)
			if row.text(4) != original.text(4) || row.text(5) != renamed {
				t.Fatal("rename did not preserve OID and refresh joined name", original, row)
			}
			if c.privileged {
				for _, i := range []int{10, 11, 12, 13, 16, 19, 20, 21} {
					if row.text(i) != current.text(i) {
						t.Fatal("rename changed retained protected fact", statActivityNames[i], row, current)
					}
				}
			} else {
				statActivityDenied(t, current, row)
			}
		}
		fresh := statActivityGet(t, p, admin, pid, true)
		if fresh.text(4) != original.text(4) || fresh.text(5) != renamed || !fresh.stamp(t, 10).Equal(original.stamp(t, 10)) {
			t.Fatal("fresh renamed backend identity", original, fresh)
		}
		if err := target.Close(); err != nil {
			t.Fatal(err)
		}
		if err := targetPool.Close(); err != nil {
			t.Fatal(err)
		}
		statActivityRetired(t, p, admin, pid)
		activityExec(t, p, admin, "DROP ROLE "+renamed)
		current[5], current[14], current[15] = nil, nil, nil
		for _, c := range cuts {
			publishRole(c, 0)
			// The dropped target must not abort the entire source before PID filtering.
			unaffected := read(c, adminPID)
			if unaffected.text(2) != fmt.Sprint(adminPID) || unaffected[5] == nil {
				t.Fatal("unaffected cached backend could not be read", unaffected)
			}
			row := read(c, pid)
			if row[5] != nil || row.text(4) != original.text(4) || row[14] != nil || row[15] != nil {
				t.Fatal("dropped joined name/public retained OID/live retirement wait", original, row)
			}
			if c.privileged {
				for i := range current {
					if row.text(i) != current.text(i) {
						t.Fatal("stats privilege lost retained dropped-principal fact", statActivityNames[i], row, current)
					}
				}
			} else {
				statActivityDenied(t, current, row)
			}
		}
		// Reused spelling is a new principal and cannot relabel the old cached OID.
		activityExec(t, p, admin, "CREATE ROLE "+renamed+" LOGIN PASSWORD 'role-attributes-fixture'")
		var reusedOID string
		if err := admin.QueryRowContext(p.Ctx, "SELECT oid::text FROM pg_catalog.pg_roles WHERE rolname=$1", renamed).Scan(&reusedOID); err != nil || reusedOID == loginOID {
			t.Fatal("reused spelling did not mint distinct role identity", reusedOID, loginOID, err)
		}
		for _, c := range cuts {
			publishRole(c, 0)
			row := read(c, pid)
			if row[5] != nil || row.text(4) != loginOID {
				t.Fatal("reused spelling rebound cached principal", row, reusedOID)
			}
		}
		for i, c := range cuts {
			if i == 0 {
				activityExec(t, p, c.conn, "ROLLBACK")
				activityExec(t, p, c.conn, "BEGIN")
			} else {
				statActivityClear(t, p, c.conn)
			}
			rows, err := c.held.QueryContext(p.Ctx, pid)
			if err != nil {
				t.Fatal("refresh retired principal", err)
			}
			if row, present := statActivityRead(t, rows); present {
				t.Fatal("clear/end retained retired physical membership", row)
			}
		}
	})
}
