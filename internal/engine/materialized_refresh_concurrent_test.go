package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// Fixtures call the real private binder/stager/root builders, not SQL execution.
func concurrentUnitRelation(t *testing.T, e *Engine) *catalog.Relation {
	t.Helper()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	statements, err := parser.Parse("CREATE MATERIALIZED VIEW unit_mv AS SELECT 1::int AS i,NULL::int AS p WITH NO DATA")
	if err != nil {
		t.Fatal(err)
	}
	stmt := statements[0].(*parser.CreateQueryTable)
	input, _ := sessionPresentation(op.Session)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	b, err := describe(context.Background(), e.analysisHost(op), stmt, auth, ids.ObjectRef{}, input, nil, nil)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	stage, d, skipped, err := e.prepareQueryTableLocked(op, auth, stmt, b)
	e.mu.Unlock()
	if err != nil || skipped {
		t.Fatal("materialized stage", err, skipped)
	}
	d.Populated = true
	if err := e.stageQueryTableRows(context.Background(), stage, d, nil, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	return d
}

func concurrentUnitRows(t *testing.T, d *catalog.Relation, payloads ...any) [][]sqltype.Value {
	t.Helper()
	rows := make([][]sqltype.Value, len(payloads))
	for i, p := range payloads {
		key, err := sqltype.Integer(d.Columns[0].Usage.Type, 1)
		if err != nil {
			t.Fatal(err)
		}
		value := sqltype.Null(d.Columns[1].Usage.Type)
		if p != nil {
			value, err = sqltype.Integer(d.Columns[1].Usage.Type, p.(int64))
			if err != nil {
				t.Fatal(err)
			}
		}
		rows[i] = []sqltype.Value{key, value}
	}
	return rows
}

func concurrentUnitReplace(t *testing.T, e *Engine, d *catalog.Relation, rows [][]sqltype.Value, concurrent, populated bool) *catalog.Relation {
	t.Helper()
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := e.stageMaterializedRows(context.Background(), stage, d, e.storage.Relations[d.Address()], rows, populated, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	stage.materialized().replacement.concurrent = concurrent
	queryCreatePublish(t, e, stage)
	return e.catalog.state.MustRelation(d.Address())
}

func materializedConcurrentUnitIndex(t *testing.T, e *Engine, parent *catalog.Relation, name, keys, suffix string) *catalog.Relation {
	t.Helper()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	statements, err := parser.Parse("CREATE UNIQUE INDEX " + name + " ON " + parent.Name() + "(" + keys + ")" + suffix)
	if err != nil {
		t.Fatal(err)
	}
	stmt := statements[0].(*parser.CreateIndex)
	input, _ := sessionPresentation(op.Session)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	definition, columns, attributes, lookup, err := e.bindIndexAttributes(op, input, auth, parent, stmt)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	definition.Unique, definition.NullsNotDistinct = true, stmt.NullsNotDistinct
	definition.Predicate, err = e.bindIndexPredicate(op, input, auth, parent, stmt.Predicate)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	oid, err := e.catalog.allocateOIDLocked()
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	revision, err := e.catalog.revisionLocked()
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	for i := range columns {
		columns[i].OriginTable = uint32(oid)
		columns[i].OriginAttribute = int16(i + 1)
	}
	index := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: parent.Namespace(), ObjectName: name, ObjectRevision: revision, Owner: parent.Owner, Columns: columns, Attributes: attributes, Index: lookup, RelationKind: catalog.IndexRelation, SQLIndex: definition, ColumnMeta: catalog.ColumnMetadata{HighWater: int32(len(attributes))}}
	stage := e.BeginCatalogStage(auth.Catalog)
	err = stage.Create(index)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	queryCreatePublish(t, e, stage)
	return index
}

func materializedConcurrentUnitError(t *testing.T, err error, code, message string) *sqlstate.Error {
	t.Helper()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message {
		t.Fatal("diagnostic", err, code, message)
	}
	return diagnostic
}

func TestMaterializedRefreshConcurrentEligibility(t *testing.T) {
	e := New()
	d := concurrentUnitRelation(t, e)
	want := `cannot refresh materialized view "public.unit_mv" concurrently`
	diagnostic := materializedConcurrentUnitError(t, materializedConcurrentAdmission(e.CatalogRead(), d, false), "55000", want)
	if diagnostic.Hint != "Create a unique index with no WHERE clause on one or more columns of the materialized view." {
		t.Fatal("eligibility hint", diagnostic)
	}
	index := materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", " INCLUDE(p)")
	d = e.catalog.state.MustRelation(d.Address())
	for _, phase := range []string{"eligible", "nonunique", "invalid_ready", "not_ready", "not_live", "partial", "expression", "system_key", "system_include", "empty_keys", "foreign_parent", "descending", "not_distinct"} {
		t.Run(phase, func(t *testing.T) {
			copy := *index
			definition := *index.SQLIndex
			definition.Keys = append([]catalog.IndexKey(nil), definition.Keys...)
			copy.SQLIndex = &definition
			eligible := false
			switch phase {
			case "eligible":
				eligible = true
			case "nonunique":
				definition.Unique = false
			case "invalid_ready":
				definition.Valid = false
			case "not_ready":
				definition.Ready = false
			case "not_live":
				definition.Live = false
			case "partial":
				definition.Predicate = &catalog.IndexPredicate{}
			case "expression":
				definition.Keys[0].Derived = &catalog.DerivedIndexKey{}
			case "system_key":
				definition.Keys[0].Attribute = -1
			case "system_include":
				definition.Include = []int32{-1}
			case "empty_keys":
				definition.Keys = nil
			case "foreign_parent":
				definition.Parent.ObjectID++
			case "descending":
				definition.Keys[0].Desc = true
				eligible = true
			case "not_distinct":
				definition.NullsNotDistinct = true
				eligible = true
			}
			if materializedIndexEligible(d, &copy) != eligible {
				t.Fatal("descriptor eligibility", phase)
			}
		})
	}
	if err := materializedConcurrentAdmission(e.CatalogRead(), d, false); err != nil {
		t.Fatal(err)
	}
	materializedConcurrentUnitIndex(t, e, d, "unit_partial", "i,p", " WHERE i>0")
	d = e.catalog.state.MustRelation(d.Address())
	if err := materializedConcurrentAdmission(e.CatalogRead(), d, false); err != nil {
		t.Fatal("any eligible index", err)
	}
	materializedConcurrentUnitError(t, materializedConcurrentAdmission(e.CatalogRead(), d, true), "42601", "CONCURRENTLY and WITH NO DATA options cannot be used together")
	unpopulated := *d
	unpopulated.Populated = false
	materializedConcurrentUnitError(t, materializedConcurrentAdmission(e.CatalogRead(), &unpopulated, true), "0A000", "CONCURRENTLY cannot be used when the materialized view is not populated")
}

func TestMaterializedRefreshConcurrentDuplicatePreflight(t *testing.T) {
	e := New()
	d := concurrentUnitRelation(t, e)
	index := materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
	d = e.catalog.state.MustRelation(d.Address())
	ordering, err := bindIndexOrdering(d, index.SQLIndex)
	if err != nil {
		t.Fatal(err)
	}
	rows := concurrentUnitRows(t, d, int64(10), int64(20), int64(10))
	entries, err := materializedNewEntries(context.Background(), ordering, rows)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic := materializedConcurrentUnitError(t, materializedDuplicatePreflight(context.Background(), e.CatalogRead(), d, ordering, entries, rows, sqltype.OutputEnvironment{}), "21000", `new data for materialized view "unit_mv" contains duplicate rows without any null columns`)
	if diagnostic.Detail != "Row: (1,10)" {
		t.Fatal("duplicate detail", diagnostic)
	}
	nullable := concurrentUnitRows(t, d, nil, nil)
	entries, err = materializedNewEntries(context.Background(), ordering, nullable)
	if err != nil {
		t.Fatal(err)
	}
	if err := materializedDuplicatePreflight(context.Background(), e.CatalogRead(), d, ordering, entries, nullable, sqltype.OutputEnvironment{}); err != nil {
		t.Fatal("NULL payload preflight", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := materializedNewEntries(ctx, ordering, rows); !errors.Is(err, context.Canceled) {
		t.Fatal("preflight cancel", err)
	}
	for _, id := range []sqltype.ID{sqltype.Numeric, sqltype.Float8, sqltype.JSON, sqltype.BPChar} {
		typ, _ := e.CatalogRead().LookupType(id)
		usage := typ.Usage()
		var a, b sqltype.Value
		switch id {
		case sqltype.Numeric:
			a, err = typ.Input(context.Background(), "1.0", usage.Typmod)
			if err == nil {
				b, err = typ.Input(context.Background(), "1.00", usage.Typmod)
			}
		case sqltype.Float8:
			a, err = sqltype.Float(typ, 0)
			if err == nil {
				b, err = sqltype.Float(typ, math.Copysign(0, -1))
			}
		default:
			text := "image"
			if id == sqltype.JSON {
				text = `{"x":1}`
			}
			a, err = typ.Input(context.Background(), text, usage.Typmod)
			if err == nil {
				b, err = typ.Input(context.Background(), text, usage.Typmod)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		same := id == sqltype.JSON || id == sqltype.BPChar
		if materializedSameRow([]sqltype.Value{a}, []sqltype.Value{b}) != same {
			t.Fatal("image distinction", id)
		}
	}
}

func TestMaterializedRefreshConcurrentMatch(t *testing.T) {
	for _, phase := range []string{"matched_once", "unmatched_null", "all_keys", "include", "null_key", "not_distinct_null_key"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			d := concurrentUnitRelation(t, e)
			oldRows := concurrentUnitRows(t, d, nil)
			d = concurrentUnitReplace(t, e, d, oldRows, false, true)
			keys, suffix := "i", ""
			if phase == "include" {
				suffix = " INCLUDE(p)"
			}
			if phase == "null_key" || phase == "not_distinct_null_key" {
				keys = "p"
			}
			if phase == "not_distinct_null_key" {
				suffix = " NULLS NOT DISTINCT"
			}
			first := materializedConcurrentUnitIndex(t, e, d, "unit_u", keys, suffix)
			d = e.catalog.state.MustRelation(d.Address())
			indices := []*catalog.Relation{first}
			if phase == "all_keys" {
				indices = append(indices, materializedConcurrentUnitIndex(t, e, d, "unit_second", "i,p", ""))
			}
			produced := concurrentUnitRows(t, d, nil, nil)
			if phase == "unmatched_null" {
				produced = concurrentUnitRows(t, d, int64(20))
				produced = append(produced, concurrentUnitRows(t, d, nil)...)
			}
			old := e.storage.Relations[d.Address()]
			frontier := e.visibility.CommitSeq()
			effective, err := materializedConcurrentRows(context.Background(), e.CatalogRead(), d, old, frontier, e.storage.IndexRoots[first.Address()].Entries, indices, produced, sqltype.OutputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			expected := 1
			if phase == "unmatched_null" || phase == "all_keys" || phase == "null_key" || phase == "not_distinct_null_key" {
				expected = 2
			}
			if len(effective) != expected || e.visibility.CommitSeq() != frontier || e.storage.Relations[d.Address()] != old || old.MaterializedLineage != nil {
				t.Fatal("effective bag/staging ownership", phase, len(effective))
			}
			if expected == 1 && &effective[0][0] != &old.Versions[0].Values[0] {
				t.Fatal("old matched datum buffer copied or replaced")
			}
			ordering, err := bindIndexOrdering(d, first.SQLIndex)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := materializedNewEntries(context.Background(), ordering, effective)
			if err != nil {
				t.Fatal(err)
			}
			err = validateUniqueEntries(context.Background(), d, first, entries, "public", sqltype.OutputEnvironment{}, nil, nil, false, ordering)
			violation := phase == "unmatched_null" || phase == "all_keys" || phase == "not_distinct_null_key"
			if violation {
				materializedConcurrentUnitError(t, err, "23505", `duplicate key value violates unique constraint "unit_u"`)
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMaterializedRefreshConcurrentPublication(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	index := materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
	d = e.catalog.state.MustRelation(d.Address())
	first := e.storage.Relations[d.Address()]
	versions, ends := first.Versions, first.Ends
	frontier := e.visibility.CommitSeq()
	for _, count := range []int{4, 0, 2} {
		old := e.storage.Relations[d.Address()]
		stage := e.BeginCatalogStage(e.CatalogRead())
		rows := refreshUnitRows(t, d, count)
		if err := e.stageMaterializedRows(context.Background(), stage, d, old, rows, true, ids.TxRef{}); err != nil {
			t.Fatal(err)
		}
		stage.materialized().replacement.concurrent = true
		candidate := stage.materializedOrZero().replacement.storage
		if old.MaterializedLineage != first.MaterializedLineage || candidate.MaterializedLineage != nil {
			t.Fatal("stage stamped lineage")
		}
		if count != 0 && &candidate.Versions[0].Values[0] != &rows[0][0] {
			t.Fatal("candidate copied owned buffers")
		}
		queryCreatePublish(t, e, stage)
		e.DiscardCatalogStage(stage)
		d = e.catalog.state.MustRelation(d.Address())
		frontier++
		if e.visibility.CommitSeq() != frontier || !d.Populated || e.storage.Relations[d.Address()] != candidate || !sameMaterializedLineage(d, d, first, candidate) || len(e.storage.IndexRoots[index.Address()].Entries) != count {
			t.Fatal("atomic roots/frontier/lineage")
		}
	}
	if !sameVersionRoot(first.Versions, versions) || first.Ends != ends {
		t.Fatal("retired root changed")
	}
	d = concurrentUnitReplace(t, e, d, nil, false, false)
	if sameMaterializedLineage(d, d, first, e.storage.Relations[d.Address()]) || e.storage.Relations[d.Address()].MaterializedLineage != nil {
		t.Fatal("plain NO DATA bridged lineage")
	}
}

func TestMaterializedRefreshConcurrentCancellation(t *testing.T) {
	for _, phase := range []string{"provider", "cancel", "generation", "length", "ends", "storage", "definition", "index_set", "frontier", "revision"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			d := refreshUnitRelation(t, e)
			materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
			d = e.catalog.state.MustRelation(d.Address())
			old := e.storage.Relations[d.Address()]
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			if phase == "revision" {
				d.ObjectRevision = ids.DefinitionRevision(math.MaxUint64)
				if err := e.stageMaterializedRows(context.Background(), stage, d, old, refreshUnitRows(t, d, 4), true, ids.TxRef{}); err == nil {
					t.Fatal("revision overflow accepted")
				}
				if old.MaterializedLineage != nil {
					t.Fatal("overflow stamped")
				}
				return
			}
			if err := e.stageMaterializedRows(context.Background(), stage, d, old, refreshUnitRows(t, d, 4), true, ids.TxRef{}); err != nil {
				t.Fatal(err)
			}
			stage.materialized().replacement.concurrent = true
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fault := errors.New("provider veto")
			if phase == "provider" {
				e.catalog.Providers[ids.RelationClassID] = queryCreateProbeProvider{Provider: e.catalog.Providers[ids.RelationClassID], e: e, t: t, check: func() error { return fault }}
			}
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err == nil {
				switch phase {
				case "cancel":
					cancel()
				case "generation":
					old.Generation++
				case "length":
					old.Versions = old.Versions[:2]
				case "ends":
					old.Ends = &storage.DeletionRoot{}
				case "storage":
					copy := *old
					e.mu.Lock()
					e.storage.InstallRelationLocked(d.Address(), &copy)
					e.mu.Unlock()
				case "definition":
					copy := *d
					copy.Owner = authorization.PrincipalRef{}
					e.catalog.state.SetDescriptor(d.Address(), &copy)
				case "index_set":
					materializedConcurrentUnitIndex(t, e, d, "unit_second", "i", "")
				case "frontier":
					e.visibility.SetCommitSeqLocked(ids.CommitSeq(math.MaxUint64))
				}
				beforeCatalog, beforeStore, beforeFrontier := e.catalog.state, e.storage.Relations[d.Address()], e.visibility.CommitSeq()
				_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
				if e.catalog.state != beforeCatalog || e.storage.Relations[d.Address()] != beforeStore || e.visibility.CommitSeq() != beforeFrontier {
					t.Fatal("veto changed publication")
				}
			}
			if err == nil || old.MaterializedLineage != nil || stage.materializedOrZero().replacement.storage.MaterializedLineage != nil {
				t.Fatal("failed candidate stamped lineage", phase, err)
			}
			if phase == "provider" && !errors.Is(err, fault) {
				t.Fatal(err)
			}
			if phase == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestMaterializedRefreshConcurrentUniqueMode(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	index := materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
	d = e.catalog.state.MustRelation(d.Address())
	rows := refreshUnitRows(t, d, 2)
	rows[1][0] = rows[0][0]
	for _, concurrent := range []bool{false, true} {
		stage := e.BeginCatalogStage(e.CatalogRead())
		if err := e.stageMaterializedRows(context.Background(), stage, d, e.storage.Relations[d.Address()], rows, true, ids.TxRef{}); err != nil {
			t.Fatal(err)
		}
		stage.materialized().replacement.concurrent = concurrent
		prepared, err := e.ValidateCatalogStage(context.Background(), stage)
		if err == nil {
			_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
		}
		message := fmt.Sprintf(`could not create unique index "%s"`, index.Name())
		if concurrent {
			message = fmt.Sprintf(`duplicate key value violates unique constraint "%s"`, index.Name())
		}
		materializedConcurrentUnitError(t, err, "23505", message)
		if e.storage.Relations[d.Address()].MaterializedLineage != nil {
			t.Fatal("integrity failure stamped")
		}
		e.DiscardCatalogStage(stage)
	}
	if !reflect.DeepEqual(e.storage.Relations[d.Address()].Versions[0].Values, refreshUnitRows(t, d, 1)[0]) {
		t.Fatal("uniqueness failure changed predecessor")
	}
}

func TestMaterializedRefreshConcurrentIndexSetIntent(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	index := materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
	d = e.catalog.state.MustRelation(d.Address())
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := e.stageMaterializedRows(context.Background(), stage, d, e.storage.Relations[d.Address()], refreshUnitRows(t, d, 2), true, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	stage.materialized().replacement.concurrent = true
	if len(stage.indexesOrZero().rebuilds) != 0 {
		t.Fatal("replacement forged REINDEX intent")
	}
	if err := stage.validateIndexMaintenance(e.catalog.state); err != nil {
		t.Fatal("exact replacement membership rejected", err)
	}
	maintenance := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(maintenance)
	maintenance.indexes().sets = map[ids.ObjectAddress]int{d.Address(): 1}
	if err := maintenance.validateIndexMaintenance(e.catalog.state); !errors.As(err, new(catalog.Conflict)) {
		t.Fatal("ordinary maintenance accepted missing rebuild intent", err)
	}
	current := catalog.CloneState(e.catalog.state)
	changed := *index
	changed.ObjectRevision++
	current.SetDescriptor(index.Address(), &changed)
	if err := stage.validateIndexMaintenance(current); !errors.As(err, new(catalog.Conflict)) {
		t.Fatal("same-count replacement accepted changed index identity", err)
	}
	if err := stage.materializedReplacementCurrent(current, e.storage.Relations[d.Address()]); !errors.As(err, new(catalog.Conflict)) {
		t.Fatal("replacement lost final exact index identity fence", err)
	}
}
