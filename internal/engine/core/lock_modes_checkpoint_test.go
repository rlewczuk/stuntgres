package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

func TestLockModesRevokedOwner(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER lockowner")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION lockowner")
	withPublicSchemaUnitOwner(t, e, "lockowner", func() {
		enginetest.RoleExecute(t, e, op, "CREATE TABLE owned(v INT)")
	})
	ref := enginetest.RoleTableRef(t, e, "owned")
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	tx, err := e.Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, enginetest.Statement(t, "LOCK owned IN ACCESS SHARE MODE"), 0, nil)
	lockModesEngineDenied(t, err, "permission denied for table owned")
	if _, err = e.Rollback(op, tx); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	tx, err = e.Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "LOCK owned")
	if _, err = e.Rollback(op, tx); err != nil {
		t.Fatal(err)
	}
	enginetest.LockEmpty(t, e)
}
