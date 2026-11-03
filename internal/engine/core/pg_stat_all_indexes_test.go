package core_test

import (
	"context"
	"reflect"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func indexStatsRows(t *testing.T, s *session.Session, q string) [][]any {
	t.Helper()
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	r, err := core.EngineOf(s).Execute(op, enginetest.Statement(t, q), 0, nil)
	if err != nil {
		t.Fatal(q, err)
	}
	if r.Rows == nil {
		t.Fatal("missing rows", q)
	}
	defer r.Rows.Close()
	cursor := enginetest.IndependentFinalRows(r.Rows)
	defer cursor.Close()
	rows := make([][]any, 0, cursor.Count)
	values := make([]sqltype.Value, len(cursor.Schema()))
	for {
		present, err := cursor.Next(op.Ctx, values)
		if err != nil {
			t.Fatal(q, err)
		}
		if !present {
			break
		}
		row := make([]any, len(values))
		for i, value := range values {
			if value.IsNull() {
				continue
			}
			switch value.Type().ID() {
			case sqltype.Oid:
				oid, err := value.OID()
				if err != nil {
					t.Fatal(q, err)
				}
				row[i] = int64(oid)
			case sqltype.Timestamptz:
				instant, err := value.TimestampTime()
				if err != nil {
					t.Fatal(q, err)
				}
				row[i] = instant
			default:
				row[i] = enginetest.Export(value)
			}
		}
		rows = append(rows, row)
	}
	if err := cursor.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	return rows
}
func TestPGStatAllIndexesCatalog(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	if got := indexStatsRows(t, s, "SELECT * FROM pg_stat_all_indexes"); len(got) != 0 {
		t.Fatal("fabricated bootstrap inventory", got)
	}
	statsFunctionExec(t, s, "CREATE TABLE stat_catalog(n integer)", "CREATE INDEX stat_catalog_a ON stat_catalog(n)", "CREATE INDEX stat_catalog_b ON stat_catalog(n)", "CREATE TEMP TABLE stat_temp(n integer)", "CREATE INDEX stat_temp_idx ON stat_temp(n)", "CREATE UNLOGGED TABLE stat_unlogged(n integer)", "CREATE INDEX stat_unlogged_idx ON stat_unlogged(n)", "CREATE MATERIALIZED VIEW stat_mat AS SELECT n FROM stat_catalog", "CREATE INDEX stat_mat_idx ON stat_mat(n)", "CREATE TABLE stat_partition(n integer) PARTITION BY RANGE(n)", "CREATE TABLE stat_leaf PARTITION OF stat_partition FOR VALUES FROM(0) TO(10)", "CREATE INDEX stat_leaf_idx ON stat_leaf(n)", "CREATE VIEW stat_view AS SELECT n FROM stat_catalog", "CREATE SEQUENCE stat_sequence")
	read := e.CatalogRead()
	view := read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatAllIndexesOID})
	names := []string{"relid", "indexrelid", "schemaname", "relname", "indexrelname", "idx_scan", "last_idx_scan", "idx_tup_read", "idx_tup_fetch"}
	types := []sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Name, sqltype.Name, sqltype.Name, sqltype.Int8, sqltype.Timestamptz, sqltype.Int8, sqltype.Int8}
	if len(view.Columns) != 9 || !view.VirtualView || view.RowType != 12189 || pgcatalog.BuiltinViewEvents(pgcatalog.StatAllIndexesOID, nil) != 0 {
		t.Fatal("view identity/shape", view)
	}
	for i, c := range view.Columns {
		if c.Name != names[i] || c.Usage.Type.ID() != types[i] || !c.Nullable || !c.NullabilityKnown || c.OriginTable != uint32(pgcatalog.StatAllIndexesOID) || c.OriginAttribute != int16(i+1) || c.Usage.Typmod != -1 {
			t.Fatal("typed nullable origin", i, c)
		}
	}
	for _, spec := range pgcatalog.Specs {
		if spec.Name == "pg_stat_sys_indexes" || spec.Name == "pg_stat_user_indexes" {
			t.Fatal("neighbor view registered")
		}
	}
	if def := pgcatalog.SummaryViewDefinition(pgcatalog.StatAllIndexesOID); !strings.Contains(def, "pg_index") || strings.Contains(def, "stats_reset") {
		t.Fatal("display definition", def)
	}
	rows := indexStatsRows(t, s, "SELECT * FROM pg_stat_all_indexes")
	seen := map[int64]bool{}
	expected := 0
	for _, d := range read.Relations() {
		if d.SQLIndex == nil {
			continue
		}
		parent, ok := read.Relation(d.SQLIndex.Parent)
		if !ok {
			t.Fatal("orphan index")
		}
		kind := pgcatalog.ClassKind(read, parent)
		if kind == "r" || kind == "t" || kind == "m" {
			expected++
		}
	}
	if len(rows) != expected {
		t.Fatal("actual index inventory", len(rows), expected)
	}
	for _, row := range rows {
		oid := row[1].(int64)
		if seen[oid] || row[5] != int64(0) || row[6] != nil || row[7] != int64(0) || row[8] != int64(0) {
			t.Fatal("duplicate/fabricated fresh activity", row)
		}
		seen[oid] = true
		index := read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)})
		if int64(index.SQLIndex.Parent.ObjectID) != row[0] {
			t.Fatal("wrong parent", row)
		}
	}
	leaf := enginetest.RoleTableRef(t, e, "stat_leaf")
	leafIndex := enginetest.RoleTableRef(t, e, "stat_leaf_idx")
	if !seen[int64(leafIndex.Address.ObjectID)] || read.MustRelation(leafIndex.Address).SQLIndex.Parent != leaf.Address || pgcatalog.ClassKind(read, read.MustRelation(leaf.Address)) != "r" {
		t.Fatal("actual partition leaf index missing or misidentified", leaf, leafIndex, rows)
	}
	a := enginetest.RoleTableRef(t, e, "stat_catalog_a")
	b := enginetest.RoleTableRef(t, e, "stat_catalog_b")
	if a == b {
		t.Fatal("index identities collapsed")
	}
	all := indexStatsRows(t, s, "SELECT indexrelid FROM pg_stat_all_indexes ORDER BY indexrelid")
	sys := indexStatsRows(t, s, "SELECT indexrelid FROM pg_stat_all_indexes WHERE schemaname IN ('pg_catalog','information_schema') OR schemaname ~ '^pg_toast' ORDER BY indexrelid")
	user := indexStatsRows(t, s, "SELECT indexrelid FROM pg_stat_all_indexes WHERE schemaname NOT IN ('pg_catalog','information_schema') AND schemaname !~ '^pg_toast' ORDER BY indexrelid")
	if len(sys)+len(user) != len(all) {
		t.Fatal("sys/user partition", all, sys, user)
	}
	indexStatsCatalogKinds(t, e, s, a, enginetest.RoleTableRef(t, e, "stat_partition"))
}

