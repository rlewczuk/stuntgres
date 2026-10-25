package core_test

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// This fixture publishes only genuine classless family/member descriptors.
// The denial cases add a private, graph-layer recorded dependency separately;
// they do not assert that SQL can create a CHECK referring to an amop/amproc.
func familyPreflightCatalog(t *testing.T) (*core.Engine, *catalog.OperatorFamily, *catalog.OperatorFamilyOperator, *catalog.OperatorFamilySupport) {
	t.Helper()
	e := engine.New()
	base := e.CatalogRead()
	builtin, _ := base.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: 1976})
	family := &catalog.OperatorFamily{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: catalogUnitOID(t, e)}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "family_preflight", ObjectRevision: 2, Owner: builtin.Owner, Method: 403}
	operator := &catalog.OperatorFamilyOperator{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodOperatorClassID, ObjectID: catalogUnitOID(t, e)}, ObjectRevision: 3, Family: family.Address().ObjectID, Left: 23, Right: 23, Number: 1, Purpose: 's', Operator: 97, Method: 403}
	support := &catalog.OperatorFamilySupport{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodProcedureClassID, ObjectID: catalogUnitOID(t, e)}, ObjectRevision: 4, Family: family.Address().ObjectID, Left: 23, Right: 23, Number: 1, Function: 351}
	setup := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(setup)
	for _, descriptor := range []catalog.Descriptor{family, operator, support} {
		if err := setup.Create(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	publication, err := e.ValidateCatalogStage(context.Background(), setup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enginetest.PublishCatalog(e, publication, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	return e, family, operator, support
}

// Capture both the immutable publication and all candidate mutation surfaces.
// dependencyPath is intentionally excluded: recording diagnostic visibility is
// the preflight's one permitted private effect, not a deletion/publication.
func familyPreflightUnchanged(t *testing.T, e *core.Engine, stage *core.CatalogStage, family *catalog.OperatorFamily, operator *catalog.OperatorFamilyOperator, support *catalog.OperatorFamilySupport) func() {
	t.Helper()
	published := e.Catalog.State
	base := e.CatalogRead()
	descriptors := maps.Collect(base.Descriptors())
	revisions := make(map[ids.ObjectAddress]ids.DefinitionRevision, len(descriptors))
	for address, descriptor := range descriptors {
		revisions[address] = descriptor.Revision()
	}
	familyValue, operatorValue, supportValue := *family, *operator, *support
	private := maps.Collect(stage.Descriptors())
	privateRevisions := make(map[ids.ObjectAddress]ids.DefinitionRevision, len(private))
	privateRelations := make(map[ids.ObjectAddress]catalog.Relation)
	for address, descriptor := range private {
		privateRevisions[address] = descriptor.Revision()
		if relation, ok := descriptor.(*catalog.Relation); ok {
			value := *relation
			value.Constraints.Check = slices.Clone(relation.Constraints.Check)
			for i := range value.Constraints.Check {
				value.Constraints.Check[i].Dependencies = slices.Clone(relation.Constraints.Check[i].Dependencies)
			}
			privateRelations[address] = value
		}
	}
	names, touched := maps.Collect(stage.Names()), maps.Collect(stage.Touched())
	removals, oldNames := maps.Collect(stage.Removals()), maps.Collect(stage.OldNames())
	targets := slices.Clone(stage.DomainTargets)
	lifecycle, invalid := stage.Lifecycle, stage.Invalid
	return func() {
		t.Helper()
		if e.Catalog.State != published || !maps.Equal(descriptors, maps.Collect(e.CatalogRead().Descriptors())) {
			t.Fatal("preflight changed the published catalog")
		}
		for address, descriptor := range e.CatalogRead().Descriptors() {
			if descriptor.Revision() != revisions[address] {
				t.Fatal("preflight changed a published revision", address)
			}
			key := catalog.DescriptorNameKey(descriptor)
			if base.NameAddress(key) != e.CatalogRead().NameAddress(key) {
				t.Fatal("preflight changed a published name", key)
			}
		}
		if !reflect.DeepEqual(*family, familyValue) || !reflect.DeepEqual(*operator, operatorValue) || !reflect.DeepEqual(*support, supportValue) {
			t.Fatal("preflight mutated a family/member descriptor")
		}
		if !maps.Equal(private, maps.Collect(stage.Descriptors())) || !maps.Equal(names, maps.Collect(stage.Names())) || !maps.Equal(touched, maps.Collect(stage.Touched())) || !maps.Equal(removals, maps.Collect(stage.Removals())) || !maps.Equal(oldNames, maps.Collect(stage.OldNames())) {
			t.Fatal("preflight changed candidate descriptors, names, observations or removals")
		}
		for address, descriptor := range stage.Descriptors() {
			if descriptor.Revision() != privateRevisions[address] {
				t.Fatal("preflight changed a private revision", address)
			}
			if relation, ok := descriptor.(*catalog.Relation); ok && !reflect.DeepEqual(*relation, privateRelations[address]) {
				t.Fatal("preflight changed the private dependency graph", address)
			}
		}
		if !slices.Equal(stage.DomainTargets, targets) || stage.Lifecycle != lifecycle || stage.Invalid != invalid {
			t.Fatal("preflight queued a target or changed publication lifecycle")
		}
		for _, read := range []catalog.Read{base, e.CatalogRead(), e.StageRead(stage)} {
			if got, ok := read.FamilyOperator(family.Address().ObjectID, 23, 23, 1, 's'); !ok || got != operator {
				t.Fatal("preflight changed the operator primary index", got)
			}
			if got, ok := read.FamilySupport(family.Address().ObjectID, 23, 23, 1); !ok || got != support {
				t.Fatal("preflight changed the support primary index", got)
			}
			for _, descriptor := range []catalog.Descriptor{family, operator, support} {
				if read.Get(descriptor.Address()) != descriptor || read.NameAddress(catalog.DescriptorNameKey(descriptor)) != descriptor.Address() {
					t.Fatal("preflight changed descriptor/name identity", descriptor.Address())
				}
			}
		}
		// The real member provider enforces the secondary amop identity.
		// A fresh primary slot must still conflict with the retained member.
		duplicate := *operator
		duplicate.ObjectAddress.ObjectID = operator.Address().ObjectID + 1000000
		duplicate.Number = 2
		probe := catalog.NewCandidate(e.StageRead(stage))
		if err := probe.Create(&duplicate); err != nil {
			t.Fatal("secondary-index probe primary slot was not free", err)
		}
		if err := (catalog.OperatorFamilyProvider{Class: ids.AccessMethodOperatorClassID}).Validate(e.Catalog.Read(probe.State()), &duplicate); err == nil {
			t.Fatal("preflight lost the operator secondary identity")
		}
	}
}

func TestOperatorFamilyPreflight(t *testing.T) {
	for _, function := range []bool{false, true} {
		name := "operator"
		if function {
			name = "function"
		}
		t.Run(name+" denial before later missing", func(t *testing.T) {
			e, family, operator, support := familyPreflightCatalog(t)
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			target := operator.Address()
			object := "operator 1 (integer, integer) of operator family family_preflight for access method btree: <(integer,integer)"
			if function {
				target = support.Address()
				object = "function 1 (integer, integer) of operator family family_preflight for access method btree: btint4cmp(integer,integer)"
			}
			// Explicit graph-boundary input, not an executable CHECK recipe or
			// a public custom-class association. Never validate/publish this
			// relation: SQL CHECK admission does not accept member references.
			dependent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}, ObjectNamespace: family.Namespace(), ObjectName: "family_preflight_dependent", ObjectRevision: 7, Owner: family.Owner, RelationKind: catalog.TableRelation}
			dependent.Constraints.Check = []catalog.CheckConstraint{{OID: catalogUnitOID(t, e), Name: "member_guard", Dependencies: []catalog.ExpressionDependency{{Ref: e.Catalog.Domain.Ref(stage.Base.Epoch(), target), Revision: stage.Base.Get(target).Revision()}}}}
			if err := stage.Create(dependent); err != nil {
				t.Fatal(err)
			}
			unchanged := familyPreflightUnchanged(t, e, stage, family, operator, support)
			op, done, err := enginetest.Operation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			auth := core.EngineBootstrapAuthorization(e, family.Owner)
			host := schemacmds.RoutineHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}
			err = index.StageAlterOperatorFamily(host, auth, &parser.AlterOperatorFamily{Action: "drop", Name: parser.ObjectName{Parts: []parser.Identifier{{Text: family.Name()}}}, Method: parser.Identifier{Text: "btree"}, Items: []parser.OperatorFamilyItem{
				{Function: function, Number: "1", Association: []parser.TypeName{{Name: "integer"}}},
				{Function: function, Number: "2", Association: []parser.TypeName{{Name: "integer"}}},
			}})
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + object + " because other objects depend on it", Detail: "constraint member_guard on table public.family_preflight_dependent depends on " + object, Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
			if !reflect.DeepEqual(err, want) {
				t.Fatalf("preflight diagnostic = %#v, want %#v (not the later missing slot)", err, want)
			}
			unchanged()
		})
	}

	t.Run("dependency path failure", func(t *testing.T) {
		e, family, operator, support := familyPreflightCatalog(t)
		stage := e.BeginCatalogStage(e.CatalogRead())
		defer e.DiscardCatalogStage(stage)
		unchanged := familyPreflightUnchanged(t, e, stage, family, operator, support)
		op, done, err := enginetest.Operation(e, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer done()
		auth := core.EngineBootstrapAuthorization(e, family.Owner)
		core.SetAuthorizationPathPrefix(&auth, family.Namespace())
		auth.Owner = nil // Actual stale-authority rejection in referencePath/CanUse.
		err = (schemacmds.RoutineHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}).CheckFamilyMemberDrop(auth, support.Address())
		if err == nil || err.Error() != "stuntgres: foreign or stale authorization object" {
			t.Fatalf("dependency-path error = %#v", err)
		}
		if stage.DependencyPath != nil {
			t.Fatal("failed path resolution recorded a successful path")
		}
		unchanged()
	})

	t.Run("closure cancellation", func(t *testing.T) {
		e, family, operator, support := familyPreflightCatalog(t)
		stage := e.BeginCatalogStage(e.CatalogRead())
		defer e.DiscardCatalogStage(stage)
		unchanged := familyPreflightUnchanged(t, e, stage, family, operator, support)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		op, done, err := enginetest.Operation(e, ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer done()
		cancel() // No timer, worker or scheduling-dependent cancellation window.
		err = (schemacmds.RoutineHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}).CheckFamilyMemberDrop(core.EngineBootstrapAuthorization(e, family.Owner), support.Address())
		if err != context.Canceled {
			t.Fatalf("closure error = %#v, want original context.Canceled", err)
		}
		wantPath := []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, {ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}}
		if !slices.Equal(stage.DependencyPath, wantPath) {
			t.Fatalf("closure did not retain the actual diagnostic path: %v", stage.DependencyPath)
		}
		unchanged()
	})
}
