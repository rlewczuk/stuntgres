package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func TestNotNullAXWaitCancellationAndEnd(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ALTER a DROP NOT NULL", "ALTER TABLE t DROP CONSTRAINT a_nn", "ALTER TABLE t DROP CONSTRAINT a_nn RESTRICT", "ALTER TABLE t DROP CONSTRAINT a_nn CASCADE"} {
		for _, ending := range []bool{false, true} {
			t.Run(q+map[bool]string{false: "/cancel", true: "/end"}[ending], func(t *testing.T) {
				e := New()
				a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
				dropping := strings.Contains(q, "DROP")
				if dropping {
					sessionSQL(t, a, "CREATE TABLE t(a INT CONSTRAINT a_nn NOT NULL,b INT CONSTRAINT b_nn NOT NULL)")
				} else {
					sessionSQL(t, a, "CREATE TABLE t(a INT,b INT CONSTRAINT b_nn NOT NULL)")
				}
				sessionSQL(t, a, "INSERT INTO t VALUES(7,8)")
				ref := roleTableRef(t, e, "t")
				old, storage := e.Catalog.State.Get(ref.Address), e.Storage.Relations[ref.Address]
				ends, generation := storage.Ends, storage.Generation
				tx := transactionBegin(t, a)
				sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := lockAsync(e, b, ctx, q, false)
				lockPending(t, e, 1)
				if ending {
					b.RequestEnd(session.CleanClose)
				} else {
					cancel()
				}
				lockAwait(t, done, context.Canceled)
				lockPending(t, e, 0)
				if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || storage.Ends != ends || storage.Generation != generation || len(storage.Versions) != 1 {
					t.Fatal("canceled waiter changed schema/storage")
				}
				transactionEnd(t, a, tx, false)
				lockEmpty(t, e)
				if dropping {
					notNullDropEnforcement(t, e)
				}
				notNullDropRows(t, e, 7, 1)
				c := sessionStart(t, e, nil)
				sessionSQL(t, c, q)
				if dropping {
					sessionSQL(t, c, "UPDATE t SET a=NULL")
					sessionSQL(t, c, "INSERT INTO t VALUES(NULL,9)")
					notNullRemainingEnforcement(t, e)
				}
				lockEmpty(t, e)
			})
		}
	}
}

func notNullDropEnforcement(t *testing.T, e *Engine) {
	t.Helper()
	for _, q := range []string{"INSERT INTO t VALUES(NULL,8)", "UPDATE t SET a=NULL"} {
		_, err := testExecute(e, context.Background(), statement(t, q), nil)
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != "23502" || got.Column != "a" {
			t.Fatal(q, err)
		}
	}
	notNullRemainingEnforcement(t, e)
}

func notNullRemainingEnforcement(t *testing.T, e *Engine) {
	t.Helper()
	for _, q := range []string{"INSERT INTO t VALUES(7,NULL)", "UPDATE t SET b=NULL"} {
		_, err := testExecute(e, context.Background(), statement(t, q), nil)
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != "23502" || got.Column != "b" {
			t.Fatal(q, err)
		}
	}
}

func notNullDropRows(t *testing.T, e *Engine, a int64, count int) {
	t.Helper()
	want := make([][]driver.Value, count)
	for i := range want {
		want[i] = []driver.Value{a, int64(8)}
	}
	if got := tuples(run(t, e, "SELECT * FROM t")); !reflect.DeepEqual(got, want) {
		t.Fatal("failed alteration changed rows", got, want)
	}
}

