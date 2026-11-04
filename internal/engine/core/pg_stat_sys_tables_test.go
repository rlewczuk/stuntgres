package core_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func TestPGStatSysTablesCatalogIdentity(t *testing.T) {
	tableStatisticsCatalogIdentity(t, false)
}

func tableStatisticsCatalogIdentity(t *testing.T, user bool) {
	oid, rowOID, arrayOID, legacy := ids.OID(12156), sqltype.ID(12158), sqltype.ID(12157), ids.OID(12010)
	name := "pg_stat_sys_tables"
	if user {
		oid, rowOID, arrayOID, legacy = 12165, 12167, 12166, 12011
		name = "pg_stat_user_tables"
	}
	e := engine.New()
	read := e.CatalogRead()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
	view := read.MustRelation(a)
	if a.ObjectID != oid || view.RowType != rowOID || view.Namespace().ObjectID != 11 || pgcatalog.ClassKind(read, view) != "v" || !view.VirtualView || len(view.Columns) != 30 {
		t.Fatal("SYS inventory", view)
	}
	if _, ok := read.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: legacy}); ok {
		t.Fatal("obsolete SYS alias")
	}
	names := strings.Fields("relid schemaname relname seq_scan last_seq_scan seq_tup_read idx_scan last_idx_scan idx_tup_fetch n_tup_ins n_tup_upd n_tup_del n_tup_hot_upd n_tup_newpage_upd n_live_tup n_dead_tup n_mod_since_analyze n_ins_since_vacuum last_vacuum last_autovacuum last_analyze last_autoanalyze vacuum_count autovacuum_count analyze_count autoanalyze_count total_vacuum_time total_autovacuum_time total_analyze_time total_autoanalyze_time")
	for i, c := range view.Columns {
		kind, collation := sqltype.Int8, uint32(0)
		switch i {
		case 0:
			kind = sqltype.Oid
		case 1, 2:
			kind, collation = sqltype.Name, 950
		case 4, 7, 18, 19, 20, 21:
			kind = sqltype.Timestamptz
		case 26, 27, 28, 29:
			kind = sqltype.Float8
		}
		if c.Name != names[i] || c.Usage.Type.ID() != kind || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || !c.Nullable || !c.NullabilityKnown || c.OriginTable != uint32(oid) || c.OriginAttribute != int16(i+1) {
			t.Fatal("typed owned SYS column", i, c)
		}
	}
	row, ok := read.LookupType(rowOID)
	if !ok || row.AssociatedArray() != arrayOID {
		t.Fatal("SYS row/array", row)
	}
	array, ok := read.LookupType(arrayOID)
	if !ok || array.Element() != row {
		t.Fatal("SYS reciprocal array", array, row)
	}
	registered := 0
	for _, spec := range pgcatalog.Specs {
		if spec.Name == name {
			registered++
			if spec.OID != oid {
				t.Fatal("SYS source identity", spec)
			}
		}
	}
	if registered != 1 {
		t.Fatal("SYS source multiplicity", registered)
	}
	if producer, ok := pgcatalog.ProducerFor(a.ObjectID); !ok || producer == nil {
		t.Fatal("SYS identity has no executable producer")
	}
	if !reflect.DeepEqual(pgcatalog.KeyColumns(a.ObjectID), []int{0, 1, 2}) {
		t.Fatal("identity/schema/name selection")
	}
	for _, pretty := range []bool{false, true} {
		definition := pgcatalog.BuiltinViewDefinition(a.ObjectID, pretty, func(n pgcatalog.BuiltinViewName) string {
			if n.IdentifierOnly {
				return `"` + n.Name + `"`
			}
			if n.Class == ids.RelationClassID {
				return `"pg_catalog"."` + n.Name + `"`
			}
			return n.Name
		})
		for _, term := range []string{`FROM "pg_catalog"."pg_stat_all_tables"`, `"schemaname"`, `'^pg_toast'`} {
			if !strings.Contains(definition, term) {
				t.Fatal("dependency-aware SYS display", term, definition)
			}
		}
	}
}

