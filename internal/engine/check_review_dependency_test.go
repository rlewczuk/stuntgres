package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Build a real CREATE candidate and stop at the incumbent validation/publication
// boundary. This intentionally exercises publication's fence independently of
// CREATE's relation holds; it is not an alternative execution implementation.
func checkReviewDependencyStage(t *testing.T, e *Engine) *CatalogStage {
	t.Helper()
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	err = e.schemaTableLocked(op, auth, stage, statement(t, "CREATE TABLE newly_dependent(a INT CONSTRAINT c CHECK(nextval('s')>0))").(*parser.CreateTable), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	return stage
}

func checkReviewDependencyValid(t *testing.T, e *Engine) {
	t.Helper()
	read := e.CatalogRead()
	for _, descriptor := range read.Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, c := range d.Constraints.Check {
			for _, dep := range c.Dependencies {
				if err := e.ValidateObjectRef(read, dep.Ref); err != nil {
					t.Fatalf("orphan CHECK %s on %s: %v", c.Name, d.Name(), err)
				}
			}
		}
	}
	var dump strings.Builder
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal("unusable catalog", err)
	}
}

func TestCheckReviewDependencyPrivateDeletionThenPublication(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE SEQUENCE s")
	sessionSQL(t, a, "CREATE SEQUENCE s_guard")
	sessionSQL(t, a, "CREATE TABLE guard(a INT CONSTRAINT guard_c CHECK(nextval('s_guard')>0))")
	sessionSQL(t, a, "INSERT INTO guard VALUES(9)")
	guardRef := catalogRef(t, e, e.CatalogRead(), "guard")
	originalGuard := e.CatalogRead().Get(guardRef.Address)
	sessionSQL(t, a, "CREATE TABLE sentinel(a INT)")
	sessionSQL(t, a, "INSERT INTO sentinel VALUES(7)")
	sessionSQL(t, a, "BEGIN")
	sessionSQL(t, a, "DROP SEQUENCE s,s_guard CASCADE")
	stage := checkReviewDependencyStage(t, e)
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	_, publicationErr := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	op, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, commitErr := e.Execute(op, statement(t, "COMMIT"), 0, nil)
	op.Done()
	if commitErr != nil {
		sessionSQL(t, a, "ROLLBACK")
	}
	// Either operation may lose, or CASCADE may absorb the fresh edge. No
	// engine-specific conflict SQLSTATE is invented for this native fence.
	checkReviewDependencyValid(t, e)
	transactionRows(t, a, "SELECT a FROM sentinel", 7)
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
	sessionSQL(t, a, "INSERT INTO guard VALUES(10)")
	transactionRows(t, a, "SELECT a FROM guard ORDER BY a", 9, 10)
	if publicationErr == nil {
		sessionSQL(t, a, "INSERT INTO newly_dependent VALUES(1)")
		transactionRows(t, a, "SELECT a FROM newly_dependent", 1)
	} else {
		if refs := e.CatalogRead().Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "newly_dependent"}); len(refs) != 0 {
			t.Fatal("failed CREATE partially published", refs)
		}
	}
}

func TestCheckReviewDependencyDeletionBetweenValidationAndPublication(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE SEQUENCE s")
	sessionSQL(t, a, "CREATE TABLE sentinel(a INT)")
	sessionSQL(t, a, "INSERT INTO sentinel VALUES(7)")
	stage := checkReviewDependencyStage(t, e)
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	sessionSQL(t, a, "DROP SEQUENCE s CASCADE")
	before := e.CatalogRead().State()
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err == nil {
		t.Fatal("CREATE published a validated CHECK after its provider was removed")
	}
	if e.CatalogRead().State() != before {
		t.Fatal("failed publication changed committed catalog")
	}
	checkReviewDependencyValid(t, e)
	transactionRows(t, a, "SELECT a FROM sentinel", 7)
	sessionSQL(t, a, "CREATE TABLE newly_dependent(a INT CHECK(a=1))")
	sessionSQL(t, a, "INSERT INTO newly_dependent VALUES(1)")
}

func TestCheckReviewDependencyRestrictComplete(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		name := "two-checks"
		if mixed {
			name = "check-and-view"
		}
		t.Run(name, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE SEQUENCE s")
			detail := "constraint c1 on table t depends on sequence s\nconstraint c2 on table t depends on sequence s"
			if mixed {
				sessionSQL(t, a, "CREATE TABLE t(a INT CONSTRAINT c1 CHECK(nextval('s')>0))")
				sessionSQL(t, a, "CREATE VIEW v AS SELECT nextval('s') AS n")
				detail = "constraint c1 on table t depends on sequence s\nview v depends on sequence s"
			} else {
				sessionSQL(t, a, "CREATE TABLE t(a INT,CONSTRAINT c1 CHECK(nextval('s')>0),CONSTRAINT c2 CHECK(nextval('s')>0))")
			}
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			before := e.CatalogRead().State()
			op, err := a.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			result, err := e.Execute(op, statement(t, "DROP SEQUENCE s"), 0, nil)
			op.Done()
			if result.Rows != nil || result.Count != 0 {
				t.Fatal("failed DROP exposed result", result)
			}
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			if e.CatalogRead().State() != before {
				t.Fatal("RESTRICT changed catalog")
			}
			checkReviewDependencyValid(t, e)
			sessionSQL(t, a, "INSERT INTO t VALUES(2)")
			transactionRows(t, a, "SELECT a FROM t ORDER BY a", 1, 2)
			if mixed {
				transactionRows(t, a, "SELECT n FROM v", 3)
			}
			sessionSQL(t, a, "DROP SEQUENCE s CASCADE")
			sessionSQL(t, a, "INSERT INTO t VALUES(3)")
			transactionRows(t, a, "SELECT a FROM t ORDER BY a", 1, 2, 3)
			read := e.CatalogRead()
			table := read.MustRelation(catalogRef(t, e, read, "t").Address)
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
	e := New()
	a := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE SEQUENCE s")
	sessionSQL(t, a, "CREATE TABLE t(a INT,CONSTRAINT c2 CHECK(nextval('s')>0),CONSTRAINT c1 CHECK(nextval('s')>0))")
	op, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Execute(op, statement(t, "DROP SEQUENCE s"), 0, nil)
	op.Done()
	if result.Rows != nil || result.Count != 0 {
		t.Fatal(result)
	}
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: "constraint c2 on table t depends on sequence s\nconstraint c1 on table t depends on sequence s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	checkReviewDependencyValid(t, e)
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
}
