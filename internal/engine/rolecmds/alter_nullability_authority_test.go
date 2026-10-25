package rolecmds_test

import (
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/sqlstate"
)

// This package-level lifetime witness changes the actor/revision through the
// internal executor after ALTER has finished, then inspects its captured
// authorization and final catalog/storage publication directly.
func TestAlterNullabilityAuthorizationLifetime(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE ROLE nn_nonowner")
	enginetest.SessionSQL(t, s, "CREATE TABLE t(a INT)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(7)")
	table := enginetest.RoleTableRef(t, e, "t")
	store := e.Storage.Relations[table.Address]
	tx := enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "ALTER TABLE t ALTER a SET NOT NULL")
	op := enginetest.TransactionOperation(t, s)
	s.Mu.Lock()
	before := s.Authorization.Revision
	s.Mu.Unlock()
	if _, err := rolecmds.ExecuteRole(e, op, enginetest.Statement(t, "SET SESSION AUTHORIZATION nn_nonowner"), 0, false); err != nil {
		op.Done()
		t.Fatal(err)
	}
	role, exists := e.Roles().Lookup("nn_nonowner")
	state := op.RoleState()
	s.Mu.Lock()
	after := s.Authorization.Revision
	s.Mu.Unlock()
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	denied := auth.Check(access.Requirement{Object: table, OwnerOnly: true})
	op.Done()
	if !exists || state.CurrentUser != role.Ref || state.SessionUser != role.Ref || after <= before || denied == nil {
		t.Fatal("authorization capability did not select an unauthorized nonowner", state, before, after, denied)
	}
	var ownership *sqlstate.Error
	wantOwnership := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "must be owner of table t"}
	if !errors.As(denied, &ownership) || *ownership != wantOwnership {
		t.Fatal(denied, ownership)
	}
	result := enginetest.TransactionEnd(t, s, tx, true)
	if !result.Committed {
		t.Fatal("authorized metadata lost after actor mutation")
	}
	current := e.Catalog.State.MustRelation(table.Address)
	if current.Columns[0].Nullable || e.Storage.Relations[table.Address] != store {
		t.Fatal("metadata/storage publication changed")
	}
	op = enginetest.TransactionOperation(t, s)
	if _, err = rolecmds.ExecuteRole(e, op, enginetest.Statement(t, "RESET SESSION AUTHORIZATION"), 0, false); err != nil {
		op.Done()
		t.Fatal(err)
	}
	op.Done()
	if got := enginetest.Tuples(enginetest.SessionSQL(t, s, "SELECT * FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
		t.Fatal(got)
	}
	op = enginetest.TransactionOperation(t, s)
	_, err = e.Execute(op, enginetest.Statement(t, "INSERT INTO t VALUES(NULL)"), 0, nil)
	op.Done()
	var diagnostic *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"}
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatal(err, diagnostic)
	}
}
