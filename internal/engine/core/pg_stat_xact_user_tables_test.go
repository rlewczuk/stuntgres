package core_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func TestPGStatXactUserTablesCatalogIdentity(t *testing.T) {
	e := engine.New()
	read := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatXactUserTablesOID}
	view, exists := read.Relation(address)
	if !exists {
		t.Fatal("missing USER relation 12170")
	}
	if pgcatalog.StatXactUserTablesOID != 12170 || view.RowType != 12172 || view.Namespace().ObjectID != 11 || pgcatalog.ClassKind(read, view) != "v" || !view.VirtualView || len(view.Columns) != 12 {
		t.Fatal("fixed USER relation identity", view)
	}
	names := strings.Fields("relid schemaname relname seq_scan seq_tup_read idx_scan idx_tup_fetch n_tup_ins n_tup_upd n_tup_del n_tup_hot_upd n_tup_newpage_upd")
	for i, c := range view.Columns {
		kind, collation := sqltype.Int8, uint32(0)
		if i == 0 {
			kind = sqltype.Oid
		} else if i < 3 {
			kind = sqltype.Name
			collation = 950
		}
		if c.Name != names[i] || c.Usage.Type.ID() != kind || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || !c.Nullable || !c.NullabilityKnown || c.OriginTable != 12170 || c.OriginAttribute != int16(i+1) {
			t.Fatal("USER typed origin", i, c)
		}
	}
	row, ok := read.LookupType(sqltype.ID(12172))
	if !ok || row.AssociatedArray() != sqltype.ID(12171) {
		t.Fatal("row array", row)
	}
	array, ok := read.LookupType(sqltype.ID(12171))
	if !ok || array.Element() != row {
		t.Fatal("reciprocal array element", array, row)
	}
	registered := 0
	for _, spec := range pgcatalog.Specs {
		if spec.Name == "pg_stat_xact_user_tables" {
			registered++
			if spec.OID != 12170 {
				t.Fatal("source OID", spec)
			}
		}
	}
	if registered != 1 {
		t.Fatal("source multiplicity", registered)
	}
	if !reflect.DeepEqual(pgcatalog.KeyColumns(pgcatalog.StatXactUserTablesOID), []int{0, 1, 2}) {
		t.Fatal("USER identity selection")
	}
	definition := pgcatalog.SummaryViewDefinition(pgcatalog.StatXactUserTablesOID)
	for _, term := range []string{"FROM pg_stat_xact_all_tables", "schemaname <> ALL (ARRAY['pg_catalog'::name, 'information_schema'::name])", "schemaname !~ '^pg_toast'::text"} {
		if !strings.Contains(definition, term) {
			t.Fatal("released USER display", term, definition)
		}
	}
	for _, pretty := range []bool{false, true} {
		rendered := pgcatalog.BuiltinViewDefinition(pgcatalog.StatXactUserTablesOID, pretty, nil, func(n pgcatalog.BuiltinViewName) string {
			if n.IdentifierOnly {
				return `"` + n.Name + `"`
			}
			if n.Class == ids.RelationClassID {
				return `"pg_catalog"."` + n.Name + `"`
			}
			return n.Name
		})
		if !strings.Contains(rendered, `FROM "pg_catalog"."pg_stat_xact_all_tables"`) || !strings.Contains(rendered, `"schemaname"`) || !strings.Contains(rendered, "'^pg_toast'") {
			t.Fatal("dependency-aware quoted/search-path display", rendered)
		}
	}
}

