package core_test

import (
	"bytes"
	"context"
	"maps"
	"reflect"
	"slices"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqltype"
	"testing"
)

func ginMaintenanceRetainedRejection(t *testing.T) {
	e := engine.New()
	actor := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TEMP TABLE gin_keep(id integer,a integer[],s text[] COLLATE \"C\")",
		"CREATE INDEX gin_keep_i ON gin_keep USING gin(a,s)",
		"INSERT INTO gin_keep VALUES(77,'{7}','{cat}')",
		"ALTER OPERATOR CLASS pg_catalog.array_ops USING gin RENAME TO gin_keep_array",
	} {
		enginetest.SessionSQL(t, actor, q)
	}
	var target *catalog.Relation
	view := e.CatalogRead()
	for _, descriptor := range view.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.Name() == "gin_keep_i" {
			target = relation
		}
	}
	if target == nil {
		t.Fatal("missing real TEMP GIN index")
	}
	root := ginCorePending(t, e, target, 2, 1)
	payload := ginCoreCopyPayload(root.Gin)
	keys := slices.Clone(target.SQLIndex.Keys)
	state := view.State()
	roots := maps.Clone(e.Storage.IndexRoots)
	stores := maps.Clone(e.Storage.Relations)
	rowFrontier, versionFrontier := e.Storage.RowFrontier(), e.Storage.VersionFrontier()
	for _, kind := range []string{"support-member", "callable", "type", "collation", "stale-source", "detached-source", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			candidate := catalog.CloneState(state)
			sourceRoots := maps.Clone(roots)
			ctx := context.Background()
			switch kind {
			case "support-member":
				member, _ := view.FamilySupport(2745, 2277, 2277, 2)
				changed := *member
				changed.Function = 333
				changed.ObjectRevision++
				candidate.SetDescriptor(changed.Address(), &changed)
			case "callable":
				procedure, _ := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 2743})
				changed := *procedure
				changed.GinSupport = 0
				changed.ObjectRevision++
				candidate.SetDescriptor(changed.Address(), &changed)
			case "type":
				typ, _ := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 1007})
				changed := *typ
				for _, replacement := range sqltype.Builtins() {
					if replacement.ID() == 1007 {
						changed.SQLType = replacement
						break
					}
				}
				candidate.SetDescriptor(changed.Address(), &changed)
			case "collation":
				collation := view.Get(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: 950}).(*catalog.Collation)
				changed := *collation
				changed.Semantics = ^uint32(0)
				candidate.SetDescriptor(changed.Address(), &changed)
			case "stale-source":
				stale := *root
				gin := *root.Gin
				gin.Pending = slices.Clone(gin.Pending)
				gin.Pending[0].Binding.Class = sqltype.IndexClass{}
				stale.Gin = &gin
				sourceRoots[target.Address()] = &stale
			case "detached-source":
				detached := *target
				candidate.SetDescriptor(detached.Address(), &detached)
			case "cancel":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			e.Mu.Lock()
			_, _, _, err := index.RemintStorage(ctx, view.WithState(candidate), index.RemintBase{Catalog: state, Relations: stores, Roots: sourceRoots, NextRow: rowFrontier, NextVersion: versionFrontier, Frontier: e.Visibility.CommitSeq()}, maps.Clone(stores), &index.Snapshot{})
			e.Mu.Unlock()
			if err == nil {
				t.Fatal("incompatible retained source acquired replacement authority")
			}
			if e.CatalogRead().State() != state || !maps.Equal(roots, e.Storage.IndexRoots) || !maps.Equal(stores, e.Storage.Relations) || e.Storage.RowFrontier() != rowFrontier || e.Storage.VersionFrontier() != versionFrontier || !reflect.DeepEqual(root.Gin, payload) || !reflect.DeepEqual(target.SQLIndex.Keys, keys) {
				t.Fatal("rejected replacement changed live retained source")
			}
		})
	}
	for _, q := range []string{"SELECT gin_clean_pending_list('pg_temp.gin_keep_i'::regclass)", "INSERT INTO gin_keep VALUES(78,'{8}','{dog}')"} {
		enginetest.SessionSQL(t, actor, q)
	}
	ginCorePending(t, e, target, 2, 2)
	beforeReset := ginCorePending(t, e, target, 2, 2)
	beforeResetPayload := ginCoreCopyPayload(beforeReset.Gin)
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	target, _ = e.CatalogRead().Relation(target.Address())
	if target == nil || target.SQLIndex.Keys[0].Class.Name != "array_ops" {
		t.Fatal("Reset retained old persistent class presentation")
	}
	verify := func() {
		t.Helper()
		ginCorePending(t, e, target, 2, 2)
		if !slices.Equal(ginCorePlacementIDs(t, e, target, true), []int64{78}) || !slices.Equal(ginCorePlacementIDs(t, e, target, false), []int64{77}) {
			t.Fatal("retained TEMP split collapsed")
		}
		parent, _ := e.CatalogRead().Relation(target.SQLIndex.Parent)
		if _, err := index.BindGinIndex(e.CatalogRead(), parent, target.SQLIndex); err != nil {
			t.Fatal("retained TEMP current authority", err)
		}
	}
	verify()
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	target, _ = e.CatalogRead().Relation(target.Address())
	if target == nil {
		t.Fatal("Restore lost retained TEMP index")
	}
	verify()
	enginetest.SessionSQL(t, actor, "SELECT gin_clean_pending_list('pg_temp.gin_keep_i'::regclass)")
	ginCorePending(t, e, target, 0, 2)
	enginetest.SessionSQL(t, actor, "INSERT INTO gin_keep VALUES(79,'{9}','{fox}')")
	ginCorePending(t, e, target, 2, 3)
	if !reflect.DeepEqual(beforeReset.Gin, beforeResetPayload) {
		t.Fatal("replacement mutated old TEMP split")
	}
}
