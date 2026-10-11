package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func refreshUnitRelation(t *testing.T, e *Engine) *relationDescriptor {
	t.Helper()
	_, done, stage, d := materializedUnitStage(t, e)
	defer done()
	d.populated = true
	rows := refreshUnitRows(t, d, 3)
	if err := e.stageQueryTableRows(context.Background(), stage, d, rows, TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	return d
}

func refreshUnitRows(t *testing.T, d *relationDescriptor, count int) [][]sqltype.Value {
	t.Helper()
	rows := make([][]sqltype.Value, count)
	for i := range rows {
		value, err := sqltype.Integer(d.columns[0].Usage.Type, int64(i+1))
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
		store := e.storage[d.address]
		root, ends, generation := store.versions, store.ends, store.generation
		frontier := e.commitSeq
		stage := e.BeginCatalogStage(e.CatalogRead())
		rows := refreshUnitRows(t, d, tc.count)
		if err := e.stageMaterializedRows(context.Background(), stage, d, store, rows, tc.populated, TxRef{}); err != nil {
			t.Fatal(err)
		}
		candidate := stage.materializedReplacement.storage
		if tc.count != 0 && &candidate.versions[0].values[0] != &rows[0][0] {
			t.Fatal("refresh copied finalized buffers")
		}
		if e.storage[d.address] != store || e.state.descriptors[d.address] != old {
			t.Fatal("staging leaked")
		}
		queryCreatePublish(t, e, stage)
		d = e.state.descriptors[d.address].(*relationDescriptor)
		if d == old || d.view != old.view || d.address != old.address || d.rowType != old.rowType || d.view.rule != old.view.rule || d.owner != old.owner || d.revision != old.revision+1 || !reflect.DeepEqual(d.acl, old.acl) || !reflect.DeepEqual(d.columnACL, old.columnACL) || !reflect.DeepEqual(d.columns, old.columns) || !reflect.DeepEqual(d.attributes, old.attributes) {
			t.Fatal("refresh lost immutable identity/metadata")
		}
		if e.storage[d.address] != candidate || len(candidate.versions) != tc.count || candidate.generation != generation+1 || d.populated != tc.populated || e.commitSeq != frontier+1 {
			t.Fatal("incomplete atomic replacement", d.populated, e.commitSeq, candidate)
		}
		if !sameVersionRoot(root, store.versions) || store.ends != ends || store.generation != generation {
			t.Fatal("predecessor mutated")
		}
		for _, row := range candidate.versions {
			if row.born != frontier+1 {
				t.Fatal("wrong row frontier", row)
			}
		}
		queryCreateAssertTypes(t, e, registry)
	}
}

func TestMaterializedRefreshSnapshots(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	old := e.storage[d.address]
	values := old.versions[0].values
	for _, populated := range []bool{true, false} {
		current := e.state.descriptors[d.address].(*relationDescriptor)
		stage := e.BeginCatalogStage(e.CatalogRead())
		if err := e.stageMaterializedRows(context.Background(), stage, current, e.storage[d.address], nil, populated, TxRef{}); err != nil {
			t.Fatal(err)
		}
		queryCreatePublish(t, e, stage)
		if len(old.versions) != 3 || &old.versions[0].values[0] != &values[0] || !sqltype.SameDatum(old.versions[0].values[0], values[0]) || old.ends != nil {
			t.Fatal("retained storage changed")
		}
	}
}

func TestMaterializedRefreshPublicationFences(t *testing.T) {
	for _, phase := range []string{"generation", "length", "ends", "store", "descriptor", "epoch", "cancel", "frontier", "source", "role", "authorization", "foreign_descriptor", "wrong_width", "wrong_type"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			d := refreshUnitRelation(t, e)
			store := e.storage[d.address]
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			rows := refreshUnitRows(t, d, 4)
			if phase == "wrong_width" {
				rows[0] = nil
			}
			if phase == "wrong_type" {
				rows[0][0] = sqltype.Null(testUsage(e, sqltype.Text).Type)
			}
			err := e.stageMaterializedRows(context.Background(), stage, d, store, rows, true, TxRef{})
			if phase == "wrong_width" || phase == "wrong_type" {
				if err == nil || e.storage[d.address] != store || e.state.descriptors[d.address] != d {
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
				store.generation++
			case "length":
				store.versions = store.versions[:2]
			case "ends":
				store.ends = &deletionRoot{}
			case "store":
				copy := *store
				e.storage[d.address] = &copy
			case "descriptor":
				copy := *d
				copy.revision++
				e.state.descriptors[d.address] = &copy
			case "epoch":
				e.state.epoch++
			case "cancel":
				cancel()
			case "frontier":
				e.commitSeq = CommitSeq(math.MaxUint64)
			case "source":
				stage.querySources = []Descriptor{&relationDescriptor{address: d.address}}
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
				copy := *stage.descriptors[d.address].(*relationDescriptor)
				stage.descriptors[d.address] = &copy
			}
			beforeCatalog, beforeStore, beforeFrontier := e.state, e.storage[d.address], e.commitSeq
			_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()})
			if err == nil || e.state != beforeCatalog || e.storage[d.address] != beforeStore || e.commitSeq != beforeFrontier {
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
			store := e.storage[d.address]
			switch phase {
			case "row":
				e.nextRow = math.MaxUint64
			case "version":
				e.nextVersion = math.MaxUint64
			case "revision":
				d.revision = DefinitionRevision(math.MaxUint64)
			case "generation":
				store.generation = math.MaxUint64
			}
			stage := e.BeginCatalogStage(e.CatalogRead())
			beforeRow, beforeVersion := e.nextRow, e.nextVersion
			if err := e.stageMaterializedRows(context.Background(), stage, d, store, refreshUnitRows(t, d, 1), true, TxRef{}); err == nil {
				t.Fatal("overflow accepted")
			}
			if e.nextRow != beforeRow || e.nextVersion != beforeVersion || e.storage[d.address] != store || e.state.descriptors[d.address] != d {
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
			old := e.storage[d.address]
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			if err := e.stageMaterializedRows(context.Background(), stage, d, old, refreshUnitRows(t, d, 4), true, TxRef{}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("materialized provider rejected")
			e.providers[RelationClassID] = queryCreateProbeProvider{CatalogProvider: e.providers[RelationClassID], e: e, t: t, check: func() error {
				switch phase {
				case "reject":
					return sentinel
				case "cancel":
					cancel()
				case "predecessor":
					e.mu.Lock()
					old.generation++
					e.mu.Unlock()
				}
				return nil
			}}
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err == nil {
				_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()})
			}
			if err == nil || e.state.descriptors[d.address] != d || e.storage[d.address] != old || len(old.versions) != 3 {
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
