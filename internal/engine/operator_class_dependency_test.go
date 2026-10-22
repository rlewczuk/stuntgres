package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
	"testing"
)

// Exercise the descriptor graph directly: required-member redirection must
// discover sibling members and consumers without hardening loose entries.
func TestOperatorClassDependencyClosure(t *testing.T) {
	e := New()
	ctx := context.Background()
	base := e.CatalogRead()
	family, class, member := operatorClassUnitDefinitions(t, e, "class_closure", 351)
	builtin, ok := base.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 97})
	if !ok {
		t.Fatal("missing operator descriptor")
	}
	implementation := *builtin
	implementation.ObjectAddress.ObjectID = catalogUnitOID(t, e)
	implementation.ObjectNamespace = class.Namespace()
	implementation.ObjectName = "==="
	sibling := &catalog.OperatorFamilyOperator{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodOperatorClassID, ObjectID: catalogUnitOID(t, e)}, ObjectRevision: 1, Family: family.Address().ObjectID, Left: 23, Right: 23, Operator: implementation.Address().ObjectID, Method: 403, Number: 1, Purpose: 's', OwningClass: class.Address().ObjectID}
	optional := *member
	optional.ObjectAddress.ObjectID = catalogUnitOID(t, e)
	optional.Number, optional.Function, optional.OwningClass = 2, 3130, 0
	looseFamily := *family
	looseFamily.ObjectAddress.ObjectID = catalogUnitOID(t, e)
	looseFamily.ObjectName = "class_closure_loose"
	loose := *sibling
	loose.ObjectAddress.ObjectID = catalogUnitOID(t, e)
	loose.Family, loose.OwningClass = looseFamily.Address().ObjectID, 0
	setup := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(setup)
	for _, d := range []catalog.Descriptor{&implementation, family, class, member, sibling, &optional, &looseFamily, &loose} {
		if err := setup.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	publication, err := e.ValidateCatalogStage(ctx, setup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(publication, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	current := e.CatalogRead()
	for _, tc := range []struct {
		name string
		root ids.ObjectAddress
		want map[ids.ObjectAddress]bool // true means automatic retirement
	}{
		{"implementation", implementation.Address(), map[ids.ObjectAddress]bool{class.Address(): false, member.Address(): true, sibling.Address(): true, loose.Address(): true}},
		{"class", class.Address(), map[ids.ObjectAddress]bool{member.Address(): true, sibling.Address(): true}},
		{"family", family.Address(), map[ids.ObjectAddress]bool{class.Address(): true, member.Address(): true, sibling.Address(): true, optional.Address(): true}},
		{"classless family", looseFamily.Address(), map[ids.ObjectAddress]bool{loose.Address(): true}},
		{"loose member", loose.Address(), map[ids.ObjectAddress]bool{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stage := e.BeginCatalogStage(current)
			defer e.DiscardCatalogStage(stage)
			edges, err := domainDependencyClosure(ctx, stage, []ids.ObjectAddress{tc.root})
			if err != nil {
				t.Fatal(err)
			}
			got := make(map[ids.ObjectAddress]table.DomainDependency, len(edges))
			for _, edge := range edges {
				if _, duplicate := got[edge.Address]; duplicate {
					t.Fatal("duplicate retirement", edge)
				}
				got[edge.Address] = edge
			}
			if len(got) != len(tc.want) {
				t.Fatalf("closure got %v want %v", got, tc.want)
			}
			for address, automatic := range tc.want {
				edge, ok := got[address]
				if !ok || edge.Automatic != automatic || edge.Root != tc.root {
					t.Fatalf("edge %v got %#v want automatic=%v root=%v", address, edge, automatic, tc.root)
				}
			}
			if tc.root == implementation.Address() && got[class.Address()].Target != implementation.Address() {
				t.Fatal("redirect lost the implementation dependency")
			}
		})
	}
	for _, owned := range []ids.ObjectAddress{member.Address(), sibling.Address()} {
		stage := e.BeginCatalogStage(current)
		_, err := domainDependencyClosure(ctx, stage, []ids.ObjectAddress{owned})
		diagnostic, ok := err.(*sqlstate.Error)
		if !ok || diagnostic.Code != "2BP01" || diagnostic.Hint != "You can drop operator class class_closure for access method btree instead." {
			t.Fatalf("member owner restriction: %#v", err)
		}
		// Explicitly retiring both owner and member remains legal.
		if _, err := domainDependencyClosure(ctx, stage, []ids.ObjectAddress{class.Address(), owned}); err != nil {
			t.Fatal("owner/member roots", err)
		}
		e.DiscardCatalogStage(stage)
	}
	if e.CatalogRead().State() != current.State() || current.Get(member.Address()) != member || current.Get(class.Address()) != class {
		t.Fatal("discovery mutated the published graph")
	}
}