func TestNotNullAXGrantTimeDescriptorAndRetargetFence(t *testing.T) {
	for _, kind := range []string{"revision", "shadow", "reset", "restore", "recreate"} {
		t.Run(kind, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE SCHEMA shadow")
			sessionSQL(t, a, "CREATE TABLE t(a INT)")
			sessionSQL(t, a, "INSERT INTO t VALUES(7)")
			if kind == "shadow" {
				sessionSQL(t, b, "SET search_path TO shadow,public")
			}
			ref := roleTableRef(t, e, "t")
			tx := transactionBegin(t, a)
			sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := lockAsync(e, b, ctx, "ALTER TABLE t ADD NOT NULL a", false)
			lockPending(t, e, 1)
			switch kind {
			case "revision":
				stage, ref, _ := notNullMetadataStage(t, e)
				prepared, err := e.ValidateCatalogStage(context.Background(), stage)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: ref.Epoch}); err != nil {
					t.Fatal(err)
				}
			case "shadow":
				run(t, e, "CREATE TABLE shadow.t(a INT)")
			case "reset":
				if err := Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			case "restore":
				if err := Restore(context.Background(), e, strings.NewReader("CREATE TABLE t(a INT);INSERT INTO t VALUES(91)")); err != nil {
					t.Fatal(err)
				}
			case "recreate":
				sessionSQL(t, a, "DROP TABLE t")
				sessionSQL(t, a, "CREATE TABLE t(a INT)")
			}
			if kind == "reset" || kind == "restore" {
				lockAwait(t, done, ids.ErrCatalogReplaced)
				transactionEnd(t, a, tx, false)
			} else {
				transactionEnd(t, a, tx, kind == "recreate")
				if kind == "revision" {
					lockAwait(t, done, nil)
					d := e.Catalog.State.MustRelation(ref.Address)
					if len(d.Constraints.NotNull) != 1 || d.Constraints.NotNull[0].Name != "t_a_not_null" {
						t.Fatal(d)
					}
				} else {
					lockAwait(t, done, ids.ErrCatalogReplaced)
				}
			}
			lockEmpty(t, e)
		})
	}
}

func TestNotNullCancellationDuringScanAndRevisionExhaustion(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT)")
	values := strings.Repeat("(7),", 128)
	run(t, e, "INSERT INTO t VALUES"+strings.TrimSuffix(values, ","))
	ref := roleTableRef(t, e, "t")
	old := e.Catalog.State.Get(ref.Address)
	storage := e.Storage.Relations[ref.Address]
	ctx, cancel := context.WithCancel(context.Background())
	op, done, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	defer cancel()
	op.Ctx = &cancelAtErrCheck{Context: op.Context(), cancel: cancel, at: 25}
	_, err = e.Execute(op, statement(t, "ALTER TABLE t ALTER a SET NOT NULL"), 0, nil)
	if !errors.Is(err, context.Canceled) || e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage {
		t.Fatal(err)
	}
	maximum := *old.(*catalog.Relation)
	maximum.ObjectRevision = ids.DefinitionRevision(^uint64(0))
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err = stage.Replace(&maximum); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: ref.Epoch}); err != nil {
		t.Fatal(err)
	}
	_, err = testExecute(e, context.Background(), statement(t, "ALTER TABLE t ALTER a SET NOT NULL"), nil)
	if err == nil || err.Error() != "stuntgres: definition revision exhausted" || e.Catalog.State.Get(ref.Address) != &maximum || e.Storage.Relations[ref.Address] != storage {
		t.Fatal(err)
	}
}

func TestNotNullOwnerOnlyACLAndNamespace(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER reader")
	roleExecute(t, e, op, "CREATE USER schema_owner")
	roleExecute(t, e, op, "CREATE SCHEMA s AUTHORIZATION schema_owner")
	roleExecute(t, e, op, "CREATE TABLE s.t(a INT)")
	ref := roleTableRef(t, e, "s.t")
	old := e.Catalog.State.Get(ref.Address)
	owner := op.RoleState().CurrentUser
	reader, _ := e.Roles().Lookup("reader")
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	old = e.Catalog.State.Get(ref.Address)
	for _, role := range []string{"schema_owner", "reader"} {
		roleExecute(t, e, op, "SET SESSION AUTHORIZATION "+role)
		_, err := e.Execute(op, statement(t, "ALTER TABLE s.t ALTER a SET NOT NULL"), 0, nil)
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != "42501" || e.Catalog.State.Get(ref.Address) != old {
			t.Fatal(got, err)
		}
		if role == "schema_owner" && got.Message != "must be owner of table t" {
			t.Fatal(got)
		}
		if role == "reader" && got.Message != "permission denied for schema s" {
			t.Fatal(got)
		}
		roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	}
	// Public grants give DML authority, never ALTER ownership.
	roleExecute(t, e, op, "CREATE TABLE public.t(a INT)")
	ref = roleTableRef(t, e, "public.t")
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	roleExecute(t, e, op, "INSERT INTO public.t VALUES(7)")
	_, err := e.Execute(op, statement(t, "ALTER TABLE public.t ADD NOT NULL a"), 0, nil)
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "must be owner of table t"}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(got, err)
	}
	withPublicSchemaUnitOwner(t, e, "reader", func() {
		roleExecute(t, e, op, "CREATE TABLE owned(a INT)")
	})
	roleExecute(t, e, op, "ALTER TABLE owned ALTER a SET NOT NULL")
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	roleExecute(t, e, op, "ALTER TABLE public.t ALTER a SET NOT NULL")
}