func TestPGStatAllIndexesSourceSelection(t *testing.T) {
	for _, mode := range []string{"cache", "snapshot", "none"} {
		for _, column := range []int{0, 1, 2, 3, 4} {
			t.Run(mode+"/"+[]string{"relid", "indexrelid", "schemaname", "relname", "indexrelname"}[column], func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				statsFunctionExec(t, s, "CREATE TABLE index_select_x(n integer)", "CREATE TABLE index_select_y(n integer)", "CREATE INDEX index_select_a ON index_select_x(n)", "CREATE INDEX index_select_b ON index_select_y(n)")
				x, y := enginetest.RoleTableRef(t, e, "index_select_a"), enginetest.RoleTableRef(t, e, "index_select_b")
				configureStats(s, mode)
				var w statistics.Writer
				w.IndexAccess(&e.Statistics, statistics.Key{Object: x}, statistics.Policy{Counts: true}, 1, 2, 2)
				w.Flush(&e.Statistics, time.Now(), true)
				enginetest.TransactionBegin(t, s)
				op := enginetest.TransactionOperation(t, s)
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(nil)
				e.Mu.Lock()
				auth, err := e.AuthorizationLocked(op)
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				read := auth.Catalog
				address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatAllIndexesOID}
				source := read.MustRelation(address)
				runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth, Settings: s.Settings})
				produce, _ := pgcatalog.ProducerFor(pgcatalog.StatAllIndexesOID)
				in := pgcatalog.Input{Context: context.Background(), Catalog: read, Roles: auth.Roles, Owner: &e.Catalog.Owner, Ref: e.Catalog.Domain.Ref(read.Epoch(), address), Source: source, Runtime: runtime}
				// schema filtering uses a deliberately impossible namespace to prove neither
				// index is fetched, rather than expecting two public parents to differ.
				in.Keep = pgcatalog.Keep{Column: column, Test: func(v sqltype.Value) bool {
					switch column {
					case 0:
						n, _ := v.OID()
						return ids.OID(n) == read.MustRelation(x.Address).SQLIndex.Parent.ObjectID
					case 1:
						n, _ := v.OID()
						return ids.OID(n) == x.Address.ObjectID
					case 2:
						return false
					case 3:
						n, _ := v.Text()
						return n == "index_select_x"
					default:
						n, _ := v.Text()
						return n == "index_select_a"
					}
				}}
				rows, err := produce(in)
				wantRows := 1
				if column == 2 {
					wantRows = 0
				}
				if err != nil || len(rows) != wantRows {
					t.Fatal("filtered source", rows, err)
				}
				w.IndexAccess(&e.Statistics, statistics.Key{Object: x}, statistics.Policy{Counts: true}, 2, 0, 0)
				w.IndexAccess(&e.Statistics, statistics.Key{Object: y}, statistics.Policy{Counts: true}, 2, 0, 0)
				w.Flush(&e.Statistics, time.Now(), true)
				wantY := int64(2)
				if mode == "snapshot" && column != 2 {
					wantY = 0
				}
				if got := runtime.IndexStatistics(y.Address).Scans; got != wantY {
					t.Fatal("rejected index fetched before Keep", got, wantY)
				}
				wantX := int64(1)
				if mode == "none" || column == 2 {
					wantX = 3
				}
				if got := runtime.IndexStatistics(x.Address).Scans; got != wantX {
					t.Fatal("accessed key did not follow Reader mode", got, wantX)
				}
				w.IndexAccess(&e.Statistics, statistics.Key{Object: x}, statistics.Policy{Counts: true}, 1, 0, 0)
				if column != 2 {
					if n, _ := rows[0][5].Integer(); n != 1 {
						t.Fatal("retained row mutated", rows)
					}
				}
				ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
				if _, err := ctx.StatsSnapshotValue(true, enginetest.Usage(e, sqltype.Void).Type); err != nil {
					t.Fatal(err)
				}
				if got := runtime.IndexStatistics(x.Address).Scans; got != 3 {
					t.Fatal("clear did not refresh shared Reader", got)
				}
				if got := e.Statistics.Read(statistics.Key{Object: x}).IndexScans; got != 3 {
					t.Fatal("clear published pending index work", got)
				}
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestPGStatAllIndexesLifetime(t *testing.T) {
	e, s, key := indexStatsSetup(t)
	statsFunctionExec(t, s, "SELECT payload FROM stat_index_rows WHERE n=2", "CREATE TABLE stat_survivor(n integer)", "INSERT INTO stat_survivor VALUES(2),(2)", "CREATE INDEX stat_survivor_hash ON stat_survivor USING hash(n)", "SELECT n FROM stat_survivor WHERE n=2")
	survivor := statistics.Key{Object: enginetest.RoleTableRef(t, e, "stat_survivor_hash")}
	statsFunctionRetire(t, s)
	indexStatsWant(t, e, key, 1, 2, 2)
	indexStatsWant(t, e, survivor, 1, 2, 2)
	actor := enginetest.StartSession(t, e, nil)
	retained := indexStatsRows(t, actor, "SELECT * FROM pg_stat_all_indexes WHERE indexrelname='stat_index_hash'")
	stamp, ok := retained[0][6].(time.Time)
	if !ok || !stamp.Equal(e.Statistics.Read(key).LastIndexScan) {
		t.Fatal("retained last scan lost its typed provider instant", retained[0][6])
	}
	before := append([]any(nil), retained[0]...)
	statsFunctionExec(t, actor, "BEGIN", "DROP INDEX stat_index_hash", "ROLLBACK")
	indexStatsWant(t, e, key, 1, 2, 2)
	statsFunctionExec(t, actor, "ALTER INDEX stat_index_hash RENAME TO stat_index_renamed", "CREATE SCHEMA stat_index_schema", "ALTER TABLE stat_index_rows SET SCHEMA stat_index_schema")
	rows := indexStatsRows(t, actor, "SELECT indexrelid,schemaname,indexrelname,idx_scan FROM pg_stat_all_indexes WHERE indexrelname='stat_index_renamed'")
	if !reflect.DeepEqual(rows, [][]any{{int64(key.Object.Address.ObjectID), "stat_index_schema", "stat_index_renamed", int64(1)}}) {
		t.Fatal("rename/move history", rows)
	}
	var late statistics.Writer
	late.IndexAccess(&e.Statistics, key, statistics.Policy{Counts: true}, 1, 2, 2)
	statsFunctionExec(t, actor, "DROP INDEX stat_index_schema.stat_index_renamed")
	late.Flush(&e.Statistics, time.Now(), true)
	indexStatsWant(t, e, key, 0, 0, 0)
	if got := indexStatsRows(t, actor, "SELECT * FROM pg_stat_all_indexes WHERE indexrelname='stat_index_renamed'"); len(got) != 0 {
		t.Fatal("dropped index remains", got)
	}
	statsFunctionExec(t, actor, "CREATE INDEX stat_index_renamed ON stat_index_schema.stat_index_rows USING hash(n)")
	var successor ids.ObjectRef
	for _, d := range e.CatalogRead().Relations() {
		if d.Name() == "stat_index_renamed" {
			successor = e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), d.Address())
		}
	}
	if successor == key.Object || successor == (ids.ObjectRef{}) {
		t.Fatal("recreate identity", successor, key)
	}
	indexStatsWant(t, e, statistics.Key{Object: successor}, 0, 0, 0)
	if !reflect.DeepEqual(retained[0], before) {
		t.Fatal("old SQL row mutated")
	}
	indexStatsWant(t, e, survivor, 1, 2, 2)
}

