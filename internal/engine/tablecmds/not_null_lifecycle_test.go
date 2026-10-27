package tablecmds_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func TestNotNullAXWaitCancellationAndEnd(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ALTER a DROP NOT NULL", "ALTER TABLE t DROP CONSTRAINT a_nn", "ALTER TABLE t DROP CONSTRAINT a_nn RESTRICT", "ALTER TABLE t DROP CONSTRAINT a_nn CASCADE"} {
		for _, ending := range []bool{false, true} {
			t.Run(q+map[bool]string{false: "/cancel", true: "/end"}[ending], func(t *testing.T) {
				e := engine.New()
				a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				dropping := strings.Contains(q, "DROP")
				if dropping {
					enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT CONSTRAINT a_nn NOT NULL,b INT CONSTRAINT b_nn NOT NULL)")
				} else {
					enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT,b INT CONSTRAINT b_nn NOT NULL)")
				}
				enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(7,8)")
				ref := enginetest.RoleTableRef(t, e, "t")
				old, storage := e.Catalog.State.Get(ref.Address), e.Storage.Relations[ref.Address]
				ends, generation := storage.Ends, storage.Generation
				tx := enginetest.TransactionBegin(t, a)
				enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := enginetest.LockAsync(e, b, ctx, q, false)
				enginetest.LockPending(t, e, 1)
				if ending {
					b.RequestEnd(session.CleanClose)
				} else {
					cancel()
				}
				enginetest.LockAwait(t, done, context.Canceled)
				enginetest.LockPending(t, e, 0)
				if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || storage.Ends != ends || storage.Generation != generation || len(storage.Versions) != 1 {
					t.Fatal("canceled waiter changed schema/storage")
				}
				enginetest.TransactionEnd(t, a, tx, false)
				enginetest.LockEmpty(t, e)
				if dropping {
					notNullDropEnforcement(t, e)
				}
				notNullDropRows(t, e, 7, 1)
				c := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, c, q)
				if dropping {
					enginetest.SessionSQL(t, c, "UPDATE t SET a=NULL")
					enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(NULL,9)")
					notNullRemainingEnforcement(t, e)
				}
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func notNullDropEnforcement(t *testing.T, e *core.Engine) {
	t.Helper()
	for _, q := range []string{"INSERT INTO t VALUES(NULL,8)", "UPDATE t SET a=NULL"} {
		_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, q), nil)
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != "23502" || got.Column != "a" {
			t.Fatal(q, err)
		}
	}
	notNullRemainingEnforcement(t, e)
}

func notNullRemainingEnforcement(t *testing.T, e *core.Engine) {
	t.Helper()
	for _, q := range []string{"INSERT INTO t VALUES(7,NULL)", "UPDATE t SET b=NULL"} {
		_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, q), nil)
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != "23502" || got.Column != "b" {
			t.Fatal(q, err)
		}
	}
}

func notNullDropRows(t *testing.T, e *core.Engine, a int64, count int) {
	t.Helper()
	want := make([][]driver.Value, count)
	for i := range want {
		want[i] = []driver.Value{a, int64(8)}
	}
	if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT * FROM t")); !reflect.DeepEqual(got, want) {
		t.Fatal("failed alteration changed rows", got, want)
	}
}