func TestNotNullIntegratedAXLockTimeout(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ALTER a DROP NOT NULL", "ALTER TABLE t ADD NOT NULL a", "ALTER TABLE t DROP CONSTRAINT IF EXISTS missing", "ALTER TABLE t DROP CONSTRAINT a_nn", "ALTER TABLE t DROP CONSTRAINT a_nn RESTRICT", "ALTER TABLE t DROP CONSTRAINT a_nn CASCADE"} {
		t.Run(q, func(t *testing.T) {
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			dropping := strings.Contains(q, "DROP") && !strings.Contains(q, "missing")
			if dropping {
				sessionSQL(t, a, "CREATE TABLE t(a INT CONSTRAINT a_nn NOT NULL,b INT CONSTRAINT b_nn NOT NULL)")
			} else {
				sessionSQL(t, a, "CREATE TABLE t(a INT,b INT CONSTRAINT b_nn NOT NULL)")
			}
			sessionSQL(t, a, "INSERT INTO t VALUES(7,8)")
			sessionSQL(t, b, "SET lock_timeout='80ms'")
			ref := roleTableRef(t, e, "t")
			old := e.Catalog.State.Get(ref.Address)
			storage := e.Storage.Relations[ref.Address]
			ends, generation := storage.Ends, storage.Generation
			tx := transactionBegin(t, a)
			sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			done := waitsCommandAsync(e, b, context.Background(), q, false)
			lockPending(t, e, 1)
			select {
			case err := <-done:
				waitsDiagnostic(t, err, "55P03", "canceling statement due to lock timeout")
			case <-time.After(5 * time.Second):
				t.Fatal("NOT NULL AX waiter did not time out")
			}
			lockPending(t, e, 0)
			if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || storage.Ends != ends || storage.Generation != generation || len(storage.Versions) != 1 {
				t.Fatal("timed-out utility mutated descriptor/storage")
			}
			transactionEnd(t, a, tx, false)
			if dropping {
				notNullDropEnforcement(t, e)
			}
			notNullDropRows(t, e, 7, 1)
			sessionSQL(t, b, q)
			if dropping {
				sessionSQL(t, b, "UPDATE t SET a=NULL")
				sessionSQL(t, b, "INSERT INTO t VALUES(NULL,9)")
				notNullRemainingEnforcement(t, e)
			}
			lockEmpty(t, e)
		})
	}
}

func TestNotNullIntegratedAXWaitWithNowaitPeer(t *testing.T) {
	e := New()
	a, b, c := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(a INT)")
	ta, tc := transactionBegin(t, a), transactionBegin(t, c)
	sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := lockAsync(e, b, ctx, "ALTER TABLE t ALTER a SET NOT NULL", false)
	lockPending(t, e, 1)
	op, err := c.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = testExecuteFrame(e, op, statement(t, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT"), nil)
	op.Done()
	waitsDiagnostic(t, err, "55P03", `could not obtain lock on relation "t"`)
	lockPending(t, e, 1)
	transactionEnd(t, c, tc, false)
	cancel()
	lockAwait(t, done, context.Canceled)
	transactionEnd(t, a, ta, false)
	lockEmpty(t, e)
	sessionSQL(t, b, "ALTER TABLE t ALTER a SET NOT NULL")
	sessionSQL(t, b, "ALTER TABLE t DROP CONSTRAINT t_a_not_null")
	lockEmpty(t, e)
}
