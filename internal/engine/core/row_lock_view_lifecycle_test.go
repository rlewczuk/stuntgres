package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestRowLockViewAuthorization(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER view_owner", "CREATE USER view_reader", "CREATE SCHEMA s AUTHORIZATION view_owner", "CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10)"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	owner, _ := e.Roles().Lookup("view_owner")
	reader, _ := e.Roles().Lookup("view_reader")
	grantor := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	grant := func(ref ids.ObjectRef, entries ...authorization.ACLEntry) {
		t.Helper()
		if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: entries}); err != nil {
			t.Fatal(err)
		}
	}
	entry := func(principal authorization.PrincipalRef, rights authorization.Privileges) authorization.ACLEntry {
		return authorization.ACLEntry{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: principal}, Privileges: rights}
	}
	table := enginetest.RoleTableRef(t, e, "t")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION view_owner")
	// Creation structurally stores marks without requiring base SELECT or UPDATE.
	for _, q := range []string{"CREATE VIEW s.owned AS SELECT id FROM public.t", "CREATE VIEW s.invoker WITH(security_invoker=true) AS SELECT id FROM public.t", "CREATE VIEW s.nested AS SELECT id FROM s.invoker", "CREATE VIEW s.stored AS SELECT id FROM public.t FOR UPDATE"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	grant(table, entry(owner.Ref, authorization.SelectPrivilege))
	namespace := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s"}))
	grant(namespace, entry(owner.Ref, authorization.UsagePrivilege|authorization.CreatePrivilege), entry(reader.Ref, authorization.UsagePrivilege))
	for _, name := range []string{"s.owned", "s.invoker", "s.nested", "s.stored"} {
		grant(enginetest.RoleTableRef(t, e, name), entry(owner.Ref, authorization.SelectPrivilege|authorization.UpdatePrivilege), entry(reader.Ref, authorization.SelectPrivilege|authorization.UpdatePrivilege))
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION view_reader")
	for _, name := range []string{"s.owned", "s.stored"} {
		q := "SELECT id FROM " + name
		if name == "s.owned" {
			q += " FOR UPDATE"
		}
		if _, err := e.Describe(op, enginetest.Statement(t, q)); err != nil {
			t.Fatal("Prepare must not deny executor ACL", err)
		}
		enginetest.RoleError(t, e, op, q, "42501", "permission denied for table t", "")
	}
	column := table
	column.Address.SubID = 2
	grant(column, entry(owner.Ref, authorization.UpdatePrivilege))
	result := enginetest.RoleExecute(t, e, op, "SELECT id FROM s.owned FOR UPDATE")
	result.Rows.Close()
	grant(enginetest.RoleTableRef(t, e, "s.stored"), entry(owner.Ref, authorization.SelectPrivilege|authorization.UpdatePrivilege), entry(reader.Ref, authorization.SelectPrivilege))
	result = enginetest.RoleExecute(t, e, op, "SELECT id FROM s.stored")
	result.Rows.Close()
	grant(table, entry(owner.Ref, authorization.SelectPrivilege), entry(reader.Ref, authorization.SelectPrivilege))
	for _, name := range []string{"s.invoker", "s.nested"} {
		enginetest.RoleError(t, e, op, "SELECT id FROM "+name+" FOR UPDATE", "42501", "permission denied for table t", "")
	}
	grant(column, entry(owner.Ref, authorization.UpdatePrivilege), entry(reader.Ref, authorization.UpdatePrivilege))
	for _, name := range []string{"s.invoker", "s.nested"} {
		result := enginetest.RoleExecute(t, e, op, "SELECT id FROM "+name+" FOR UPDATE")
		result.Rows.Close()
	}
	grant(column, entry(reader.Ref, authorization.UpdatePrivilege))
	enginetest.RoleError(t, e, op, "SELECT id FROM s.owned FOR UPDATE", "42501", "permission denied for table t", "")
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	enginetest.LockEmpty(t, e)
}

func TestRowLockViewLifetime(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(map[bool]string{false: "reset", true: "restore"}[restore], func(t *testing.T) {
			e, a, b, c := rowLockViewFixture(t)
			retained := enginetest.SessionSQL(t, c, "SELECT val FROM w ORDER BY val FOR KEY SHARE")
			defer retained.Rows.Close()
			ar := enginetest.TransactionBegin(t, a)
			enginetest.TransactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := rowLockViewAsync(t, b, ctx, "SELECT val FROM w ORDER BY val FOR SHARE")
			rowLockQueued(t, e, a.Transactions.Ticket, lock.ForShare)
			if restore {
				if err := engine.Restore(ctx, e, strings.NewReader("CREATE TABLE t(id INT,v INT);INSERT INTO t VALUES(1,99);CREATE VIEW w AS SELECT id,v+1 AS val FROM t")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := engine.Reset(ctx, e); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case outcome := <-done:
				if !errors.Is(outcome.err, ids.ErrCatalogReplaced) || outcome.result.Rows != nil {
					t.Fatal(outcome)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if got := enginetest.Tuples(retained); len(got) != 2 || got[0][0] != int64(11) || got[1][0] != int64(21) {
				t.Fatal("retained view changed", got)
			}
			enginetest.TransactionEnd(t, a, ar, false)
			enginetest.LockEmpty(t, e)
			if restore {
				enginetest.TransactionRows(t, b, "SELECT val FROM w FOR UPDATE", 100)
			}
		})
	}
	t.Run("cancelled_wait_and_atomic_insert", func(t *testing.T) {
		e, a, b, _ := rowLockViewFixture(t)
		ar := enginetest.TransactionBegin(t, a)
		enginetest.TransactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
		ctx, cancel := context.WithCancel(context.Background())
		done := rowLockViewAsync(t, b, ctx, "SELECT val FROM w WHERE id=1 FOR SHARE")
		rowLockQueued(t, e, a.Transactions.Ticket, lock.ForShare)
		cancel()
		deleteAwait(t, done, 0, context.Canceled)
		enginetest.TransactionEnd(t, a, ar, false)
		enginetest.LockEmpty(t, e)
		enginetest.SessionSQL(t, b, "CREATE TABLE target(v INT)")
		result, err := rowLockViewExecute(t, e, b, "INSERT INTO target SELECT 1/(val-11) FROM w FOR UPDATE", true)
		if result.Rows != nil {
			result.Rows.Close()
			t.Fatal("failed insert cursor")
		}
		waitsDiagnostic(t, err, "22012", "division by zero")
		enginetest.TransactionRows(t, b, "SELECT v FROM target")
		enginetest.LockEmpty(t, e)
	})
	t.Run("security_filter_before_outer_error", func(t *testing.T) {
		e, a, b, _ := rowLockEngineFixture(t)
		enginetest.SessionSQL(t, b, "CREATE TABLE text_source(id INT,v TEXT)")
		enginetest.SessionSQL(t, b, "INSERT INTO text_source VALUES(1,'bad'),(2,'7')")
		for _, barrier := range []string{"false", "true"} {
			name := map[string]string{"false": "plain_view", "true": "barrier_view"}[barrier]
			enginetest.SessionSQL(t, b, "CREATE VIEW "+name+" WITH(security_barrier="+barrier+") AS SELECT id,v FROM text_source WHERE id=2")
			enginetest.TransactionRows(t, a, "SELECT CAST(v AS INT) FROM "+name+" FOR UPDATE", 7)
		}
		enginetest.LockEmpty(t, e)
	})
}