type sysTableDemand struct {
	pgcatalog.Runtime
	tables, indexes map[ids.ObjectAddress]int
}

func (r *sysTableDemand) TableStatistics(a ids.ObjectAddress) pgcatalog.MaintenanceFact {
	r.tables[a]++
	return r.Runtime.TableStatistics(a)
}
func (r *sysTableDemand) IndexStatistics(a ids.ObjectAddress) pgcatalog.IndexStatisticsFact {
	r.indexes[a]++
	return r.Runtime.IndexStatistics(a)
}
func (r *sysTableDemand) clear() {
	r.tables = make(map[ids.ObjectAddress]int)
	r.indexes = make(map[ids.ObjectAddress]int)
}

func TestPGStatSysTablesSourceSelection(t *testing.T) {
	tableStatisticsSourceSelection(t, false)
}

func tableStatisticsSourceSelection(t *testing.T, user bool) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, s, "CREATE SCHEMA sst_selection", "CREATE TABLE sst_selection.sst_x(n integer)", "CREATE TABLE sst_selection.sst_plain(n integer)", "CREATE TABLE sst_outside(n integer)", "SET track_counts=off", "CREATE INDEX sst_xi ON sst_selection.sst_x(n)", "CREATE INDEX sst_oi ON sst_outside(n)", "SET track_counts=on")
	x := enginetest.RoleTableRef(t, e, "sst_selection.sst_x")
	plain := enginetest.RoleTableRef(t, e, "sst_selection.sst_plain")
	outside := enginetest.RoleTableRef(t, e, "sst_outside")
	xi := enginetest.RoleTableRef(t, e, "sst_selection.sst_xi")
	oi := enginetest.RoleTableRef(t, e, "sst_oi")
	var second ids.ObjectRef
	if user {
		statsFunctionExec(t, s, "SET track_counts=off", "CREATE INDEX sst_xj ON sst_selection.sst_x(n)", "SET track_counts=on")
		second = enginetest.RoleTableRef(t, e, "sst_selection.sst_xj")
	}
	configureStats(s, "none")
	enginetest.TransactionBegin(t, s)
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	// Isolate the fixture namespace without hiding genuine information_schema
	// heaps. Only test-local spelling changes; object/provider identities remain.
	state := catalog.CloneState(auth.Catalog.State())
	namespace, _ := auth.Catalog.Namespace(auth.Catalog.MustRelation(x.Address).Namespace())
	selectedNamespace := *namespace
	selectedNamespace.ObjectName = "pg_toast_selection"
	if user {
		selectedNamespace.ObjectName = "sut_selection"
	}
	selectedNamespace.ObjectRevision = namespace.Revision() + 1
	state.SetDescriptor(selectedNamespace.Address(), &selectedNamespace)
	read := e.Catalog.Read(state)
	auth.Catalog = read
	runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth, Settings: s.Settings})
	demand := &sysTableDemand{Runtime: runtime}
	demand.clear()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatSysTablesOID}
	if user {
		a.ObjectID = pgcatalog.StatUserTablesOID
	}
	in := pgcatalog.Input{Context: context.Background(), Catalog: read, Roles: auth.Roles, Owner: read.Owner(), Ref: e.Catalog.Domain.Ref(read.Epoch(), a), Source: read.MustRelation(a), Runtime: demand}
	produce, ok := pgcatalog.ProducerFor(a.ObjectID)
	if !ok {
		t.Fatal("missing SYS producer")
	}
	for _, column := range []int{0, 1, 2} {
		demand.clear()
		in.Keep = pgcatalog.Keep{Column: column, Test: func(v sqltype.Value) bool {
			if column == 0 {
				n, _ := v.OID()
				return ids.OID(n) == x.Address.ObjectID
			}
			name, _ := v.Text()
			if column == 1 {
				return name == selectedNamespace.ObjectName
			}
			return name == "sst_x"
		}}
		rows, err := produce(in)
		if err != nil || column != 1 && len(rows) != 1 || column == 1 && len(rows) < 2 {
			t.Fatal("selected SYS rows", column, rows, err)
		}
		if demand.tables[x.Address] != 1 || demand.indexes[xi.Address] != 1 || demand.tables[outside.Address] != 0 || demand.indexes[oi.Address] != 0 || demand.tables[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259}] != 0 || column != 1 && demand.tables[plain.Address] != 0 {
			t.Fatal("rejected table/index demanded before Keep", column, demand.tables, demand.indexes)
		}
		wantTables := map[ids.ObjectAddress]int{x.Address: 1}
		if column == 1 {
			wantTables[plain.Address] = 1
		}
		wantIndexes := map[ids.ObjectAddress]int{xi.Address: 1}
		if user {
			wantIndexes[second.Address] = 1
		}
		if !reflect.DeepEqual(demand.tables, wantTables) || !reflect.DeepEqual(demand.indexes, wantIndexes) {
			t.Fatal("exact selected runtime demands", column, demand.tables, demand.indexes)
		}
	}
	selectID := func(id ids.OID) pgcatalog.Keep {
		return pgcatalog.Keep{Column: 0, Test: func(v sqltype.Value) bool { n, _ := v.OID(); return ids.OID(n) == id }}
	}
	in.Keep = selectID(x.Address.ObjectID)
	held, err := produce(in)
	if err != nil || len(held) != 1 {
		t.Fatal(held, err)
	}
	for _, i := range []int{6, 8} {
		n, err := held[0][i].Integer()
		if err != nil || n != 0 {
			t.Fatal("untouched index zero", i, n, err)
		}
	}
	if !held[0][7].IsNull() {
		t.Fatal("untouched index timestamp")
	}
	retained := append([]sqltype.Value(nil), held[0]...)
	var writer statistics.Writer
	writer.Add(&e.Statistics, statistics.Key{Object: x}, statistics.Fact{SeqScans: 1, SeqTuples: 3, TableIndexFetches: 7})
	writer.IndexAccess(&e.Statistics, statistics.Key{Object: xi}, statistics.Policy{Counts: true}, 2, 4, 4)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	writer.TransactionStopped(at)
	writer.Flush(&e.Statistics, at, true)
	if user {
		writer.IndexAccess(&e.Statistics, statistics.Key{Object: second}, statistics.Policy{Counts: true}, 3, 5, 5)
		writer.TransactionStopped(at.Add(time.Second))
		writer.Flush(&e.Statistics, at.Add(time.Second), true)
	}
	e.Statistics.ReportAnalyze(statistics.Key{Object: x}, statistics.Policy{Counts: true}, at, 1500*time.Microsecond, 2, 1, true)
	fresh, err := produce(in)
	if err != nil || len(fresh) != 1 {
		t.Fatal(fresh, err)
	}
	for _, tc := range []struct {
		column int
		want   int64
	}{{3, 1}, {5, 3}, {6, 2}, {8, 11}, {12, 0}, {13, 0}, {24, 1}} {
		if user && tc.column == 6 {
			tc.want = 5
		}
		if user && tc.column == 8 {
			tc.want = 16
		}
		n, err := fresh[0][tc.column].Integer()
		if err != nil || n != tc.want {
			t.Fatal("shared provider projection", tc, n, err)
		}
	}
	elapsed, err := fresh[0][28].Float()
	if err != nil || elapsed != 1.5 {
		t.Fatal("fractional milliseconds", elapsed, err)
	}
	if user {
		last, err := fresh[0][7].TimestampTime()
		if err != nil || !last.Equal(at.Add(time.Second)) {
			t.Fatal("MAX across attached index stop times", last, err)
		}
	}
	if !reflect.DeepEqual(held[0], retained) {
		t.Fatal("retained SYS rows mutated")
	}
	in.Keep = selectID(plain.Address.ObjectID)
	rows, err := produce(in)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	for _, i := range []int{6, 7, 8} {
		if !rows[0][i].IsNull() || rows[0][i].Type() != in.Source.Columns[i].Usage.Type {
			t.Fatal("typed empty index set", i, rows)
		}
	}
	base := read.State()
	for _, tc := range []struct {
		name  string
		admit bool
	}{{"pg_catalog", true}, {"information_schema", true}, {"pg_toast", true}, {"pg_toast_extra", true}, {"pg_toast_temp_7", true}, {"pg_toasty", true}, {"public", false}, {"xpg_toast", false}, {"PG_TOAST", false}, {"pg_catalog_extra", false}, {"information_schema_extra", false}, {"pg_temp_7", false}} {
		if user {
			tc.admit = !tc.admit
		}
		state := catalog.CloneState(base)
		ns, _ := read.Namespace(read.MustRelation(x.Address).Namespace())
		copyNS := *ns
		copyNS.ObjectName = tc.name
		copyNS.ObjectRevision = ns.Revision() + 1
		state.SetDescriptor(copyNS.Address(), &copyNS)
		in.Catalog = e.Catalog.Read(state)
		in.Keep = selectID(x.Address.ObjectID)
		demand.clear()
		rows, err := produce(in)
		want := 0
		if tc.admit {
			want = 1
		}
		if err != nil || len(rows) != want || demand.tables[x.Address] != want || demand.indexes[xi.Address] != want {
			t.Fatal("case-sensitive subset before inquiry", tc, rows, demand.tables, demand.indexes, err)
		}
	}
	// ClassKind has no physical TOAST constructor; real TOAST is covered by the
	// public oracle when present. These are every represented ordinary kind.
	for _, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.CatalogRelation, catalog.MaterializedViewRelation, catalog.PartitionedRelation, catalog.ViewRelation, catalog.SequenceRelation, catalog.IndexRelation, catalog.ForeignRelation, catalog.CompositeRelation} {
		state := catalog.CloneState(base)
		relation := *read.MustRelation(x.Address)
		relation.RelationKind = kind
		relation.ObjectRevision = relation.Revision() + 1
		if kind == catalog.IndexRelation {
			relation.SQLIndex = &catalog.IndexDefinition{Parent: plain.Address}
		}
		state.SetDescriptor(relation.Address(), &relation)
		in.Catalog = e.Catalog.Read(state)
		demand.clear()
		rows, err := produce(in)
		want := 0
		if kind == catalog.TableRelation || kind == catalog.CatalogRelation || kind == catalog.MaterializedViewRelation || kind == catalog.PartitionedRelation {
			want = 1
		}
		if err != nil || len(rows) != want || demand.tables[x.Address] != want || demand.indexes[xi.Address] != want {
			t.Fatal("kind before inquiry", kind, rows, demand.tables, demand.indexes, err)
		}
	}
	state = catalog.CloneState(base)
	parent := *read.MustRelation(plain.Address)
	parent.RelationKind = catalog.PartitionedRelation
	parent.Partition = &catalog.PartitionDefinition{}
	parent.ObjectRevision = parent.Revision() + 1
	state.SetDescriptor(parent.Address(), &parent)
	partitionIndex := *read.MustRelation(x.Address)
	partitionIndex.RelationKind = catalog.IndexRelation
	partitionIndex.SQLIndex = &catalog.IndexDefinition{Parent: plain.Address}
	partitionIndex.ObjectRevision = partitionIndex.Revision() + 1
	state.SetDescriptor(partitionIndex.Address(), &partitionIndex)
	in.Catalog = e.Catalog.Read(state)
	demand.clear()
	if pgcatalog.ClassKind(in.Catalog, &partitionIndex) != "I" {
		t.Fatal("partitioned index fixture")
	}
	if rows, err := produce(in); err != nil || len(rows) != 0 || len(demand.tables) != 0 || len(demand.indexes) != 0 {
		t.Fatal("partitioned index admitted", rows, demand.tables, demand.indexes, err)
	}
	in.Catalog = read
	canceled, cancel := context.WithCancel(context.Background())
	in.Context = canceled
	in.Keep = pgcatalog.Keep{Column: 2, Test: func(sqltype.Value) bool { cancel(); return true }}
	if partial, err := produce(in); err != context.Canceled || partial != nil {
		t.Fatal("canceled capture leaked partial result", partial, err)
	}
	in.Context = context.Background()
	in.Owner = nil
	if partial, err := produce(in); err == nil || partial != nil {
		t.Fatal("invalid authority accepted", partial, err)
	}
}
