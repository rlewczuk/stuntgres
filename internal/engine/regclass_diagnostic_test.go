package engine

import (
	"context"
	"fmt"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// Exercise the actual private ACL and completed-row formatter seams, not a
// native SQL GRANT adapter or another copy of the public write scenarios.
func TestRegclassDiagnosticSnapshotACL(t *testing.T) {
	for _, whole := range []bool{false, true} {
		for _, zero := range []bool{false, true} {
			t.Run(fmt.Sprintf("whole=%v/zero=%v", whole, zero), func(t *testing.T) {
				e := New()
				admin, done := roleOperation(t, e, "postgres")
				defer done()
				for _, query := range []string{
					"CREATE ROLE rr_diag_reader LOGIN",
					"CREATE SCHEMA rr_diag_scope",
					`CREATE TABLE rr_diag_scope."Moved.Target"(v INT)`,
					"CREATE DOMAIN rr_diag_domain AS regclass",
					"CREATE DOMAIN rr_diag_array_domain AS regclass[]",
					"CREATE TABLE rr_diag(id INT,n INT,r regclass,d rr_diag_domain,a regclass[],da rr_diag_array_domain,CONSTRAINT rr_diag_bound CHECK(n>0))",
				} {
					roleExecute(t, e, admin, query)
				}
				ref := roleTableRef(t, e, "rr_diag")
				actor, _ := e.Roles().Lookup("rr_diag_reader")
				grant := func(attribute int32, privileges authorization.Privileges) {
					target := ref
					target.Address.SubID = attribute
					if err := e.SetObjectACL(context.Background(), target, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: privileges}}}); err != nil {
						t.Fatal(err)
					}
				}
				grant(2, authorization.UpdatePrivilege)
				for _, attribute := range []int32{1, 3, 5} {
					grant(attribute, authorization.SelectPrivilege)
				}
				if whole {
					grant(0, authorization.SelectPrivilege)
				}
				descriptor, ok := e.CatalogRead().Descriptor(ref.Address)
				if !ok {
					t.Fatal("missing diagnostic fixture")
				}
				relation := descriptor.(*catalog.Relation)
				op, finish := roleOperation(t, e, "rr_diag_reader")
				defer finish()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(nil)
				e.mu.Lock()
				err = e.captureCheckCommentSnapshotLocked(scope, relation)
				e.mu.Unlock()
				if err != nil || scope.output.Regclass != scope || scope.commentCatalog.State() == nil {
					t.Fatal("missing captured output demand", err)
				}
				target := roleTableRef(t, e, `rr_diag_scope."Moved.Target"`)
				oid := uint32(target.Address.ObjectID)
				if zero {
					oid = 0
				}
				inputs := []string{"2", "0", strconv.FormatUint(uint64(oid), 10), strconv.FormatUint(uint64(oid), 10), fmt.Sprintf("{%d,NULL}", oid), fmt.Sprintf("{%d,NULL}", oid)}
				row := make([]sqltype.Value, len(inputs))
				for i, input := range inputs {
					row[i], err = relation.Columns[i].Usage.Type.Input(context.Background(), input, -1)
					if err != nil {
						t.Fatal(i, err)
					}
				}
				// Formatting must keep the capture even if a catalog publication
				// changes the live spelling before this function runs.
				roleExecute(t, e, admin, `ALTER TABLE rr_diag_scope."Moved.Target" RENAME TO "Later.Target"`)
				scalar, array := `rr_diag_scope."Moved.Target"`, `{"rr_diag_scope.\"Moved.Target\"",NULL}`
				if zero {
					scalar, array = "-", "{-,NULL}"
				}
				detail := fmt.Sprintf("Failing row contains (id, n, r, a) = (2, 0, %s, %s).", scalar, array)
				if whole {
					detail = fmt.Sprintf("Failing row contains (2, 0, %s, %s, %s, %s).", scalar, scalar, array, array)
				}
				bound := binding{ref: ref, relation: relation, targets: []int{1}}
				for repeat := 0; repeat != 2; repeat++ {
					err = e.rowConstraintError(scope, bound, "public", "", "rr_diag_bound", row)
					nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23514", Message: `new row for relation "rr_diag" violates check constraint "rr_diag_bound"`, Detail: detail, Schema: "public", Table: "rr_diag", Constraint: "rr_diag_bound"})
				}
				if len(e.storage.Relations[ref.Address].Versions) != 0 {
					t.Fatal("formatter changed stored rows")
				}
				if got := scope.Finish(err); got != err {
					t.Fatal("statement completion changed diagnostic", got, err)
				}
			})
		}
	}
}

func TestRegclassPrimitiveDiagnosticAvoidsSnapshot(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, admin, "CREATE TABLE rr_primitive(n INT CHECK(n>0))")
	ref := roleTableRef(t, e, "rr_primitive")
	descriptor, _ := e.CatalogRead().Descriptor(ref.Address)
	scope, err := e.StartStatement(admin)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	e.mu.Lock()
	err = e.captureCheckCommentSnapshotLocked(scope, descriptor.(*catalog.Relation))
	e.mu.Unlock()
	if err != nil || scope.commentCatalog.State() != nil || scope.output.Regclass != nil {
		t.Fatal("primitive row allocated output snapshot", err)
	}
}
