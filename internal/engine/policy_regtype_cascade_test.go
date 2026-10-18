package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
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
					roleExecute(t, e, op, "INSERT INTO t VALUES(7,TRUE)")
					roleExecute(t, e, op, "CREATE TABLE provider(id INTEGER)")
					roleExecute(t, e, op, "CREATE POLICY p ON t "+clause+"('"+represented+"'::regtype IS NOT NULL)")
					roleExecute(t, e, op, "CREATE POLICY retained ON t USING(NULL::regtype IS NULL) WITH CHECK('integer'::regtype IS NOT NULL)")
					base := e.CatalogRead()
					ownerRef := roleTableRef(t, e, "t")
					old := base.MustRelation(ownerRef.Address)
					providerRef := roleTableRef(t, e, "provider")
					provider := base.MustRelation(providerRef.Address)
					rowAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(provider.rowType)}
					row := base.MustType(rowAddress)
					arrayAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.array)}
					storage := e.storage[old.address]
					retained := old.policies[1]
					stage := e.BeginCatalogStage(base)
					defer e.DiscardCatalogStage(stage)
					if err := stage.Remove(providerRef); err != nil {
						t.Fatal(err)
					}
					if err := stageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{providerRef.Address}, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil {
						t.Fatal(err)
					}
					next, ok := stage.descriptors[old.address].(*relationDescriptor)
					if !ok || len(next.policies) != 1 || !reflect.DeepEqual(next.policies[0], retained) || next.revision <= old.revision {
						t.Fatal("wrong surviving policy collection", next)
					}
					if _, removed := stage.removals[old.address]; removed {
						t.Fatal("policy owner became a retirement target")
					}
					if want := []string{"drop cascades to policy p on table t"}; !reflect.DeepEqual(stage.viewCascadeNames, want) {
						t.Fatal("wrong captured notices", stage.viewCascadeNames)
					}
					view := e.stageRead(stage)
					// Validation must still reject the unpruned old condition; the
					// fix changes retirement authority, not validator strictness.
					condition := policyExpressionOf(old.policies[0].using)
					if clause == "WITH CHECK" {
						condition = policyExpressionOf(old.policies[0].check)
					}
					if err := validatePolicyExpression(view, next, condition); err == nil {
						t.Fatal("stale represented provider accepted")
					}
					prepared, err := e.ValidateCatalogStage(context.Background(), stage)
					if err != nil {
						t.Fatal("cleaned retirement failed validation", err)
					}
					if e.CatalogRead().state != base.state || len(old.policies) != 2 || e.storage[old.address] != storage {
						t.Fatal("private retirement mutated published roots/storage")
					}
					if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
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
					if e.storage[old.address] != storage || len(old.policies) != 2 {
						t.Fatal("retirement changed survivor storage or old policies")
					}
					roleExecute(t, e, op, "ALTER POLICY retained ON t")
					result := run(t, e, "SELECT id FROM t")
					if result.Count != 1 {
						t.Fatal("owner data lost", result.Count)
					}
					result.Rows.Close()
					roleExecute(t, e, op, "CREATE TABLE provider(id INTEGER)")
					roleExecute(t, e, op, "DROP TABLE provider RESTRICT")
				})
			}
		})
	}
}
