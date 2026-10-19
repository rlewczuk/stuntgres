package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func refreshUnitRelation(t *testing.T, e *Engine) *catalog.Relation {
	t.Helper()
	_, done, stage, d := materializedUnitStage(t, e)
	defer done()
	d.Populated = true
	rows := refreshUnitRows(t, d, 3)
	if err := e.stageQueryTableRows(context.Background(), stage, d, rows, ids.TxRef{}); err != nil {
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
		store := e.storage.Relations[d.Address()]
		root, ends, generation := store.Versions, store.Ends, store.Generation
		frontier := e.commitSeq
		stage := e.BeginCatalogStage(e.CatalogRead())
		rows := refreshUnitRows(t, d, tc.count)
		if err := e.stageMaterializedRows(context.Background(), stage, d, store, rows, tc.populated, ids.TxRef{}); err != nil {
			t.Fatal(err)
		}
		candidate := stage.materializedReplacement.storage
		if tc.count != 0 && &candidate.Versions[0].Values[0] != &rows[0][0] {
			t.Fatal("refresh copied finalized buffers")
		}
		if e.storage.Relations[d.Address()] != store || e.catalog.state.Get(d.Address()) != old {
			t.Fatal("staging leaked")
		}
		queryCreatePublish(t, e, stage)
		d = e.catalog.state.MustRelation(d.Address())
		if d == old || d.View != old.View || d.Address() != old.Address() || d.RowType != old.RowType || viewOf(d).rule != viewOf(old).rule || d.Owner != old.Owner || d.Revision() != old.Revision()+1 || !reflect.DeepEqual(d.Security.ACL, old.Security.ACL) || !reflect.DeepEqual(d.Security.ColumnACL, old.Security.ColumnACL) || !reflect.DeepEqual(d.Columns, old.Columns) || !reflect.DeepEqual(d.Attributes, old.Attributes) {
			t.Fatal("refresh lost immutable identity/metadata")
		}
		if e.storage.Relations[d.Address()] != candidate || len(candidate.Versions) != tc.count || candidate.Generation != generation+1 || d.Populated != tc.populated || e.commitSeq != frontier+1 {
			t.Fatal("incomplete atomic replacement", d.Populated, e.commitSeq, candidate)
		}
		if !sameVersionRoot(root, store.Versions) || store.Ends != ends || store.Generation != generation {
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
	old := e.storage.Relations[d.Address()]
	values := old.Versions[0].Values
	for _, populated := range []bool{true, false} {
		current := e.catalog.state.MustRelation(d.Address())
		stage := e.BeginCatalogStage(e.CatalogRead())
		if err := e.stageMaterializedRows(context.Background(), stage, current, e.storage.Relations[d.Address()], nil, populated, ids.TxRef{}); err != nil {
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
			store := e.storage.Relations[d.Address()]
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
				if err == nil || e.storage.Relations[d.Address()] != store || e.catalog.state.Get(d.Address()) != d {
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
				store.Ends = &deletionRoot{}
			case "store":
				copy := *store
				e.storage.Relations[d.Address()] = &copy
			case "descriptor":
				copy := *d
				copy.ObjectRevision++
				e.catalog.state.SetDescriptor(d.Address(), &copy)
			case "epoch":
				e.catalog.state.SetEpoch(e.catalog.state.Epoch() + 1)
			case "cancel":
				cancel()
			case "frontier":
				e.commitSeq = ids.CommitSeq(math.MaxUint64)
			case "source":
				stage.querySources = []catalog.Descriptor{&catalog.Relation{ObjectAddress: d.Address()}}
			case "role", "authorization":
				op, done := roleOperation(t, e, "postgres")
				defer done()
				stage.authSession = op.session
				stage.authRevision, stage.roleRevision = op.session.authorization.revision, e.roles.revision
				if phase == "role" {
					e.roles.revision++
				} else {
					op.session.authorization.revision++
				}
			case "foreign_descriptor":
				copy := *stage.Descriptors[d.Address()].(*catalog.Relation)
				stage.Descriptors[d.Address()] = &copy
			}
			beforeCatalog, beforeStore, beforeFrontier := e.catalog.state, e.storage.Relations[d.Address()], e.commitSeq
			_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
			if err == nil || e.catalog.state != beforeCatalog || e.storage.Relations[d.Address()] != beforeStore || e.commitSeq != beforeFrontier {
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
			store := e.storage.Relations[d.Address()]
			switch phase {
			case "row":
				e.storage.SetFrontierLocked(math.MaxUint64, e.storage.VersionFrontier())
			case "version":
				e.storage.SetFrontierLocked(e.storage.RowFrontier(), math.MaxUint64)
			case "revision":
				d.ObjectRevision = ids.DefinitionRevision(math.MaxUint64)
			case "generation":
				store.Generation = math.MaxUint64
			}
			stage := e.BeginCatalogStage(e.CatalogRead())
			beforeRow, beforeVersion := e.storage.RowFrontier(), e.storage.VersionFrontier()
			if err := e.stageMaterializedRows(context.Background(), stage, d, store, refreshUnitRows(t, d, 1), true, ids.TxRef{}); err == nil {
				t.Fatal("overflow accepted")
			}
			if e.storage.RowFrontier() != beforeRow || e.storage.VersionFrontier() != beforeVersion || e.storage.Relations[d.Address()] != store || e.catalog.state.Get(d.Address()) != d {
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
			old := e.storage.Relations[d.Address()]
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			if err := e.stageMaterializedRows(context.Background(), stage, d, old, refreshUnitRows(t, d, 4), true, ids.TxRef{}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("materialized provider rejected")
			e.catalog.Providers[ids.RelationClassID] = queryCreateProbeProvider{Provider: e.catalog.Providers[ids.RelationClassID], e: e, t: t, check: func() error {
				switch phase {
				case "reject":
					return sentinel
				case "cancel":
					cancel()
				case "predecessor":
					e.mu.Lock()
					old.Generation++
					e.mu.Unlock()
				}
				return nil
			}}
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err == nil {
				_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
			}
			if err == nil || e.catalog.state.Get(d.Address()) != d || e.storage.Relations[d.Address()] != old || len(old.Versions) != 3 {
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
