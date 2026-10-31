package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// ForeignKeyJobs captures nothing when no staged relation takes part in a
// foreign key, and row-exclusive mode for a cascading child.
func TestStatementHostForeignKeyJobs(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(pid INT REFERENCES p ON UPDATE CASCADE)", "CREATE TABLE unrelated(id INT)", "INSERT INTO p VALUES(1)", "INSERT INTO c VALUES(1)"} {
		enginetest.SessionSQL(t, s, q)
	}
	scope := foreignActionUnitScope(t, e, s, "INSERT INTO unrelated VALUES(1)")
	jobs, modes, auth, err := scope.WriteHost().ForeignKeyJobs()
	if err != nil || len(jobs) != 0 || modes != nil || auth == nil {
		t.Fatal("irrelevant stage captured jobs", jobs, modes, auth, err)
	}
	scope.Finish(context.Canceled)

	scope = foreignActionUnitScope(t, e, s, "UPDATE p SET id=2")
	jobs, modes, auth, err = scope.WriteHost().ForeignKeyJobs()
	if err != nil {
		t.Fatal(err)
	}
	job := foreignActionUnitJob(t, jobs, "c")
	epoch := auth.Access().Catalog.Epoch()
	child := e.Catalog.Domain.Ref(epoch, job.Child.Address())
	parent := e.Catalog.Domain.Ref(epoch, job.Parent.Address())
	if len(jobs) != 1 || len(modes) != 2 || modes[child] != lock.RowExclusive || modes[parent] != lock.RowShare {
		t.Fatal("cascade modes", jobs, modes)
	}
}

// RetainedForeignKeyRows is empty under READ COMMITTED and borrows the
// snapshot's committed image under REPEATABLE READ.
func TestStatementHostRetainedForeignKeyRows(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(pid INT REFERENCES p ON UPDATE CASCADE)", "INSERT INTO p VALUES(1)", "INSERT INTO c VALUES(1)"} {
		enginetest.SessionSQL(t, s, q)
	}
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "c")
	scope := foreignActionUnitScope(t, e, s, "UPDATE p SET id=2")
	h := scope.WriteHost()
	current, err := h.ForeignKeyRows(ref, ids.ObjectAddress{})
	if err != nil {
		t.Fatal(err)
	}
	retained, err := h.RetainedForeignKeyRows(ref, current)
	if err != nil || retained.Storage != nil {
		t.Fatal("READ COMMITTED retained an image", retained.Storage, err)
	}
	scope.Finish(context.Canceled)

	enginetest.SessionSQL(t, s, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	enginetest.SessionSQL(t, s, "SELECT * FROM c")
	scope = foreignActionUnitScope(t, e, s, "UPDATE p SET id=3")
	h = scope.WriteHost()
	current, err = h.ForeignKeyRows(ref, ids.ObjectAddress{})
	if err != nil {
		t.Fatal(err)
	}
	retained, err = h.RetainedForeignKeyRows(ref, current)
	root := scope.Owner.Roots[ref.Address]
	if err != nil || retained.Storage != current.Storage || len(retained.Committed) != len(root.Versions) || retained.Frontier != scope.Owner.Frontier {
		t.Fatal("REPEATABLE READ image", retained, err)
	}
	var _ dml.FinishHost = h
}
