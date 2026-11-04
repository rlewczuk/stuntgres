package core_test

import (
	"context"
	"reflect"
	"testing"
	"time"

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

func TestPGStatSysTablesSchemaReader(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, s, "CREATE SCHEMA sst_reader_selected", "CREATE SCHEMA sst_reader_other", "CREATE TABLE sst_reader_selected.sst_reader_a(n integer)", "CREATE TABLE sst_reader_other.sst_reader_b(n integer)", "SET track_counts=off", "CREATE INDEX sst_reader_ai ON sst_reader_selected.sst_reader_a(n)", "CREATE INDEX sst_reader_bi ON sst_reader_other.sst_reader_b(n)", "SET track_counts=on")
			a, b := enginetest.RoleTableRef(t, e, "sst_reader_selected.sst_reader_a"), enginetest.RoleTableRef(t, e, "sst_reader_other.sst_reader_b")
			ai, bi := enginetest.RoleTableRef(t, e, "sst_reader_selected.sst_reader_ai"), enginetest.RoleTableRef(t, e, "sst_reader_other.sst_reader_bi")
			at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			var writer statistics.Writer
			publish := func(scans int64, stop time.Time) {
				for _, ref := range []ids.ObjectRef{a, b} {
					writer.Add(&e.Statistics, statistics.Key{Object: ref}, statistics.Fact{SeqScans: scans, SeqTuples: scans * 3, TableIndexFetches: scans * 4})
				}
				for _, ref := range []ids.ObjectRef{ai, bi} {
					writer.IndexAccess(&e.Statistics, statistics.Key{Object: ref}, statistics.Policy{Counts: true}, scans, scans*2, scans*2)
				}
				writer.TransactionStopped(stop)
				writer.Flush(&e.Statistics, stop, true)
			}
			publish(1, at)
			configureStats(s, mode)
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
			// Only the test-local descriptor spelling changes. Both heaps/indexes
			// retain their actual engine/domain/epoch identity and real provider facts.
			state := catalog.CloneState(e.Catalog.State)
			for _, fixture := range []struct {
				ref  ids.ObjectRef
				name string
			}{{a, "pg_toast_reader_a"}, {b, "pg_toast_reader_b"}} {
				ns, _ := auth.Catalog.Namespace(auth.Catalog.MustRelation(fixture.ref.Address).Namespace())
				copyNS := *ns
				copyNS.ObjectName = fixture.name
				copyNS.ObjectRevision = ns.Revision() + 1
				state.SetDescriptor(copyNS.Address(), &copyNS)
			}
			read := e.Catalog.Read(state)
			auth.Catalog = read
			runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth, Settings: s.Settings})
			address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatSysTablesOID}
			in := pgcatalog.Input{Context: context.Background(), Catalog: read, Roles: auth.Roles, Owner: read.Owner(), Ref: e.Catalog.Domain.Ref(read.Epoch(), address), Source: read.MustRelation(address), Runtime: runtime, Keep: pgcatalog.Keep{Column: 1, Test: func(v sqltype.Value) bool { name, _ := v.Text(); return name == "pg_toast_reader_a" }}}
			produce, ok := pgcatalog.ProducerFor(address.ObjectID)
			if !ok {
				t.Fatal("missing executable SYS source")
			}
			held, err := produce(in)
			if err != nil || len(held) != 1 {
				t.Fatal("pure schema capture", held, err)
			}
			wantRow := func(rows [][]sqltype.Value, id ids.OID, scans int64) {
				t.Helper()
				if len(rows) != 1 {
					t.Fatal("source cardinality", len(rows))
				}
				gotID, _ := rows[0][0].OID()
				if ids.OID(gotID) != id {
					t.Fatal("wrong table identity", gotID, id)
				}
				for _, tc := range []struct {
					column int
					want   int64
				}{{3, scans}, {5, scans * 3}, {6, scans}, {8, scans * 6}} {
					got, err := rows[0][tc.column].Integer()
					if err != nil || got != tc.want {
						t.Fatal("shared table/index Reader cut", mode, tc, got, err)
					}
				}
			}
			wantRow(held, a.Address.ObjectID, 1)
			retained := append([]sqltype.Value(nil), held[0]...)
			publish(2, at.Add(time.Second))
			capture := func(ref ids.ObjectRef) [][]sqltype.Value {
				t.Helper()
				in.Keep = pgcatalog.Keep{Column: 0, Test: func(v sqltype.Value) bool { id, _ := v.OID(); return ids.OID(id) == ref.Address.ObjectID }}
				rows, err := produce(in)
				if err != nil {
					t.Fatal(err)
				}
				return rows
			}
			wantA, wantB := int64(1), int64(3)
			if mode == "none" {
				wantA = 3
			}
			if mode == "snapshot" {
				wantB = 1
			}
			wantRow(capture(a), a.Address.ObjectID, wantA)
			wantRow(capture(b), b.Address.ObjectID, wantB)
			// Clearing the actual transaction Reader must refresh both categories
			// without publishing the writer's additional unflushed work.
			writer.Add(&e.Statistics, statistics.Key{Object: a}, statistics.Fact{SeqScans: 5})
			writer.IndexAccess(&e.Statistics, statistics.Key{Object: ai}, statistics.Policy{Counts: true}, 5, 0, 0)
			ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
			if _, err := ctx.StatsSnapshotValue(true, enginetest.Usage(e, sqltype.Void).Type); err != nil {
				t.Fatal(err)
			}
			wantRow(capture(a), a.Address.ObjectID, 3)
			wantRow(capture(b), b.Address.ObjectID, 3)
			if e.Statistics.Read(statistics.Key{Object: a}).SeqScans != 3 || e.Statistics.Read(statistics.Key{Object: ai}).IndexScans != 3 {
				t.Fatal("clear published pending work")
			}
			if !reflect.DeepEqual(held[0], retained) {
				t.Fatal("retained schema result mutated")
			}
		})
	}
}
