package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"testing"
	"time"
)

func TestRowLockViewAuthorization(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER view_owner", "CREATE USER view_reader", "CREATE SCHEMA s AUTHORIZATION view_owner", "CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10)"} {
		roleExecute(t, e, op, q)
	}
	owner, _ := e.Roles().Lookup("view_owner")
	reader, _ := e.Roles().Lookup("view_reader")
	grantor := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	grant := func(ref ids.ObjectRef, entries ...authorization.ACLEntry) {
		t.Helper()
		if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: entries}); err != nil {
			t.Fatal(err)
		}
	}
	entry := func(principal authorization.PrincipalRef, rights authorization.Privileges) authorization.ACLEntry {
		return authorization.ACLEntry{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: principal}, Privileges: rights}
	}
	table := roleTableRef(t, e, "t")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION view_owner")
	// Creation structurally stores marks without requiring base SELECT or UPDATE.
	for _, q := range []string{"CREATE VIEW s.owned AS SELECT id FROM public.t", "CREATE VIEW s.invoker WITH(security_invoker=true) AS SELECT id FROM public.t", "CREATE VIEW s.nested AS SELECT id FROM s.invoker", "CREATE VIEW s.stored AS SELECT id FROM public.t FOR UPDATE"} {
		roleExecute(t, e, op, q)
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	grant(table, entry(owner.Ref, authorization.SelectPrivilege))
	namespace := e.catalog.Domain.Ref(e.catalog.state.Epoch(), e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s"}))
	grant(namespace, entry(owner.Ref, authorization.UsagePrivilege|authorization.CreatePrivilege), entry(reader.Ref, authorization.UsagePrivilege))
	for _, name := range []string{"s.owned", "s.invoker", "s.nested", "s.stored"} {
		grant(roleTableRef(t, e, name), entry(owner.Ref, authorization.SelectPrivilege|authorization.UpdatePrivilege), entry(reader.Ref, authorization.SelectPrivilege|authorization.UpdatePrivilege))
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION view_reader")
	for _, name := range []string{"s.owned", "s.stored"} {
		q := "SELECT id FROM " + name
		if name == "s.owned" {
			q += " FOR UPDATE"
		}
		if _, err := e.Describe(op, statement(t, q)); err != nil {
			t.Fatal("Prepare must not deny executor ACL", err)
		}
		roleError(t, e, op, q, "42501", "permission denied for table t", "")
	}
	column := table
	column.Address.SubID = 2
	grant(column, entry(owner.Ref, authorization.UpdatePrivilege))
	result := roleExecute(t, e, op, "SELECT id FROM s.owned FOR UPDATE")
	result.Rows.Close()
	grant(roleTableRef(t, e, "s.stored"), entry(owner.Ref, authorization.SelectPrivilege|authorization.UpdatePrivilege), entry(reader.Ref, authorization.SelectPrivilege))
	result = roleExecute(t, e, op, "SELECT id FROM s.stored")
	result.Rows.Close()
	grant(table, entry(owner.Ref, authorization.SelectPrivilege), entry(reader.Ref, authorization.SelectPrivilege))
	for _, name := range []string{"s.invoker", "s.nested"} {
		roleError(t, e, op, "SELECT id FROM "+name+" FOR UPDATE", "42501", "permission denied for table t", "")
	}
	grant(column, entry(owner.Ref, authorization.UpdatePrivilege), entry(reader.Ref, authorization.UpdatePrivilege))
	for _, name := range []string{"s.invoker", "s.nested"} {
		result := roleExecute(t, e, op, "SELECT id FROM "+name+" FOR UPDATE")
		result.Rows.Close()
	}
	grant(column, entry(reader.Ref, authorization.UpdatePrivilege))
	roleError(t, e, op, "SELECT id FROM s.owned FOR UPDATE", "42501", "permission denied for table t", "")
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	lockEmpty(t, e)
}

func TestRowLockViewLifetime(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(map[bool]string{false: "reset", true: "restore"}[restore], func(t *testing.T) {
			e, a, b, c := rowLockViewFixture(t)
			retained := sessionSQL(t, c, "SELECT val FROM w ORDER BY val FOR KEY SHARE")
			defer retained.Rows.Close()
			ar := transactionBegin(t, a)
			transactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := rowLockViewAsync(t, b, ctx, "SELECT val FROM w ORDER BY val FOR SHARE")
			rowLockQueued(t, e, a.transactions.ticket, lock.ForShare)
			if restore {
				if err := e.Restore(ctx, strings.NewReader("CREATE TABLE t(id INT,v INT);INSERT INTO t VALUES(1,99);CREATE VIEW w AS SELECT id,v+1 AS val FROM t")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := e.Reset(ctx); err != nil {
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
			if got := tuples(retained); len(got) != 2 || got[0][0] != int64(11) || got[1][0] != int64(21) {
				t.Fatal("retained view changed", got)
			}
			transactionEnd(t, a, ar, false)
			lockEmpty(t, e)
			if restore {
				transactionRows(t, b, "SELECT val FROM w FOR UPDATE", 100)
			}
		})
	}
	t.Run("cancelled_wait_and_atomic_insert", func(t *testing.T) {
		e, a, b, _ := rowLockViewFixture(t)
		ar := transactionBegin(t, a)
		transactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
		ctx, cancel := context.WithCancel(context.Background())
		done := rowLockViewAsync(t, b, ctx, "SELECT val FROM w WHERE id=1 FOR SHARE")
		rowLockQueued(t, e, a.transactions.ticket, lock.ForShare)
		cancel()
		deleteAwait(t, done, 0, context.Canceled)
		transactionEnd(t, a, ar, false)
		lockEmpty(t, e)
		sessionSQL(t, b, "CREATE TABLE target(v INT)")
		result, err := rowLockViewExecute(t, e, b, "INSERT INTO target SELECT 1/(val-11) FROM w FOR UPDATE", true)
		if result.Rows != nil {
			result.Rows.Close()
			t.Fatal("failed insert cursor")
		}
		waitsDiagnostic(t, err, "22012", "division by zero")
		transactionRows(t, b, "SELECT v FROM target")
		lockEmpty(t, e)
	})
	t.Run("security_filter_before_outer_error", func(t *testing.T) {
		e, a, b, _ := rowLockEngineFixture(t)
		sessionSQL(t, b, "CREATE TABLE text_source(id INT,v TEXT)")
		sessionSQL(t, b, "INSERT INTO text_source VALUES(1,'bad'),(2,'7')")
		for _, barrier := range []string{"false", "true"} {
			name := map[string]string{"false": "plain_view", "true": "barrier_view"}[barrier]
			sessionSQL(t, b, "CREATE VIEW "+name+" WITH(security_barrier="+barrier+") AS SELECT id,v FROM text_source WHERE id=2")
			transactionRows(t, a, "SELECT CAST(v AS INT) FROM "+name+" FOR UPDATE", 7)
		}
		lockEmpty(t, e)
	})
}
