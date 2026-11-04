package core_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func TestPGStatSysIndexesCatalog(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	if got := indexStatsRows(t, s, "SELECT * FROM pg_stat_sys_indexes"); len(got) != 0 {
		t.Fatal("fabricated bootstrap SYS inventory", got)
	}
	read := e.CatalogRead()
	view := read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatSysIndexesOID})
	row := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12194})
	array := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12193})
	if view.Address().ObjectID != 12192 || view.Namespace().ObjectID != 11 || view.RowType != 12194 || !view.VirtualView || pgcatalog.ClassKind(read, view) != "v" || row.Array != 12193 || row.OwningRelation != view.Address() || array.OwningRelation != view.Address() || array.SQLType.Element() != row.SQLType || pgcatalog.BuiltinViewEvents(pgcatalog.StatSysIndexesOID, nil) != 0 {
		t.Fatal("SYS relation/row/array/events", view, row, array)
	}
	names := []string{"relid", "indexrelid", "schemaname", "relname", "indexrelname", "idx_scan", "last_idx_scan", "idx_tup_read", "idx_tup_fetch"}
	types := []sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Name, sqltype.Name, sqltype.Name, sqltype.Int8, sqltype.Timestamptz, sqltype.Int8, sqltype.Int8}
	if len(view.Columns) != len(names) || !reflect.DeepEqual(pgcatalog.KeyColumns(pgcatalog.StatSysIndexesOID), []int{0, 1, 2, 3, 4}) {
		t.Fatal("SYS shape/keys", view.Columns)
	}
	for i, c := range view.Columns {
		collation := uint32(0)
		if i >= 2 && i <= 4 {
			collation = 950
		}
		if c.Name != names[i] || c.Usage.Type.ID() != types[i] || !c.Nullable || !c.NullabilityKnown || c.OriginTable != 12192 || c.OriginAttribute != int16(i+1) || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation {
			t.Fatal("SYS typed nullable origin", i, c)
		}
	}
	registered := false
	for _, spec := range pgcatalog.Specs {
		if spec.Name == "pg_stat_sys_indexes" {
			registered = true
		}
	}
	if !registered {
		t.Fatal("SYS not registered")
	}
}

func TestPGStatSysIndexesSourceSelection(t *testing.T) {
	for _, mode := range []string{"cache", "snapshot", "none"} {
		for column, name := range []string{"relid", "indexrelid", "schemaname", "relname", "indexrelname", "namespace_only"} {
			t.Run(mode+"/"+name, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				statsFunctionExec(t, s, "CREATE TABLE information_schema.six_select_x(n integer)", "CREATE TABLE information_schema.six_select_y(n integer)", "CREATE TABLE public.six_select_public(n integer)", "CREATE INDEX six_select_a ON information_schema.six_select_x(n)", "CREATE INDEX six_select_b ON information_schema.six_select_y(n)", "CREATE INDEX six_select_public_idx ON public.six_select_public(n)")
				x, y := enginetest.RoleTableRef(t, e, "information_schema.six_select_a"), enginetest.RoleTableRef(t, e, "information_schema.six_select_b")
				public := enginetest.RoleTableRef(t, e, "six_select_public_idx")
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
				address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatSysIndexesOID}
				runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth, Settings: s.Settings})
				produce, _ := pgcatalog.ProducerFor(pgcatalog.StatSysIndexesOID)
				in := pgcatalog.Input{Context: context.Background(), Catalog: read, Roles: auth.Roles, Owner: &e.Catalog.Owner, Ref: e.Catalog.Domain.Ref(read.Epoch(), address), Source: read.MustRelation(address), Runtime: runtime}
				if column < 5 {
					in.Keep = pgcatalog.Keep{Column: column, Test: func(v sqltype.Value) bool {
						switch column {
						case 0:
							n, _ := v.OID()
							return ids.OID(n) == read.MustRelation(x.Address).SQLIndex.Parent.ObjectID
						case 1:
							n, _ := v.OID()
							return ids.OID(n) == x.Address.ObjectID
						case 2:
							return false // all rejected must not establish a snapshot
						case 3:
							n, _ := v.Text()
							return n == "six_select_x"
						default:
							n, _ := v.Text()
							return n == "six_select_a"
						}
					}}
				}
				rows, err := produce(in)
				wantRows := 1
				if column == 2 {
					wantRows = 0
				} else if column == 5 {
					wantRows = 2
				}
				if err != nil || len(rows) != wantRows {
					t.Fatal("filtered SYS source", rows, err)
				}
				for _, key := range []ids.ObjectRef{x, y, public} {
					w.IndexAccess(&e.Statistics, statistics.Key{Object: key}, statistics.Policy{Counts: true}, 2, 0, 0)
				}
				w.Flush(&e.Statistics, time.Now(), true)
				wantPublic := int64(2)
				if mode == "snapshot" && column != 2 {
					wantPublic = 0
				}
				if got := runtime.IndexStatistics(public.Address).Scans; got != wantPublic {
					t.Fatal("namespace exclusion demanded provider", got, wantPublic)
				}
				wantY := int64(2)
				if (mode == "snapshot" && column != 2) || (mode == "cache" && column == 5) {
					wantY = 0
				}
				if got := runtime.IndexStatistics(y.Address).Scans; got != wantY {
					t.Fatal("Keep demanded rejected identity", got, wantY)
				}
				wantX := int64(1)
				if mode == "none" || column == 2 {
					wantX = 3
				}
				// Demand through ALL after SYS: the same Reader object cache applies.
				allAddress := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatAllIndexesOID}
				all := in
				all.Source, all.Ref = read.MustRelation(allAddress), e.Catalog.Domain.Ref(read.Epoch(), allAddress)
				all.Keep = pgcatalog.Keep{Column: 1, Test: func(v sqltype.Value) bool { n, _ := v.OID(); return ids.OID(n) == x.Address.ObjectID }}
				allProduce, _ := pgcatalog.ProducerFor(pgcatalog.StatAllIndexesOID)
				allRows, err := allProduce(all)
				if err != nil || len(allRows) != 1 {
					t.Fatal("ALL shared demand", allRows, err)
				}
				if got, _ := allRows[0][5].Integer(); got != wantX {
					t.Fatal("per-view rather than shared Reader", got, wantX)
				}
				w.IndexAccess(&e.Statistics, statistics.Key{Object: x}, statistics.Policy{Counts: true}, 1, 0, 0)
				ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
				if _, err := ctx.StatsSnapshotValue(true, enginetest.Usage(e, sqltype.Void).Type); err != nil {
					t.Fatal(err)
				}
				in.Keep = all.Keep
				fresh, err := produce(in)
				if err != nil || len(fresh) != 1 {
					t.Fatal("refreshed SYS", fresh, err)
				}
				if got, _ := fresh[0][5].Integer(); got != 3 || runtime.IndexStatistics(x.Address).Scans != 3 || e.Statistics.Read(statistics.Key{Object: x}).IndexScans != 3 {
					t.Fatal("clear must refresh both without publishing pending work", fresh)
				}
				for _, row := range rows {
					n, _ := row[1].OID()
					if ids.OID(n) == x.Address.ObjectID {
						if got, _ := row[5].Integer(); got != 1 {
							t.Fatal("retained row mutated", row)
						}
					}
				}
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
