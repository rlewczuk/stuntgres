package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func TestDomainIdentityIndexClosure(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.d AS integer"))
	domainUnitTable(t, e, op, "CREATE TABLE public.identity_retire(v public.d,keep integer)")
	domainUnitTable(t, e, op, "CREATE TABLE public.identity_unrelated(v integer)")
	// Real private AST execution supplies canonical index definitions/providers
	// and empty physical roots, rather than fabricated index metadata.
	s := sessionStart(t, e, nil)
	for _, q := range []string{
		"CREATE INDEX identity_key ON public.identity_retire(v)",
		"CREATE INDEX identity_include ON public.identity_retire(keep) INCLUDE(v)",
		"CREATE INDEX identity_expression ON public.identity_retire((v+1))",
		"CREATE INDEX identity_predicate ON public.identity_retire(keep) WHERE v>0",
		"CREATE INDEX identity_keep ON public.identity_retire(keep)",
		"CREATE INDEX identity_other ON public.identity_unrelated(v)",
	} {
		usingIndexUnitExecute(t, e, s, statement(t, q))
	}
	before := e.CatalogRead()
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	address := func(name string) ids.ObjectAddress {
		return before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})
	}
	parent := before.MustRelation(address("identity_retire"))
	domain := domainUnitType(t, before, "d")
	stage := domainUnitStage(t, e, op, "DROP DOMAIN public.d CASCADE")
	edges, err := domainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{domain.Address()})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"identity_key", "identity_include", "identity_expression", "identity_predicate"} {
		found := false
		for _, edge := range edges {
			if edge.Address == address(name) {
				found = true
				if !edge.Automatic || edge.Target.ObjectID != parent.Address().ObjectID || edge.Target.SubID != parent.Attributes[0] {
					t.Fatal("index lost stable-attribute AUTO ownership", name, edge)
				}
			}
		}
		if !found {
			t.Fatal("missing index AUTO closure", name)
		}
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	if err := scope.gate(statement(t, "DROP DOMAIN public.d CASCADE")); err != nil {
		t.Fatal(err)
	}
	if err := scope.stageDomainDependents(stage, parser.DropCascade); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"identity_key", "identity_include", "identity_expression", "identity_predicate"} {
		if !stage.Removed(address(name)) {
			t.Fatal("index was left live until late index evolution", name)
		}
	}
	for _, name := range []string{"identity_keep", "identity_other"} {
		if stage.Removed(address(name)) {
			t.Fatal("unrelated index retired", name)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ValidateCatalogStage(ctx, stage); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.CatalogRead().State() != before.State() || len(parent.Columns) != 2 || before.Get(address("identity_key")) == nil {
		t.Fatal("unpublished closure changed retained roots")
	}
	// Validation must now see no live index column with a retired domain type.
	domainUnitPublish(t, e, stage)
	current := e.CatalogRead()
	for _, name := range []string{"identity_key", "identity_include", "identity_expression", "identity_predicate"} {
		if current.Get(address(name)) != nil {
			t.Fatal("retired index survived publication", name)
		}
	}
	if current.Get(address("identity_keep")) == nil || current.Get(address("identity_other")) != before.Get(address("identity_other")) || len(current.MustRelation(parent.Address()).Columns) != 1 || len(parent.Columns) != 2 {
		t.Fatal("survivors or old snapshot changed")
	}
}