func TestPGStatXactUserTablesSourceSelection(t *testing.T) {
	e := engine.New()
	sess := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, sess, "CREATE TABLE public.xut_unit(n integer)", "CREATE TABLE public.xut_plain(n integer)", "CREATE INDEX xut_unit_index ON public.xut_unit(n)", "CREATE TABLE information_schema.xut_outside(n integer)", "CREATE INDEX xut_outside_index ON information_schema.xut_outside(n)")
	x := enginetest.RoleTableRef(t, e, "public.xut_unit")
	index := enginetest.RoleTableRef(t, e, "public.xut_unit_index")
	plain := enginetest.RoleTableRef(t, e, "public.xut_plain")
	outside := enginetest.RoleTableRef(t, e, "information_schema.xut_outside")
	outsideIndex := enginetest.RoleTableRef(t, e, "information_schema.xut_outside_index")
	configureStats(sess, "snapshot")
	enginetest.TransactionBegin(t, sess)
	op := enginetest.TransactionOperation(t, sess)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	timestampType := enginetest.Usage(e, sqltype.Timestamptz).Type
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	read := auth.Catalog
	runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth, Settings: sess.Settings})
	wrapped := &xactSelectionRuntime{Runtime: runtime, seen: make(map[ids.ObjectAddress]int)}
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatXactUserTablesOID}
	if _, exists := read.Relation(a); !exists {
		t.Fatal("missing USER relation 12170")
	}
	in := pgcatalog.Input{Context: context.Background(), Catalog: read, Roles: auth.Roles, Owner: read.Owner(), Ref: e.Catalog.Domain.Ref(read.Epoch(), a), Source: read.MustRelation(a), Runtime: wrapped}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.StatXactUserTablesOID)
	if !ok {
		t.Fatal("missing USER producer")
	}
	for _, column := range []int{0, 1, 2} {
		wrapped.seen = make(map[ids.ObjectAddress]int)
		in.Keep = pgcatalog.Keep{Column: column, Test: func(v sqltype.Value) bool {
			if column == 0 {
				n, _ := v.OID()
				return ids.OID(n) == x.Address.ObjectID
			}
			text, _ := v.Text()
			if column == 1 {
				return text == "public"
			}
			return text == "xut_unit"
		}}
		rows, err := produce(in)
		if err != nil || column != 1 && len(rows) != 1 || column == 1 && (len(rows) < 2 || wrapped.seen[plain.Address] != 1) {
			t.Fatal("selected USER source", column, len(rows), err)
		}
		if wrapped.seen[x.Address] != 1 || wrapped.seen[index.Address] != 1 || wrapped.seen[outside.Address] != 0 || wrapped.seen[outsideIndex.Address] != 0 {
			t.Fatal("Keep/namespace must precede runtime inquiry", column, wrapped.seen)
		}
		if wrapped.seen[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ClassOID}] != 0 || column != 1 && wrapped.seen[plain.Address] != 0 {
			t.Fatal("identity/schema/name Keep inquired rejected system relation", column, wrapped.seen)
		}
		for address, relation := range read.Relations() {
			ns, ok := read.Namespace(relation.Namespace())
			if !ok {
				continue
			}
			system := ns.Name() == "pg_catalog" || ns.Name() == "information_schema" || strings.HasPrefix(ns.Name(), "pg_toast")
			if system && wrapped.seen[address] != 0 {
				t.Fatal("USER inquired excluded system member/index", column, address, wrapped.seen[address])
			}
		}
	}
	in.Keep = pgcatalog.Keep{Column: 2, Test: func(v sqltype.Value) bool { s, _ := v.Text(); return s == "xut_unit" }}
	held, err := produce(in)
	if err != nil || len(held) != 1 {
		t.Fatal("USER baseline", held, err)
	}
	if held[0][5].IsNull() || held[0][6].IsNull() {
		t.Fatal("attached untouched index must be zero", held)
	}
	for _, i := range []int{5, 6} {
		n, err := held[0][i].Integer()
		if err != nil || n != 0 {
			t.Fatal("attached zero", i, n, err)
		}
	}
	retained := append([]sqltype.Value(nil), held[0]...)
	scope.Stage.Stats.Record(statistics.Key{Object: x}, statistics.Fact{Inserts: 2}, 0)
	fresh, err := produce(in)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := fresh[0][7].Integer()
	if n != 2 || !reflect.DeepEqual(held[0], retained) {
		t.Fatal("live counts/retained immutable rows", n, held, retained)
	}
	in.Keep = pgcatalog.Keep{Column: 0, Test: func(v sqltype.Value) bool { id, _ := v.OID(); return ids.OID(id) == plain.Address.ObjectID }}
	rows, err := produce(in)
	if err != nil || len(rows) != 1 || !rows[0][5].IsNull() || !rows[0][6].IsNull() {
		t.Fatal("no-index typed NULL", rows, err)
	}
	for _, column := range []int{5, 6} {
		if rows[0][column].Type() != in.Source.Columns[column].Usage.Type || rows[0][column].Type().ID() != sqltype.Int8 {
			t.Fatal("NULL lost INT8 carrier", column)
		}
	}
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
	stamp, err := ctx.StatsSnapshotValue(false, timestampType)
	if err != nil || !stamp.IsNull() {
		t.Fatal("USER initialized cumulative Reader", stamp, err)
	}
	// Standalone cumulative runtimes acquire the initial shared-capture fence.
	e.Mu.Unlock()
	runtime.TableStatistics(x.Address)
	e.Mu.Lock()
	cut, err := ctx.StatsSnapshotValue(false, timestampType)
	if err != nil || cut.IsNull() {
		t.Fatal("missing established cumulative cut", cut, err)
	}
	if _, err := produce(in); err != nil {
		t.Fatal(err)
	}
	still, err := ctx.StatsSnapshotValue(false, timestampType)
	if err != nil || !reflect.DeepEqual(cut, still) {
		t.Fatal("USER changed established cumulative cut", cut, still, err)
	}
	// Clone only test-local descriptors: this proves spelling boundaries without
	// trying to CREATE reserved schemas or altering the real provider authority.
	base := e.Catalog.State
	for _, tc := range []struct {
		name     string
		admitted bool
	}{{"pg_catalog", false}, {"information_schema", false}, {"pg_toast", false}, {"pg_toast_extra", false}, {"pg_toast_temp_7", false}, {"pg_toasty", false}, {"xpg_toast", true}, {"PG_TOAST", true}, {"pg_catalog_extra", true}, {"information_schema_extra", true}, {"public", true}, {"pg_temp_7", true}} {
		state := catalog.CloneState(base)
		namespace, _ := read.Namespace(read.MustRelation(x.Address).Namespace())
		copyNamespace := *namespace
		copyNamespace.ObjectName = tc.name
		copyNamespace.ObjectRevision = namespace.Revision() + 1
		state.SetDescriptor(copyNamespace.Address(), &copyNamespace)
		in.Catalog = e.Catalog.Read(state)
		in.Keep = pgcatalog.Keep{Column: 0, Test: func(v sqltype.Value) bool { id, _ := v.OID(); return ids.OID(id) == x.Address.ObjectID }}
		wrapped.seen = make(map[ids.ObjectAddress]int)
		rows, err := produce(in)
		want := 0
		if tc.admitted {
			want = 1
		}
		if err != nil || len(rows) != want || wrapped.seen[x.Address] != want {
			t.Fatal("case-sensitive namespace before inquiry", tc, len(rows), wrapped.seen, err)
		}
	}
	in.Catalog = read
	for _, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.MaterializedViewRelation, catalog.PartitionedRelation, catalog.ViewRelation, catalog.SequenceRelation, catalog.ForeignRelation, catalog.CompositeRelation} {
		state := catalog.CloneState(base)
		relation := *read.MustRelation(x.Address)
		relation.RelationKind = kind
		relation.ObjectRevision++
		state.SetDescriptor(relation.Address(), &relation)
		in.Catalog = e.Catalog.Read(state)
		rows, err := produce(in)
		want := 0
		if kind == catalog.TableRelation || kind == catalog.MaterializedViewRelation || kind == catalog.PartitionedRelation {
			want = 1
		}
		if err != nil || len(rows) != want {
			t.Fatal("relation kind boundary", kind, len(rows), err)
		}
	}
	in.Catalog = read
	preCanceled, stop := context.WithCancel(context.Background())
	stop()
	in.Context = preCanceled
	in.Keep = pgcatalog.Keep{}
	wrapped.seen = make(map[ids.ObjectAddress]int)
	if rows, err := produce(in); err != context.Canceled || rows != nil || len(wrapped.seen) != 0 {
		t.Fatal("pre-canceled USER capture made inquiries", rows, wrapped.seen, err)
	}
	unchangedCut, err := ctx.StatsSnapshotValue(false, timestampType)
	if err != nil || !reflect.DeepEqual(cut, unchangedCut) {
		t.Fatal("canceled USER capture changed cumulative cut", cut, unchangedCut, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	in.Context = canceled
	in.Keep = pgcatalog.Keep{Column: 2, Test: func(sqltype.Value) bool { cancel(); return true }}
	partial, err := produce(in)
	if err != context.Canceled || partial != nil {
		t.Fatal("enumeration cancellation returned partial USER", partial, err)
	}
	in.Context = context.Background()
	in.Owner = nil
	if partial, err := produce(in); err == nil || partial != nil {
		t.Fatal("invalid USER capture authority accepted")
	}
}
