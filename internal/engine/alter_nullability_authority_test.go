package engine

import (
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/sqlstate"
	"testing"
)

// This package-level lifetime witness changes the actor/revision through the
// internal executor after ALTER has finished, then inspects its captured
// authorization and final catalog/storage publication directly.
func TestAlterNullabilityAuthorizationLifetime(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE ROLE nn_nonowner")
	sessionSQL(t, s, "CREATE TABLE t(a INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(7)")
	table := roleTableRef(t, e, "t")
	store := e.storage.Relations[table.Address]
	tx := transactionBegin(t, s)
	sessionSQL(t, s, "ALTER TABLE t ALTER a SET NOT NULL")
	op := transactionOperation(t, s)
	s.Mu.Lock()
	before := s.Authorization.Revision
	s.Mu.Unlock()
	if _, err := e.executeRole(op, statement(t, "SET SESSION AUTHORIZATION nn_nonowner"), 0, false); err != nil {
		op.Done()
		t.Fatal(err)
	}
	role, exists := e.Roles().Lookup("nn_nonowner")
	state := op.RoleState()
	s.Mu.Lock()
	after := s.Authorization.Revision
	s.Mu.Unlock()
	auth, err := operationAuthorization(op)
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
	result := transactionEnd(t, s, tx, true)
	if !result.Committed {
		t.Fatal("authorized metadata lost after actor mutation")
	}
	current := e.catalog.State.MustRelation(table.Address)
	if current.Columns[0].Nullable || e.storage.Relations[table.Address] != store {
		t.Fatal("metadata/storage publication changed")
	}
	op = transactionOperation(t, s)
	if _, err = e.executeRole(op, statement(t, "RESET SESSION AUTHORIZATION"), 0, false); err != nil {
		op.Done()
		t.Fatal(err)
	}
	op.Done()
	if got := tuples(sessionSQL(t, s, "SELECT * FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
		t.Fatal(got)
	}
	op = transactionOperation(t, s)
	_, err = e.Execute(op, statement(t, "INSERT INTO t VALUES(NULL)"), 0, nil)
	op.Done()
	var diagnostic *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"}
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatal(err, diagnostic)
	}
}
