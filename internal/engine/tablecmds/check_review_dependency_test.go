package tablecmds_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Build a real CREATE candidate and stop at the incumbent validation/publication
// boundary. This intentionally exercises publication's fence independently of
// CREATE's relation holds; it is not an alternative execution implementation.
func checkReviewDependencyStage(t *testing.T, e *core.Engine) *core.CatalogStage {
	t.Helper()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	err = tablecmds.SchemaTableLocked(e, op, auth, stage, enginetest.Statement(t, "CREATE TABLE newly_dependent(a INT CONSTRAINT c CHECK(nextval('s')>0))").(*parser.CreateTable), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	return stage
}

func checkReviewDependencyValid(t *testing.T, e *core.Engine) {
	t.Helper()
	read := e.CatalogRead()
	for _, descriptor := range read.Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, c := range d.Constraints.Check {
			for _, dep := range c.Dependencies {
				if err := enginetest.ValidateObjectRef(e, read, dep.Ref); err != nil {
					t.Fatalf("orphan CHECK %s on %s: %v", c.Name, d.Name(), err)
				}
			}
		}
	}
	var dump strings.Builder
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal("unusable catalog", err)
	}
}

func TestCheckReviewDependencyPrivateDeletionThenPublication(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE SEQUENCE s")
	enginetest.SessionSQL(t, a, "CREATE SEQUENCE s_guard")
	enginetest.SessionSQL(t, a, "CREATE TABLE guard(a INT CONSTRAINT guard_c CHECK(nextval('s_guard')>0))")
	enginetest.SessionSQL(t, a, "INSERT INTO guard VALUES(9)")
	guardRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "guard")
	originalGuard := e.CatalogRead().Get(guardRef.Address)
	enginetest.SessionSQL(t, a, "CREATE TABLE sentinel(a INT)")
	enginetest.SessionSQL(t, a, "INSERT INTO sentinel VALUES(7)")
	enginetest.SessionSQL(t, a, "BEGIN")
	enginetest.SessionSQL(t, a, "DROP SEQUENCE s,s_guard CASCADE")
	stage := checkReviewDependencyStage(t, e)
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	_, publicationErr := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
	op, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, commitErr := e.Execute(op, enginetest.Statement(t, "COMMIT"), 0, nil)
	op.Done()
	if commitErr != nil {
		enginetest.SessionSQL(t, a, "ROLLBACK")
	}
	// Either operation may lose, or CASCADE may absorb the fresh edge. No
	// engine-specific conflict SQLSTATE is invented for this native fence.
	checkReviewDependencyValid(t, e)
	enginetest.TransactionRows(t, a, "SELECT a FROM sentinel", 7)
	read := e.CatalogRead()
	present := func(name string) bool {
		return len(read.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})) != 0
	}
	if present("s") != present("s_guard") {
		t.Fatal("sequence list partially published")
	}
	guard := read.MustRelation(guardRef.Address)
	if commitErr != nil {
		if !present("s") || guard != originalGuard {
			t.Fatal("failed deletion changed providers or existing CHECK")
		}
	} else if present("s") || len(guard.Constraints.Check) != 0 {
		t.Fatal("successful CASCADE failed to publish every removal")
	}
	enginetest.SessionSQL(t, a, "INSERT INTO guard VALUES(10)")
	enginetest.TransactionRows(t, a, "SELECT a FROM guard ORDER BY a", 9, 10)
	if publicationErr == nil {
		enginetest.SessionSQL(t, a, "INSERT INTO newly_dependent VALUES(1)")
		enginetest.TransactionRows(t, a, "SELECT a FROM newly_dependent", 1)
	} else {
		if refs := e.CatalogRead().Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "newly_dependent"}); len(refs) != 0 {
			t.Fatal("failed CREATE partially published", refs)
		}
	}
}

func TestCheckReviewDependencyDeletionBetweenValidationAndPublication(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE SEQUENCE s")
	enginetest.SessionSQL(t, a, "CREATE TABLE sentinel(a INT)")
	enginetest.SessionSQL(t, a, "INSERT INTO sentinel VALUES(7)")
	stage := checkReviewDependencyStage(t, e)
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, a, "DROP SEQUENCE s CASCADE")
	before := e.CatalogRead().State()
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err == nil {
		t.Fatal("CREATE published a validated CHECK after its provider was removed")
	}
	if e.CatalogRead().State() != before {
		t.Fatal("failed publication changed committed catalog")
	}
	checkReviewDependencyValid(t, e)
	enginetest.TransactionRows(t, a, "SELECT a FROM sentinel", 7)
	enginetest.SessionSQL(t, a, "CREATE TABLE newly_dependent(a INT CHECK(a=1))")
	enginetest.SessionSQL(t, a, "INSERT INTO newly_dependent VALUES(1)")
}

func TestCheckReviewDependencyRestrictComplete(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		name := "two-checks"
		if mixed {
			name = "check-and-view"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE SEQUENCE s")
			detail := "constraint c1 on table t depends on sequence s\nconstraint c2 on table t depends on sequence s"
			if mixed {
				enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT CONSTRAINT c1 CHECK(nextval('s')>0))")
				enginetest.SessionSQL(t, a, "CREATE VIEW v AS SELECT nextval('s') AS n")
				detail = "constraint c1 on table t depends on sequence s\nview v depends on sequence s"
			} else {
				enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT,CONSTRAINT c1 CHECK(nextval('s')>0),CONSTRAINT c2 CHECK(nextval('s')>0))")
			}
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
			before := e.CatalogRead().State()
			op, err := a.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			result, err := e.Execute(op, enginetest.Statement(t, "DROP SEQUENCE s"), 0, nil)
			op.Done()
			if result.Rows != nil || result.Count != 0 {
				t.Fatal("failed DROP exposed result", result)
			}
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			if e.CatalogRead().State() != before {
				t.Fatal("RESTRICT changed catalog")
			}
			checkReviewDependencyValid(t, e)
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(2)")
			enginetest.TransactionRows(t, a, "SELECT a FROM t ORDER BY a", 1, 2)
			if mixed {
				enginetest.TransactionRows(t, a, "SELECT n FROM v", 3)
			}
			enginetest.SessionSQL(t, a, "DROP SEQUENCE s CASCADE")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(3)")
			enginetest.TransactionRows(t, a, "SELECT a FROM t ORDER BY a", 1, 2, 3)
			read := e.CatalogRead()
			table := read.MustRelation(enginetest.CatalogRef(t, e, read, "t").Address)
			if len(table.Constraints.Check) != 0 {
				t.Fatal("CASCADE retained CHECK dependents")
			}
			for _, name := range []string{"s", "v"} {
				if refs := read.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name}); len(refs) != 0 {
					t.Fatal("CASCADE retained dependent/provider", name)
				}
			}
			checkReviewDependencyValid(t, e)
		})
	}
}

func TestCheckReviewDependencyRestrictReversedDeclaration(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE SEQUENCE s")
	enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT,CONSTRAINT c2 CHECK(nextval('s')>0),CONSTRAINT c1 CHECK(nextval('s')>0))")
	op, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Execute(op, enginetest.Statement(t, "DROP SEQUENCE s"), 0, nil)
	op.Done()
	if result.Rows != nil || result.Count != 0 {
		t.Fatal(result)
	}
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: "constraint c2 on table t depends on sequence s\nconstraint c1 on table t depends on sequence s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	checkReviewDependencyValid(t, e)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
}
