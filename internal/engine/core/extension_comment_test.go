package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestExtensionCommentDescriptorCopies(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION fuzzystrmatch VERSION '1.1'")
	enginetest.RoleExecute(t, e, op, "COMMENT ON EXTENSION fuzzystrmatch IS 'old 雪'")
	before := e.CatalogRead()
	ext := before.MustExtension(before.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "fuzzystrmatch"}))
	clone := catalog.CloneState(before.State())
	if clone.MustExtension(ext.Address()) != ext {
		t.Fatal("clone copied immutable header")
	}
	// Real remapping must preserve text while rebuilding installation provenance.
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 930000}, ext.TargetNamespace: {ClassID: ids.NamespaceClassID, ObjectID: 930001}}
	for i, a := range ext.Members {
		mapping[a] = ids.ObjectAddress{ClassID: a.ClassID, ObjectID: ids.OID(930100 + i)}
	}
	mapped := extension.DescriptorRemapper(mapping)(ext).(*catalog.Extension)
	if mapped.Comment != ext.Comment || mapped.Address() != mapping[ext.Address()] || !mapped.Installation.Valid() || mapped.Installation == ext.Installation || mapped.Installation.Implementation != ext.Installation.Implementation || mapped.Installation.Cohort != mapped.Address() {
		t.Fatal("address remap lost comment or installation authority")
	}
	enginetest.RoleExecute(t, e, op, "CREATE ROLE comment_copy_owner")
	owner, _ := e.Roles().Lookup("comment_copy_owner")
	principal := func(p authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		if p == ext.Owner {
			return owner.Ref, nil
		}
		return p, nil
	}
	if err := catalog.RemapPrincipals(clone, catalog.PrincipalRemap{Principal: principal, SeededOwner: func(p authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) { return principal(p) }, OperatorSeeded: func(*catalog.Operator) bool { return false }}); err != nil {
		t.Fatal(err)
	}
	remapped := clone.MustExtension(ext.Address())
	if remapped == ext || remapped.Owner != owner.Ref || remapped.Comment != "old 雪" || remapped.Installation != ext.Installation {
		t.Fatal("principal remap lost immutable metadata")
	}
	enginetest.RoleExecute(t, e, op, "ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'")
	updated := e.CatalogRead().MustExtension(ext.Address())
	if updated.Comment != "old 雪" || updated.Version != "1.2" || ext.Version != "1.1" {
		t.Fatal("update changed comment or captured header")
	}
	enginetest.RoleExecute(t, e, op, "COMMENT ON EXTENSION fuzzystrmatch IS 'new'")
	current := e.CatalogRead().MustExtension(ext.Address())
	if current == updated || current.Comment != "new" || current.Revision() == updated.Revision() || current.Installation != updated.Installation || !reflect.DeepEqual(current.Members, updated.Members) || ext.Comment != "old 雪" || updated.Comment != "old 雪" || clone.MustExtension(ext.Address()).Comment != "old 雪" {
		t.Fatal("replacement mutated immutable comment/installation backing")
	}
}

func TestExtensionCommentPublicationCancellation(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			e := engine.New()
			worker := sessionStart(t, e, nil)
			sessionSQL(t, worker, "CREATE EXTENSION fuzzystrmatch")
			sessionSQL(t, worker, "CREATE TABLE comment_cancel_sentinel(v INT)")
			sessionSQL(t, worker, "INSERT INTO comment_cancel_sentinel VALUES(77)")
			for _, literal := range []string{"'old 雪'", "NULL"} {
				sessionSQL(t, worker, "COMMENT ON EXTENSION fuzzystrmatch IS "+literal)
				before := e.CatalogRead()
				ext := before.MustExtension(before.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "fuzzystrmatch"}))
				if explicit {
					sessionSQL(t, worker, "BEGIN")
				}
				ctx, cancel := context.WithCancel(context.Background())
				scope, op := cteDMLUnitScope(t, worker, ctx)
				// Exercise the existing statement-private publication seam: utilities with
				// a private journal owe their real atomic publication to Finish.
				scope.Catalog = session.NewJournal()
				result, err := scope.Execute(enginetest.Statement(t, "COMMENT ON EXTENSION fuzzystrmatch IS 'unpublished'"), 0, nil)
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				if result.Rows != nil {
					result.Rows.Close()
					cancel()
					t.Fatal("COMMENT returned rows")
				}
				if scope.Completed || e.CatalogRead().Get(ext.Address()) != ext {
					cancel()
					t.Fatal("COMMENT published before Finish")
				}
				if scope.Catalog == nil {
					cancel()
					t.Fatal("COMMENT did not stage a private journal")
				}
				candidate, ok := scope.Catalog.Descriptors[ext.Address()].(*catalog.Extension)
				if !ok || candidate == ext || candidate.Comment != "unpublished" || candidate.Installation != ext.Installation {
					cancel()
					t.Fatal("missing real immutable staged replacement")
				}
				cancel()
				if err := scope.Finish(nil); !errors.Is(err, context.Canceled) {
					t.Fatal("post-stage Finish", err)
				}
				op.Done()
				after := e.CatalogRead()
				if after.State() != before.State() || after.Get(ext.Address()) != ext || ext.Installation != after.MustExtension(ext.Address()).Installation {
					t.Fatal("canceled Finish changed committed descriptor/root")
				}
				if explicit {
					sessionSQL(t, worker, "ROLLBACK")
				}
				transactionRows(t, worker, "SELECT v FROM comment_cancel_sentinel", 77)
				enginetest.LockEmpty(t, e)
			}
		})
	}
}
