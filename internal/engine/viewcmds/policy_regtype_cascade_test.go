package viewcmds_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// The closure discovers represented owned types through a relation root. Its
// captured provider targets must also reach pruning, even when physical removal
// already staged the owned type and no separate type-removal edge is emitted.
func TestRLSPolicyRegtypeCascadeRetirement(t *testing.T) {
	for _, represented := range []string{"provider", "provider[]"} {
		t.Run(represented, func(t *testing.T) {
			for _, clause := range []string{"USING", "WITH CHECK"} {
				t.Run(clause, func(t *testing.T) {
					e, op, done, _ := policyUnitFixture(t)
					defer done()
					enginetest.RoleExecute(t, e, op, "INSERT INTO t VALUES(7,TRUE)")
					enginetest.RoleExecute(t, e, op, "CREATE TABLE provider(id INTEGER)")
					enginetest.RoleExecute(t, e, op, "CREATE POLICY p ON t "+clause+"('"+represented+"'::regtype IS NOT NULL)")
					enginetest.RoleExecute(t, e, op, "CREATE POLICY retained ON t USING(NULL::regtype IS NULL) WITH CHECK('integer'::regtype IS NOT NULL)")
					base := e.CatalogRead()
					ownerRef := enginetest.RoleTableRef(t, e, "t")
					old := base.MustRelation(ownerRef.Address)
					providerRef := enginetest.RoleTableRef(t, e, "provider")
					provider := base.MustRelation(providerRef.Address)
					rowAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(provider.RowType)}
					row := base.MustType(rowAddress)
					arrayAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)}
					storage := e.Storage.Relations[old.Address()]
					retained := old.Security.Policies[1]
					stage := e.BeginCatalogStage(base)
					defer e.DiscardCatalogStage(stage)
					if err := stage.Remove(providerRef); err != nil {
						t.Fatal(err)
					}
					if err := engine.StageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{providerRef.Address}, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil {
						t.Fatal(err)
					}
					next, ok := stage.Get(old.Address()).(*catalog.Relation)
					if !ok || len(next.Security.Policies) != 1 || !reflect.DeepEqual(next.Security.Policies[0], retained) || next.Revision() <= old.Revision() {
						t.Fatal("wrong surviving policy collection", next)
					}
					if _, removed := stage.Removal(old.Address()); removed {
						t.Fatal("policy owner became a retirement target")
					}
					if want := []string{"drop cascades to policy p on table t"}; !reflect.DeepEqual(stage.DropCascadeNames, want) {
						t.Fatal("wrong captured notices", stage.DropCascadeNames)
					}
					view := e.StageRead(stage)
					// Validation must still reject the unpruned old condition; the
					// fix changes retirement authority, not validator strictness.
					condition := stored.PolicyOf(old.Security.Policies[0].Using)
					if clause == "WITH CHECK" {
						condition = stored.PolicyOf(old.Security.Policies[0].Check)
					}
					if err := stored.ValidatePolicy(view, next, condition); err == nil {
						t.Fatal("stale represented provider accepted")
					}
					prepared, err := e.ValidateCatalogStage(context.Background(), stage)
					if err != nil {
						t.Fatal("cleaned retirement failed validation", err)
					}
					if e.CatalogRead().State() != base.State() || len(old.Security.Policies) != 2 || e.Storage.Relations[old.Address()] != storage {
						t.Fatal("private retirement mutated published roots/storage")
					}
					if _, err := enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: base.Epoch()}); err != nil {
						t.Fatal(err)
					}
					now := e.CatalogRead()
					for _, address := range []ids.ObjectAddress{providerRef.Address, rowAddress, arrayAddress} {
						if _, exists := now.Descriptor(address); exists {
							t.Fatal("retired provider remains", address)
						}
						if _, exists := base.Descriptor(address); !exists {
							t.Fatal("old immutable root lost provider", address)
						}
					}
					if e.Storage.Relations[old.Address()] != storage || len(old.Security.Policies) != 2 {
						t.Fatal("retirement changed survivor storage or old policies")
					}
					enginetest.RoleExecute(t, e, op, "ALTER POLICY retained ON t")
					result := enginetest.Run(t, e, "SELECT id FROM t")
					if result.Count != 1 {
						t.Fatal("owner data lost", result.Count)
					}
					result.Rows.Close()
					enginetest.RoleExecute(t, e, op, "CREATE TABLE provider(id INTEGER)")
					enginetest.RoleExecute(t, e, op, "DROP TABLE provider RESTRICT")
				})
			}
		})
	}
}
