package binder_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
)

func TestClusterIndexMetadataAfterRoutineReplacement(t *testing.T) {
	e, s, _ := clusterStorageSeed(t)
	for _, q := range []string{
		"CREATE FUNCTION public.cluster_metadata_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1+10$$",
		"CREATE INDEX metadata_key ON t(public.cluster_metadata_key(k))",
		"CREATE INDEX metadata_predicate ON t(k) WHERE public.cluster_metadata_key(k)>11",
	} {
		enginetest.SessionSQL(t, s, q)
	}
	original := e.CatalogRead()
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION public.cluster_metadata_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1+20$$")
	view := e.CatalogRead()
	for _, name := range []string{"metadata_key", "metadata_predicate"} {
		t.Run(name, func(t *testing.T) {
			ref := enginetest.CatalogRef(t, e, view, name)
			idx := view.MustRelation(ref.Address)
			facts, err := index.ProjectIndexFacts(t.Context(), view, ref)
			if err != nil || facts.IndIsClustered || !facts.IndIsReady || !facts.IndIsValid || !facts.IndIsLive || facts.IndNKeyAtts != 1 || facts.IndNatts != 1 {
				t.Fatal("frozen structural facts", facts, err)
			}
			if name == "metadata_key" {
				if len(facts.IndexExpressions) != 1 {
					t.Fatal("lost original index expression", facts)
				}
				call := facts.IndexExpressions[0].(plan.SQLRoutineExpr)
				stored := idx.SQLIndex.Keys[0].Derived.Definition.(plan.SQLRoutineExpr)
				current, exists := view.Procedure(call.Ref.Address)
				if !exists || call.Procedure != stored.Procedure || call.Info.RoutineReference != stored.Procedure || call.Procedure == current {
					t.Fatal("metadata rebound or substituted original routine nodes", call, current)
				}
			} else if !reflect.DeepEqual(facts.Predicate, idx.SQLIndex.Predicate.Definition) {
				t.Fatal("metadata replaced original predicate", facts.Predicate)
			}
			if idx != original.MustRelation(ref.Address) {
				t.Fatal("function replacement mutated index descriptor")
			}
			if err := index.ValidateIndexDefinition(view, idx); err == nil || err.Error() != "stuntgres: stale routine declaration carrier" {
				t.Fatal("metadata mode escaped into strict definition validation", err)
			}
			strict, names, err := index.ProjectIndexMetadata(t.Context(), view, ref)
			if err == nil || !reflect.DeepEqual(strict, index.Metadata{}) || !reflect.DeepEqual(names, index.IndexesMetadata{}) {
				t.Fatal("combined deparse accepted stale executable declaration", strict, names, err)
			}
			old, err := index.ProjectIndexFacts(t.Context(), original, ref)
			if err != nil || !reflect.DeepEqual(old, facts) {
				t.Fatal("facts changed original snapshot/node ownership", old, facts, err)
			}
		})
	}
	ref := enginetest.CatalogRef(t, e, view, "metadata_key")
	idx := view.MustRelation(ref.Address)
	foreign := engine.New()
	for _, invalid := range []struct {
		name string
		ref  ids.ObjectRef
	}{
		{"foreign_domain", foreign.Catalog.Domain.Ref(view.Epoch(), ref.Address)},
		{"wrong_epoch", ref.Domain().Ref(ref.Epoch+1, ref.Address)},
		{"missing_index", ref.Domain().Ref(ref.Epoch, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999999})},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			facts, err := index.ProjectIndexFacts(t.Context(), view, invalid.ref)
			if err == nil || !reflect.DeepEqual(facts, index.Metadata{}) {
				t.Fatal("invalid capture exposed facts", facts, err)
			}
		})
	}
	for _, mode := range []string{"missing_parent", "bad_provider", "zero_owner", "foreign_owner", "foreign_dependency", "missing_manifest", "bad_call_carrier"} {
		t.Run(mode, func(t *testing.T) {
			state := *view.State()
			broken, definition := *idx, *idx.SQLIndex
			broken.SQLIndex = &definition
			switch mode {
			case "missing_parent":
				definition.Parent = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999999}
			case "bad_provider":
				definition.MethodOID = 999999
			case "zero_owner":
				broken.Owner = e.RoleStore.Domain.Principal(0)
			case "foreign_owner":
				broken.Owner = foreign.CatalogRead().Owner().Principals.Principal(idx.Owner.ID())
			default:
				definition.Keys = slices.Clone(definition.Keys)
				derived := *definition.Keys[0].Derived
				definition.Keys[0].Derived = &derived
				call := derived.Definition.(plan.SQLRoutineExpr)
				if mode == "bad_call_carrier" {
					call.Info.RoutineReference = nil
					derived.Definition = call
				} else {
					derived.Dependencies = slices.Clone(derived.Dependencies)
					for i, dependency := range derived.Dependencies {
						if dependency != call.Ref {
							continue
						}
						if mode == "foreign_dependency" {
							derived.Dependencies[i] = foreign.Catalog.Domain.Ref(dependency.Epoch, dependency.Address)
						} else {
							derived.Dependencies = append(derived.Dependencies[:i], derived.Dependencies[i+1:]...)
						}
						break
					}
				}
			}
			state.SetDescriptor(ref.Address, &broken)
			facts, err := index.ProjectIndexFacts(t.Context(), e.Catalog.Read(&state), ref)
			if err == nil || !reflect.DeepEqual(facts, index.Metadata{}) {
				t.Fatal("structural invariant was suppressed", mode, facts, err)
			}
			if mode == "zero_owner" && err.Error() != "stuntgres: invalid index metadata identity" {
				t.Fatal("zero owner bypassed metadata identity validation", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	facts, err := index.ProjectIndexFacts(ctx, view, ref)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(facts, index.Metadata{}) {
		t.Fatal("metadata ignored cancellation", facts, err)
	}
}

func clusterStorageSeed(t *testing.T) (*core.Engine, *session.Session, ids.ObjectAddress) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id int,k int,payload text)", "INSERT INTO t VALUES(1,2,'b'),(2,1,'a'),(3,2,'b'),(4,NULL,NULL),(5,3,'c')", "CREATE INDEX a ON t(k)", "CREATE INDEX b ON t((lower(payload)) DESC NULLS FIRST,k) INCLUDE(id)", "CREATE UNIQUE INDEX u ON t(id)", "CREATE TABLE unrelated(id int)", "INSERT INTO unrelated VALUES(9)", "CREATE INDEX unrelated_idx ON unrelated(id)"} {
		enginetest.SessionSQL(t, s, q)
	}
	return e, s, enginetest.CatalogRef(t, e, e.CatalogRead(), "t").Address
}
