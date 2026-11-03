package core_test

import (
	"context"
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

// Observe demand without substituting facts or snapshot authority.
type statioObservedRuntime struct {
	pgcatalog.Runtime
	fetched map[ids.ObjectAddress]int
}

func (r *statioObservedRuntime) IndexBlockStatistics(address ids.ObjectAddress) pgcatalog.IndexBlockFact {
	r.fetched[address]++
	return r.Runtime.IndexBlockStatistics(address)
}

func TestStatioAllIndexesReaderConsistency(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, column := range []int{0, 1, 3, 4} {
			t.Run(mode+"/"+[]string{"parent", "index", "", "table_name", "index_name"}[column], func(t *testing.T) {
				e := engine.New()
				for _, q := range []string{"CREATE TABLE statio_x(k integer)", "CREATE TABLE statio_y(k integer)", "CREATE INDEX statio_x_i ON statio_x(k)", "CREATE INDEX statio_y_i ON statio_y(k)"} {
					enginetest.Run(t, e, q)
				}
				x, y := statioKey(t, e, "statio_x_i"), statioKey(t, e, "statio_y_i")
				var w statistics.Writer
				w.IndexBlock(&e.Statistics, x, statistics.Policy{Counts: true}, false)
				w.Flush(&e.Statistics, time.Now(), true)
				s := enginetest.StartSession(t, e, nil)
				configureStats(s, mode)
				enginetest.TransactionBegin(t, s)
				op := enginetest.TransactionOperation(t, s)
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				e.Mu.Lock()
				auth, err := e.AuthorizationLocked(op)
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth, Settings: s.Settings})
				address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatioAllIndexesOID}
				read := auth.Catalog
				source := read.MustRelation(address)
				ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
				stamp := func() sqltype.Value {
					v, err := ctx.StatsSnapshotValue(false, enginetest.Usage(e, sqltype.Timestamptz).Type)
					if err != nil {
						t.Fatal(err)
					}
					return v
				}
				if !stamp().IsNull() {
					t.Fatal("eager snapshot timestamp")
				}
				parent := enginetest.RoleTableRef(t, e, "statio_x")
				keep := pgcatalog.Keep{Column: column, Test: func(v sqltype.Value) bool {
					if column == 0 || column == 1 {
						n, err := v.OID()
						want := uint32(x.Object.Address.ObjectID)
						if column == 0 {
							want = uint32(parent.Address.ObjectID)
						}
						return err == nil && n == want
					}
					name, err := v.Text()
					want := "statio_x_i"
					if column == 3 {
						want = "statio_x"
					}
					return err == nil && name == want
				}}
				observed := &statioObservedRuntime{Runtime: runtime, fetched: make(map[ids.ObjectAddress]int)}
				input := pgcatalog.Input{Context: context.Background(), Catalog: read, Roles: auth.Roles, Owner: &e.Catalog.Owner, Ref: e.Catalog.Domain.Ref(read.Epoch(), address), Source: source, Runtime: observed, Keep: keep}
				produce, ok := pgcatalog.ProducerFor(pgcatalog.StatioAllIndexesOID)
				if !ok {
					t.Fatal("producer missing")
				}
				rows, err := produce(input)
				if err != nil || len(rows) != 1 {
					t.Fatal("selected X", rows, err)
				}
				if len(observed.fetched) != 1 || observed.fetched[x.Object.Address] != 1 {
					t.Fatal("Keep fetched rejected index before filtering", observed.fetched)
				}
				retained := rows[0][5:7]
				if stamp().IsNull() != (mode != "snapshot") {
					t.Fatal("lazy shared timestamp")
				}
				for range 2 {
					w.IndexBlock(&e.Statistics, x, statistics.Policy{Counts: true}, true)
				}
				w.IndexBlock(&e.Statistics, y, statistics.Policy{Counts: true}, false)
				w.Flush(&e.Statistics, time.Now(), true)
				wantX := pgcatalog.IndexBlockFact{Read: 1, Hit: 2}
				if mode != "none" {
					wantX.Hit = 0
				}
				wantY := pgcatalog.IndexBlockFact{Read: 1}
				if mode == "snapshot" {
					wantY.Read = 0
				}
				if got := runtime.IndexBlockStatistics(x.Object.Address); got != wantX {
					t.Fatal("X mode", got, wantX)
				}
				if got := runtime.IndexBlockStatistics(y.Object.Address); got != wantY {
					t.Fatal("Keep fetched rejected Y", got, wantY)
				}
				if n, _ := retained[0].Integer(); n != 1 {
					t.Fatal("held read mutated")
				}
				if n, _ := retained[1].Integer(); n != 0 {
					t.Fatal("held hit mutated")
				}
				if _, err := ctx.StatsSnapshotValue(true, enginetest.Usage(e, sqltype.Void).Type); err != nil {
					t.Fatal(err)
				}
				if !stamp().IsNull() {
					t.Fatal("clear retained timestamp")
				}
				if got := runtime.IndexBlockStatistics(x.Object.Address); got != (pgcatalog.IndexBlockFact{Read: 1, Hit: 2}) {
					t.Fatal("clear failed", got)
				}
				if got := runtime.IndexBlockStatistics(y.Object.Address); got != (pgcatalog.IndexBlockFact{Read: 1}) {
					t.Fatal("clear failed", got)
				}
				configureStats(s, "none")
				configureStats(s, "snapshot")
				if !stamp().IsNull() {
					t.Fatal("settings roundtrip retained snapshot")
				}
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
				op.Done()
				enginetest.SessionSQL(t, s, "ROLLBACK")
				enginetest.SessionSQL(t, s, "BEGIN")
				result := enginetest.SessionSQL(t, s, "SELECT pg_stat_get_snapshot_timestamp()")
				cell := make([]sqltype.Value, 1)
				if present, err := result.Rows.Next(context.Background(), cell); err != nil || !present || !cell[0].IsNull() {
					t.Fatal("transaction terminal retained snapshot", cell, err)
				}
				enginetest.SessionSQL(t, s, "ROLLBACK")
			})
		}
	}
	for _, first := range []string{"table", "index"} {
		t.Run("cross_consumer/"+first, func(t *testing.T) {
			e := engine.New()
			statioSeed(t, e)
			s := enginetest.StartSession(t, e, nil)
			configureStats(s, "snapshot")
			enginetest.TransactionBegin(t, s)
			op := enginetest.TransactionOperation(t, s)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth, Settings: s.Settings})
			parent := enginetest.RoleTableRef(t, e, "statio_events")
			key := statioKey(t, e, "statio_events_i")
			oldTable := runtime.TableStatistics // Both consumers use one transaction Reader.
			if first == "table" {
				oldTable(parent.Address)
			} else {
				runtime.IndexBlockStatistics(key.Object.Address)
			}
			var w statistics.Writer
			w.Add(&e.Statistics, statistics.Key{Object: parent}, statistics.Fact{Inserts: 7})
			w.IndexBlock(&e.Statistics, key, statistics.Policy{Counts: true}, false)
			w.Flush(&e.Statistics, time.Now(), true)
			if runtime.TableStatistics(parent.Address).Inserts != 2 {
				t.Fatal("index-first snapshot did not capture table facts")
			}
			if got := runtime.IndexBlockStatistics(key.Object.Address); got != (pgcatalog.IndexBlockFact{}) {
				t.Fatal("table-first snapshot did not capture absent index", got)
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			op.Done()
			enginetest.SessionSQL(t, s, "ROLLBACK")
		})
	}
}