// Staged immutable descriptors exercise represented internal/unready identities
// without inventing public bootstrap indexes or changing installed inventory.
func indexStatsCatalogKinds(t *testing.T, e *core.Engine, s *session.Session, indexRef, partitionRef ids.ObjectRef) {
	t.Helper()
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	base := auth.Catalog
	original := base.MustRelation(indexRef.Address)
	parent := base.MustRelation(original.SQLIndex.Parent)
	partition := base.MustRelation(partitionRef.Address)
	if partition.RelationKind != catalog.PartitionedRelation || partition.Partition == nil {
		t.Fatal("partitioned-parent fixture lost its actual partition definition", partition)
	}
	for _, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.MaterializedViewRelation, catalog.PartitionedRelation, catalog.ForeignRelation, catalog.ViewRelation, catalog.SequenceRelation} {
		stage := e.BeginCatalogStage(base)
		copiedParent := *parent
		if kind == catalog.PartitionedRelation {
			// Parent-index DDL is unsupported for this access method. Compose
			// only its catalog identity against the real partitioned parent;
			// no index access path or physical root is created.
			copiedParent = *partition
		}
		copied := *original
		definition := *original.SQLIndex
		definition.Parent = copiedParent.Address()
		definition.Ready = false
		definition.Valid = false
		copied.SQLIndex = &definition
		copied.ObjectRevision++
		if err := stage.Replace(&copied); err != nil {
			t.Fatal(err)
		}
		copiedParent.RelationKind = kind
		copiedParent.ObjectRevision++
		if err := stage.Replace(&copiedParent); err != nil {
			t.Fatal(err)
		}
		namespace, _ := base.Namespace(copiedParent.Namespace())
		copiedNamespace := *namespace
		copiedNamespace.ObjectName = "pg_toast_extra"
		copiedNamespace.ObjectRevision++
		if err := stage.Replace(&copiedNamespace); err != nil {
			t.Fatal(err)
		}
		read := e.StageRead(stage)
		address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatAllIndexesOID}
		input := pgcatalog.Input{Context: context.Background(), Catalog: read, Roles: auth.Roles, Owner: &e.Catalog.Owner, Ref: e.Catalog.Domain.Ref(read.Epoch(), address), Source: read.MustRelation(address), Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth, Settings: s.Settings}), Keep: pgcatalog.Keep{Column: 1, Test: func(v sqltype.Value) bool { n, _ := v.OID(); return ids.OID(n) == indexRef.Address.ObjectID }}}
		produce, _ := pgcatalog.ProducerFor(pgcatalog.StatAllIndexesOID)
		rows, err := produce(input)
		if err != nil {
			t.Fatal(err)
		}
		included := kind == catalog.TableRelation || kind == catalog.MaterializedViewRelation
		if (len(rows) == 1) != included {
			t.Fatal("parent kind/readiness filter", kind, rows)
		}
		if included {
			name, _ := rows[0][2].Text()
			if name != "pg_toast_extra" || !strings.HasPrefix(name, "pg_toast") {
				t.Fatal("system prefix", name)
			}
		}
		e.DiscardCatalogStage(stage)
	}
}
