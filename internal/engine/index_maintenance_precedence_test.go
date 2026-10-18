package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

// Real ACLs admit schema access but deny parent MAINTAIN. A held parent writer
// makes ordering observable: malformed controls must return before any wait.
func TestIndexMaintenanceMalformedControlBeforeDeniedWait(t *testing.T) {
	e := New()
	admin, actor := sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{"CREATE USER im_badcontrol", "CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1)", "CREATE INDEX ix ON t(n)"} {
		sessionSQL(t, admin, q)
	}
	table := roleTableRef(t, e, "t")
	owner := e.state.descriptors[table.Address].(*relationDescriptor).owner
	user, ok := e.Roles().Lookup("im_badcontrol")
	if !ok {
		t.Fatal("missing role fixture")
	}
	namespace := e.domain.Ref(e.state.epoch, ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID})
	if err := e.SetObjectACL(context.Background(), namespace, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: user.Ref}, Privileges: authorization.UsagePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetObjectACL(context.Background(), table, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	sessionSQL(t, actor, "SET SESSION AUTHORIZATION im_badcontrol")
	tx := transactionBegin(t, admin)
	sessionSQL(t, admin, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
	before := indexMaintenanceCapture(e)
	for _, target := range []string{"INDEX ix", "TABLE t"} {
		for _, c := range []struct{ option, message, position string }{
			{"unknown", `unrecognized REINDEX option "unknown"`, "10"},
			{"verbose 'bad'", "verbose requires a Boolean value", ""},
			{"concurrently 'bad'", "concurrently requires a Boolean value", ""},
			{"tablespace", "tablespace requires a parameter", ""},
			{"verbose false, unknown", `unrecognized REINDEX option "unknown"`, "25"},
		} {
			q := "REINDEX (" + c.option + ") " + target
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			op, err := actor.Acquire(ctx)
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			result, err := e.Execute(op, statement(t, q), 0, nil)
			op.Done()
			cancel()
			if result.Rows != nil {
				result.Rows.Close()
				t.Fatal("failed malformed control exposed rows")
			}
			var diagnostic *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: c.message, Position: c.position}
			if result.Count != 0 || !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal("malformed controls lost precedence before denied target/writer", q, result, diagnostic, err)
			}
			indexMaintenanceUnchanged(t, e, before)
		}
	}
	transactionEnd(t, admin, tx, false)
	lockEmpty(t, e)
}