func TestNotNullAXGrantTimeDescriptorAndRetargetFence(t *testing.T) {
	for _, kind := range []string{"revision", "shadow", "reset", "restore", "recreate"} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE SCHEMA shadow")
			enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT)")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(7)")
			if kind == "shadow" {
				enginetest.SessionSQL(t, b, "SET search_path TO shadow,public")
			}
			ref := enginetest.RoleTableRef(t, e, "t")
			tx := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := enginetest.LockAsync(e, b, ctx, "ALTER TABLE t ADD NOT NULL a", false)
			enginetest.LockPending(t, e, 1)
			switch kind {
			case "revision":
				stage, ref, _ := notNullMetadataStage(t, e)
				prepared, err := e.ValidateCatalogStage(context.Background(), stage)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: ref.Epoch}); err != nil {
					t.Fatal(err)
				}
			case "shadow":
				enginetest.Run(t, e, "CREATE TABLE shadow.t(a INT)")
			case "reset":
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			case "restore":
				if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE t(a INT);INSERT INTO t VALUES(91)")); err != nil {
					t.Fatal(err)
				}
			case "recreate":
				enginetest.SessionSQL(t, a, "DROP TABLE t")
				enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT)")
			}
			if kind == "reset" || kind == "restore" {
				enginetest.LockAwait(t, done, ids.ErrCatalogReplaced)
				enginetest.TransactionEnd(t, a, tx, false)
			} else {
				enginetest.TransactionEnd(t, a, tx, kind == "recreate")
				if kind == "revision" {
					enginetest.LockAwait(t, done, nil)
					d := e.Catalog.State.MustRelation(ref.Address)
					if len(d.Constraints.NotNull) != 1 || d.Constraints.NotNull[0].Name != "t_a_not_null" {
						t.Fatal(d)
					}
				} else {
					enginetest.LockAwait(t, done, ids.ErrCatalogReplaced)
				}
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestNotNullCancellationDuringScanAndRevisionExhaustion(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(a INT)")
	values := strings.Repeat("(7),", 128)
	enginetest.Run(t, e, "INSERT INTO t VALUES"+strings.TrimSuffix(values, ","))
	ref := enginetest.RoleTableRef(t, e, "t")
	old := e.Catalog.State.Get(ref.Address)
	storage := e.Storage.Relations[ref.Address]
	ctx, cancel := context.WithCancel(context.Background())
	op, done, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	defer cancel()
	op.Ctx = &cancelAtErrCheck{Context: op.Context(), cancel: cancel, at: 25}
	_, err = e.Execute(op, enginetest.Statement(t, "ALTER TABLE t ALTER a SET NOT NULL"), 0, nil)
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
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: ref.Epoch}); err != nil {
		t.Fatal(err)
	}
	_, err = enginetest.Execute(e, context.Background(), enginetest.Statement(t, "ALTER TABLE t ALTER a SET NOT NULL"), nil)
	if err == nil || err.Error() != "stuntgres: definition revision exhausted" || e.Catalog.State.Get(ref.Address) != &maximum || e.Storage.Relations[ref.Address] != storage {
		t.Fatal(err)
	}
}

func TestNotNullOwnerOnlyACLAndNamespace(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER reader")
	enginetest.RoleExecute(t, e, op, "CREATE USER schema_owner")
	enginetest.RoleExecute(t, e, op, "CREATE SCHEMA s AUTHORIZATION schema_owner")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE s.t(a INT)")
	ref := enginetest.RoleTableRef(t, e, "s.t")
	old := e.Catalog.State.Get(ref.Address)
	owner := op.RoleState().CurrentUser
	reader, _ := e.Roles().Lookup("reader")
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	old = e.Catalog.State.Get(ref.Address)
	for _, role := range []string{"schema_owner", "reader"} {
		enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION "+role)
		_, err := e.Execute(op, enginetest.Statement(t, "ALTER TABLE s.t ALTER a SET NOT NULL"), 0, nil)
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
		enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	}
	// Public grants give DML authority, never ALTER ownership.
	enginetest.RoleExecute(t, e, op, "CREATE TABLE public.t(a INT)")
	ref = enginetest.RoleTableRef(t, e, "public.t")
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	enginetest.RoleExecute(t, e, op, "INSERT INTO public.t VALUES(7)")
	_, err := e.Execute(op, enginetest.Statement(t, "ALTER TABLE public.t ADD NOT NULL a"), 0, nil)
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "must be owner of table t"}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(got, err)
	}
	withPublicSchemaUnitOwner(t, e, "reader", func() {
		enginetest.RoleExecute(t, e, op, "CREATE TABLE owned(a INT)")
	})
	enginetest.RoleExecute(t, e, op, "ALTER TABLE owned ALTER a SET NOT NULL")
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	enginetest.RoleExecute(t, e, op, "ALTER TABLE public.t ALTER a SET NOT NULL")
}

