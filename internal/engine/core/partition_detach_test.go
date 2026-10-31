package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/sqltype"
)

// A ready-invalid virtual descriptor never needs a physical logical root, even
// when no members remain and coverage is vacuously complete.
func TestPartitionDetachVirtualRoots(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	stage := f.e.BeginCatalogStage(f.view)
	defer f.e.DiscardCatalogStage(stage)
	f.child.SQLIndex.PartitionParent = f.parent.Address()
	if err := partition.StageDetach(stage, stage.Base, f.root, f.leaf); err != nil {
		t.Fatal(err)
	}
	next := f.e.StageRead(stage)
	next.State().RemoveDescriptor(f.child.Address())
	next.State().RemoveDescriptor(f.leaf.Address())
	roots, err := core.EnginePrepareIndexRoots(f.e, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: next}}, catalog.NewState(0), next.State(), nil, nil, nil, nil, nil, true, sqltype.OutputEnvironment{}, nil, nil, nil)
	if err != nil || roots[f.parent.Address()] != nil || f.parent.SQLIndex.Valid {
		t.Fatal("virtual invalid-complete required root or promotion", err)
	}
}

func partitionAttachUnitRoot(t *testing.T, e *core.Engine, name string, id ids.OID) *catalog.Relation {
	t.Helper()
	d := inheritanceUnitRelation(e, name, id, "k")
	d.RelationKind = catalog.PartitionedRelation
	d.TableMethod = 0
	class, _ := sqltype.DefaultIndexClass(d.Columns[0].Usage.Type)
	comparison, err := operatorFamilyTestOrdering(d.Columns[0].Usage, class)
	if err != nil {
		t.Fatal(err)
	}
	d.Partition = &catalog.PartitionDefinition{Keys: []catalog.PartitionKeyEntry{{Attribute: 1, Usage: d.Columns[0].Usage, Class: class, Comparison: comparison, Label: "k"}}}
	return d
}

func inheritanceUnitRelation(e *core.Engine, name string, id ids.OID, names ...string) *catalog.Relation {
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: name, ObjectRevision: 1, Index: make(map[string]int)}
	d.TableMethod = 2
	for i, name := range names {
		d.Index[name] = i
		d.Attributes = append(d.Attributes, int32(i+1))
		d.Columns = append(d.Columns, catalog.Column{Name: name, Usage: enginetest.Usage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true, OriginTable: uint32(id), OriginAttribute: int16(i + 1)})
	}
	d.ColumnMeta.HighWater = int32(len(names))
	return d
}

func partitionAttachUnitRange(t *testing.T, parent *catalog.Relation, lower, upper int64) *catalog.PartitionMembership {
	t.Helper()
	lo, err := sqltype.Integer(parent.Columns[0].Usage.Type, lower)
	if err != nil {
		t.Fatal(err)
	}
	hi, err := sqltype.Integer(parent.Columns[0].Usage.Type, upper)
	if err != nil {
		t.Fatal(err)
	}
	return &catalog.PartitionMembership{Parent: parent.Address(), Lower: []catalog.PartitionDatum{{Value: lo}}, Upper: []catalog.PartitionDatum{{Value: hi}}}
}

// All setup uses catalog/transaction functions, not a public SQL executor.
func partitionAttachUnitInstall(t *testing.T, e *core.Engine, relations ...*catalog.Relation) {
	t.Helper()
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	for _, d := range relations {
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}
