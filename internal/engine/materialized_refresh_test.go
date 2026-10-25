package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
	"testing"
)

func refreshUnitRelation(t *testing.T, e *Engine) *catalog.Relation {
	t.Helper()
	_, done, stage, d := materializedUnitStage(t, e)
	defer done()
	d.Populated = true
	rows := refreshUnitRows(t, d, 3)
	if err := e.StageQueryTableRows(context.Background(), stage, d, rows, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	return d
}

func refreshUnitRows(t *testing.T, d *catalog.Relation, count int) [][]sqltype.Value {
	t.Helper()
	rows := make([][]sqltype.Value, count)
	for i := range rows {
		value, err := sqltype.Integer(d.Columns[0].Usage.Type, int64(i+1))
		if err != nil {
			t.Fatal(err)
		}
		rows[i] = []sqltype.Value{value}
	}
	return rows
}

func TestMaterializedRefreshPublication(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	registry := queryCreateTypes(e)
	for _, tc := range []struct {
		count     int
		populated bool
	}{{4, true}, {1, true}, {0, true}, {0, false}, {3, true}} {
		old := d
		store := e.Storage.Relations[d.Address()]
		root, ends, generation := store.Versions, store.Ends, store.Generation
		frontier := e.Visibility.CommitSeq()
		stage := e.BeginCatalogStage(e.CatalogRead())
		rows := refreshUnitRows(t, d, tc.count)
		if err := e.stageMaterializedRows(context.Background(), stage, d, store, rows, tc.populated, ids.TxRef{}); err != nil {
			t.Fatal(err)
		}
		candidate := stage.MaterializedOrZero().Replacement.Storage
		if tc.count != 0 && &candidate.Versions[0].Values[0] != &rows[0][0] {
			t.Fatal("refresh copied finalized buffers")
		}
		if e.Storage.Relations[d.Address()] != store || e.Catalog.State.Get(d.Address()) != old {
			t.Fatal("staging leaked")
		}
		queryCreatePublish(t, e, stage)
		d = e.Catalog.State.MustRelation(d.Address())
		if d == old || d.View != old.View || d.Address() != old.Address() || d.RowType != old.RowType || stored.ViewOf(d).RuleOID() != stored.ViewOf(old).RuleOID() || d.Owner != old.Owner || d.Revision() != old.Revision()+1 || !reflect.DeepEqual(d.Security.ACL, old.Security.ACL) || !reflect.DeepEqual(d.Security.ColumnACL, old.Security.ColumnACL) || !reflect.DeepEqual(d.Columns, old.Columns) || !reflect.DeepEqual(d.Attributes, old.Attributes) {
			t.Fatal("refresh lost immutable identity/metadata")
		}
		if e.Storage.Relations[d.Address()] != candidate || len(candidate.Versions) != tc.count || candidate.Generation != generation+1 || d.Populated != tc.populated || e.Visibility.CommitSeq() != frontier+1 {
			t.Fatal("incomplete atomic replacement", d.Populated, e.Visibility.CommitSeq(), candidate)
		}
		if !storage.SameVersionRoot(root, store.Versions) || store.Ends != ends || store.Generation != generation {
			t.Fatal("predecessor mutated")
		}
		for _, row := range candidate.Versions {
			if row.Born != frontier+1 {
				t.Fatal("wrong row frontier", row)
			}
		}
		queryCreateAssertTypes(t, e, registry)
	}
}

func TestMaterializedRefreshSnapshots(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	old := e.Storage.Relations[d.Address()]
	values := old.Versions[0].Values
	for _, populated := range []bool{true, false} {
		current := e.Catalog.State.MustRelation(d.Address())
		stage := e.BeginCatalogStage(e.CatalogRead())
		if err := e.stageMaterializedRows(context.Background(), stage, current, e.Storage.Relations[d.Address()], nil, populated, ids.TxRef{}); err != nil {
			t.Fatal(err)
		}
		queryCreatePublish(t, e, stage)
		if len(old.Versions) != 3 || &old.Versions[0].Values[0] != &values[0] || !sqltype.SameDatum(old.Versions[0].Values[0], values[0]) || old.Ends != nil {
			t.Fatal("retained storage changed")
		}
	}
}

func TestMaterializedRefreshPublicationFences(t *testing.T) {
	for _, phase := range []string{"generation", "length", "ends", "store", "descriptor", "epoch", "cancel", "frontier", "source", "role", "authorization", "foreign_descriptor", "wrong_width", "wrong_type"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			d := refreshUnitRelation(t, e)
			store := e.Storage.Relations[d.Address()]
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			rows := refreshUnitRows(t, d, 4)
			if phase == "wrong_width" {
				rows[0] = nil
			}
			if phase == "wrong_type" {
				rows[0][0] = sqltype.Null(testUsage(e, sqltype.Text).Type)
			}
			err := e.stageMaterializedRows(context.Background(), stage, d, store, rows, true, ids.TxRef{})
			if phase == "wrong_width" || phase == "wrong_type" {
				if err == nil || e.Storage.Relations[d.Address()] != store || e.Catalog.State.Get(d.Address()) != d {
					t.Fatal("invalid row accepted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				t.Fatal(err)
			}
			switch phase {
			case "generation":
				store.Generation++
			case "length":
				store.Versions = store.Versions[:2]
			case "ends":
				store.Ends = &storage.DeletionRoot{}
			case "store":
				copy := *store
				e.Mu.Lock()
				e.Storage.InstallRelationLocked(d.Address(), &copy)
				e.Mu.Unlock()
			case "descriptor":
				copy := *d
				copy.ObjectRevision++
				e.Catalog.State.SetDescriptor(d.Address(), &copy)
			case "epoch":
				e.Catalog.State.SetEpoch(e.Catalog.State.Epoch() + 1)
			case "cancel":
				cancel()
			case "frontier":
				e.Visibility.SetCommitSeqLocked(ids.CommitSeq(math.MaxUint64))
			case "source":
				stage.QuerySources = []catalog.Descriptor{&catalog.Relation{ObjectAddress: d.Address()}}
			case "role", "authorization":
				op, done := roleOperation(t, e, "postgres")
				defer done()
				stage.AuthSession = op.Session
				stage.AuthRevision, stage.RoleRevision = op.Session.Authorization.Revision, e.RoleStore.Published.Revision
				if phase == "role" {
					e.RoleStore.Published.Revision++
				} else {
					op.Session.Authorization.Revision++
				}
			case "foreign_descriptor":
				copy := *stage.Get(d.Address()).(*catalog.Relation)
				stage.UpdateDescriptor(&copy)
			}
			beforeCatalog, beforeStore, beforeFrontier := e.Catalog.State, e.Storage.Relations[d.Address()], e.Visibility.CommitSeq()
			_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
			if err == nil || e.Catalog.State != beforeCatalog || e.Storage.Relations[d.Address()] != beforeStore || e.Visibility.CommitSeq() != beforeFrontier {
				t.Fatal("failed fence published", err)
			}
			if phase == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestMaterializedRefreshPublicationCounters(t *testing.T) {
	for _, phase := range []string{"row", "version", "revision", "generation"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			d := refreshUnitRelation(t, e)
			store := e.Storage.Relations[d.Address()]
			switch phase {
			case "row":
				e.Storage.SetFrontierLocked(math.MaxUint64, e.Storage.VersionFrontier())
			case "version":
				e.Storage.SetFrontierLocked(e.Storage.RowFrontier(), math.MaxUint64)
			case "revision":
				d.ObjectRevision = ids.DefinitionRevision(math.MaxUint64)
			case "generation":
				store.Generation = math.MaxUint64
			}
			stage := e.BeginCatalogStage(e.CatalogRead())
			beforeRow, beforeVersion := e.Storage.RowFrontier(), e.Storage.VersionFrontier()
			if err := e.stageMaterializedRows(context.Background(), stage, d, store, refreshUnitRows(t, d, 1), true, ids.TxRef{}); err == nil {
				t.Fatal("overflow accepted")
			}
			if e.Storage.RowFrontier() != beforeRow || e.Storage.VersionFrontier() != beforeVersion || e.Storage.Relations[d.Address()] != store || e.Catalog.State.Get(d.Address()) != d {
				t.Fatal("overflow changed live state")
			}
		})
	}
}

func TestMaterializedRefreshProviderFences(t *testing.T) {
	for _, phase := range []string{"reject", "cancel", "predecessor"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			d := refreshUnitRelation(t, e)
			old := e.Storage.Relations[d.Address()]
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			if err := e.stageMaterializedRows(context.Background(), stage, d, old, refreshUnitRows(t, d, 4), true, ids.TxRef{}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("materialized provider rejected")
			e.Catalog.Providers[ids.RelationClassID] = queryCreateProbeProvider{Provider: e.Catalog.Providers[ids.RelationClassID], e: e, t: t, check: func() error {
				switch phase {
				case "reject":
					return sentinel
				case "cancel":
					cancel()
				case "predecessor":
					e.Mu.Lock()
					old.Generation++
					e.Mu.Unlock()
				}
				return nil
			}}
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err == nil {
				_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
			}
			if err == nil || e.Catalog.State.Get(d.Address()) != d || e.Storage.Relations[d.Address()] != old || len(old.Versions) != 3 {
				t.Fatal("provider fence installed a candidate", err)
			}
			if phase == "reject" && !errors.Is(err, sentinel) {
				t.Fatal(err)
			}
			if phase == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}