func TestNotNullIntegratedAXLockTimeout(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ALTER a DROP NOT NULL", "ALTER TABLE t ADD NOT NULL a", "ALTER TABLE t DROP CONSTRAINT IF EXISTS missing", "ALTER TABLE t DROP CONSTRAINT a_nn", "ALTER TABLE t DROP CONSTRAINT a_nn RESTRICT", "ALTER TABLE t DROP CONSTRAINT a_nn CASCADE"} {
		t.Run(q, func(t *testing.T) {
			e := engine.New()
			a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			dropping := strings.Contains(q, "DROP") && !strings.Contains(q, "missing")
			if dropping {
				enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT CONSTRAINT a_nn NOT NULL,b INT CONSTRAINT b_nn NOT NULL)")
			} else {
				enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT,b INT CONSTRAINT b_nn NOT NULL)")
			}
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(7,8)")
			ref := enginetest.RoleTableRef(t, e, "t")
			old := e.Catalog.State.Get(ref.Address)
			storage := e.Storage.Relations[ref.Address]
			ends, generation := storage.Ends, storage.Generation
			tx := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			// Prove admission without racing the short timeout's expiry.
			waitContext, cancel := context.WithCancel(context.Background())
			waiting := waitsCommandAsync(e, b, waitContext, q, false)
			joined := false
			defer func() {
				cancel()
				if !joined {
					enginetest.LockAwait(t, waiting, context.Canceled)
				}
			}()
			enginetest.LockPending(t, e, 1)
			cancel()
			enginetest.LockAwait(t, waiting, context.Canceled)
			joined = true
			enginetest.LockPending(t, e, 0)
			enginetest.SessionSQL(t, b, "SET lock_timeout='80ms'")
			done := waitsCommandAsync(e, b, context.Background(), q, false)
			select {
			case err := <-done:
				waitsDiagnostic(t, err, "55P03", "canceling statement due to lock timeout")
			case <-time.After(5 * time.Second):
				t.Fatal("NOT NULL AX waiter did not time out")
			}
			enginetest.LockPending(t, e, 0)
			if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || storage.Ends != ends || storage.Generation != generation || len(storage.Versions) != 1 {
				t.Fatal("timed-out utility mutated descriptor/storage")
			}
			enginetest.TransactionEnd(t, a, tx, false)
			if dropping {
				notNullDropEnforcement(t, e)
			}
			notNullDropRows(t, e, 7, 1)
			enginetest.SessionSQL(t, b, q)
			if dropping {
				enginetest.SessionSQL(t, b, "UPDATE t SET a=NULL")
				enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(NULL,9)")
				notNullRemainingEnforcement(t, e)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestNotNullIntegratedAXWaitWithNowaitPeer(t *testing.T) {
	e := engine.New()
	a, b, c := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT)")
	ta, tc := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, c)
	enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := enginetest.LockAsync(e, b, ctx, "ALTER TABLE t ALTER a SET NOT NULL", false)
	enginetest.LockPending(t, e, 1)
	op, err := c.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = enginetest.ExecuteFrame(e, op, enginetest.Statement(t, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT"), nil)
	op.Done()
	waitsDiagnostic(t, err, "55P03", `could not obtain lock on relation "t"`)
	enginetest.LockPending(t, e, 1)
	enginetest.TransactionEnd(t, c, tc, false)
	cancel()
	enginetest.LockAwait(t, done, context.Canceled)
	enginetest.TransactionEnd(t, a, ta, false)
	enginetest.LockEmpty(t, e)
	enginetest.SessionSQL(t, b, "ALTER TABLE t ALTER a SET NOT NULL")
	enginetest.SessionSQL(t, b, "ALTER TABLE t DROP CONSTRAINT t_a_not_null")
	enginetest.LockEmpty(t, e)
}

func waitsDiagnostic(t *testing.T, err error, code, message string, position ...string) {
	t.Helper()
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if len(position) != 0 {
		want.Position = position[0]
	}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("diagnostic = %#v (%v), want %#v", got, err, want)
	}
}
