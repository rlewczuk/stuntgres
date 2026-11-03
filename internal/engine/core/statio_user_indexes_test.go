package core_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

// This fixture retains the actual transaction-owned Reader; the wrapper only
// counts demands, and never supplies or caches an alternate statistics value.
func statioUserReader(t *testing.T, mode string, system bool) (*core.Engine, *statioObservedRuntime, pgcatalog.Input, func(bool) sqltype.Value) {
	t.Helper()
	e := engine.New()
	prefix := ""
	if system {
		prefix = "information_schema."
	}
	for _, q := range []string{"CREATE TABLE " + prefix + "user_reader_x(k integer)", "CREATE INDEX user_reader_x_i ON " + prefix + "user_reader_x(k)", "CREATE TABLE " + prefix + "user_reader_y(k integer)", "CREATE INDEX user_reader_y_i ON " + prefix + "user_reader_y(k)"} {
		enginetest.Run(t, e, q)
	}
	s := enginetest.StartSession(t, e, nil)
	configureStats(s, mode)
	enginetest.TransactionBegin(t, s)
	op := enginetest.TransactionOperation(t, s)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
		op.Done()
		enginetest.SessionSQL(t, s, "ROLLBACK")
	})
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth, Settings: s.Settings})
	observed := &statioObservedRuntime{Runtime: runtime, fetched: make(map[ids.ObjectAddress]int)}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatioUserIndexesOID}
	read := auth.Catalog
	input := pgcatalog.Input{Context: t.Context(), Catalog: read, Roles: auth.Roles, Owner: &e.Catalog.Owner, Ref: e.Catalog.Domain.Ref(read.Epoch(), address), Source: read.MustRelation(address), Runtime: observed}
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context(), Host: core.HostOf(scope)}}
	stamp := func(clear bool) sqltype.Value {
		typeID := sqltype.Timestamptz
		if clear {
			typeID = sqltype.Void
		}
		v, err := ctx.StatsSnapshotValue(clear, enginetest.Usage(e, typeID).Type)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	return e, observed, input, stamp
}

func statioUserProduce(t *testing.T, in pgcatalog.Input, oid ids.OID) [][]sqltype.Value {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
	in.Source = in.Catalog.MustRelation(address)
	in.Ref = in.Owner.Domain.Ref(in.Catalog.Epoch(), address)
	produce, ok := pgcatalog.ProducerFor(oid)
	if !ok {
		t.Fatal("missing named source producer", oid)
	}
	rows, err := produce(in)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func statioUserWantPair(t *testing.T, rows [][]sqltype.Value, name string, read, hit int64) {
	t.Helper()
	found := 0
	for _, row := range rows {
		n, _ := row[4].Text()
		if n != name {
			continue
		}
		found++
		r, re := row[5].Integer()
		h, he := row[6].Integer()
		if re != nil || he != nil || row[5].IsNull() || row[6].IsNull() || r != read || h != hit {
			t.Fatal("independent typed selected facts", name, row, read, hit, re, he)
		}
	}
	if found != 1 {
		t.Fatal("real identity multiplicity", name, found, rows)
	}
}

func TestPGStatioUserIndexesSourceSelection(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, column := range []int{-1, 0, 1, 3, 4} {
			t.Run(fmt.Sprintf("%s/reject_%d", mode, column), func(t *testing.T) {
				system := column == -1
				e, r, in, stamp := statioUserReader(t, mode, system)
				prefix := ""
				if system {
					prefix = "information_schema."
				}
				x := statioKey(t, e, prefix+"user_reader_x_i")
				if !system {
					in.Keep = pgcatalog.Keep{Column: column, Test: func(sqltype.Value) bool { return false }}
				}
				if got := statioUserProduce(t, in, pgcatalog.StatioUserIndexesOID); len(got) != 0 {
					t.Fatal("excluded row returned", got)
				}
				if len(r.fetched) != 0 || !stamp(false).IsNull() {
					t.Fatal("excluded row demanded Reader", r.fetched)
				}
				var writer statistics.Writer
				writer.IndexBlock(&e.Statistics, x, statistics.Policy{Counts: true}, false)
				writer.Flush(&e.Statistics, time.Now(), true)
				in.Keep = pgcatalog.Keep{}
				oid := pgcatalog.StatioUserIndexesOID
				if system {
					oid = pgcatalog.StatioAllIndexesOID
				}
				rows := statioUserProduce(t, in, oid)
				statioUserWantPair(t, rows, "user_reader_x_i", 1, 0)
				statioUserWantPair(t, rows, "user_reader_y_i", 0, 0)
				if len(r.fetched) != 2 {
					t.Fatal("accepted demand", r.fetched)
				}
				if stamp(false).IsNull() != (mode != "snapshot") {
					t.Fatal("lazy snapshot timestamp")
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				in.Context = ctx
				before := r.fetched[x.Object.Address]
				produce, _ := pgcatalog.ProducerFor(oid)
				if _, err := produce(in); !errors.Is(err, context.Canceled) {
					t.Fatal("source cancellation", err)
				}
				if r.fetched[x.Object.Address] != before {
					t.Fatal("precanceled provider demand")
				}
			})
		}
	}
}

func TestPGStatioUserIndexesReaderConsistency(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, first := range []ids.OID{pgcatalog.StatioAllIndexesOID, pgcatalog.StatioUserIndexesOID} {
			for _, column := range []int{0, 1, 3, 4} {
				t.Run(fmt.Sprintf("%s/%d/%d", mode, first, column), func(t *testing.T) {
					e, r, in, stamp := statioUserReader(t, mode, false)
					x, y := statioKey(t, e, "user_reader_x_i"), statioKey(t, e, "user_reader_y_i")
					parent := enginetest.RoleTableRef(t, e, "user_reader_x")
					var writer statistics.Writer
					writer.IndexBlock(&e.Statistics, x, statistics.Policy{Counts: true}, false)
					writer.Flush(&e.Statistics, time.Now(), true)
					in.Keep = pgcatalog.Keep{Column: column, Test: func(v sqltype.Value) bool {
						if column == 0 || column == 1 {
							n, err := v.OID()
							want := uint32(x.Object.Address.ObjectID)
							if column == 0 {
								want = uint32(parent.Address.ObjectID)
							}
							return err == nil && n == want
						}
						n, err := v.Text()
						want := "user_reader_x_i"
						if column == 3 {
							want = "user_reader_x"
						}
						return err == nil && n == want
					}}
					if !stamp(false).IsNull() {
						t.Fatal("eager timestamp")
					}
					old := statioUserProduce(t, in, first)
					statioUserWantPair(t, old, "user_reader_x_i", 1, 0)
					if len(old) != 1 || len(r.fetched) != 1 || r.fetched[x.Object.Address] != 1 {
						t.Fatal("Keep read rejected Y", r.fetched, old)
					}
					for range 2 {
						writer.IndexBlock(&e.Statistics, x, statistics.Policy{Counts: true}, true)
					}
					writer.IndexBlock(&e.Statistics, y, statistics.Policy{Counts: true}, false)
					writer.Add(&e.Statistics, statistics.Key{Object: parent}, statistics.Fact{Inserts: 7})
					writer.Flush(&e.Statistics, time.Now(), true)
					second := pgcatalog.StatioUserIndexesOID
					if first == second {
						second = pgcatalog.StatioAllIndexesOID
					}
					in.Keep = pgcatalog.Keep{}
					next := statioUserProduce(t, in, second)
					hit := int64(0)
					readY := int64(1)
					if mode == "none" {
						hit = 2
					}
					if mode == "snapshot" {
						readY = 0
					}
					statioUserWantPair(t, next, "user_reader_x_i", 1, hit)
					statioUserWantPair(t, next, "user_reader_y_i", readY, 0)
					// Same Reader across named index and table consumers, not merely
					// equal numbers from two independent projection-owned caches.
					wantInserts := int64(7)
					if mode == "snapshot" {
						wantInserts = 0
					}
					if got := r.TableStatistics(parent.Address).Inserts; got != wantInserts {
						t.Fatal("shared table/index cut", got, wantInserts)
					}
					stamp(true)
					if !stamp(false).IsNull() {
						t.Fatal("clear kept timestamp")
					}
					fresh := statioUserProduce(t, in, first)
					statioUserWantPair(t, fresh, "user_reader_x_i", 1, 2)
					statioUserWantPair(t, fresh, "user_reader_y_i", 1, 0)
					statioUserWantPair(t, old, "user_reader_x_i", 1, 0)
					if stamp(false).IsNull() != (mode != "snapshot") {
						t.Fatal("clear did not reset lazy policy")
					}
				})
			}
		}
	}
	t.Run("authenticated_capture_generation_terminal_peer", func(t *testing.T) {
		e := engine.New()
		enginetest.Run(t, e, "CREATE TABLE user_capture(k integer)")
		enginetest.Run(t, e, "CREATE INDEX user_capture_i ON user_capture(k)")
		key := statioKey(t, e, "user_capture_i")
		var writer statistics.Writer
		writer.IndexBlock(&e.Statistics, key, statistics.Policy{Counts: true}, false)
		writer.Flush(&e.Statistics, time.Now(), true)
		s, peer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		configureStats(s, "snapshot")
		tx := enginetest.TransactionBegin(t, s)
		op := enginetest.TransactionOperation(t, s)
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		stampType := enginetest.Usage(e, sqltype.Timestamptz).Type
		ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context(), Host: core.HostOf(scope)}}
		stamp := func() sqltype.Value {
			v, err := ctx.StatsSnapshotValue(false, stampType)
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
		capture := func(scope *core.StatementScope) [][]sqltype.Value {
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(scope.Op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatioUserIndexesOID}
			source := auth.Catalog.MustRelation(address)
			// Exercise actual core policy rather than injecting runtime Auth:
			// current catalog/roles/settings and the admitted session's Reader.
			captured, err := core.EngineOpenCatalogSourceLocked(e, scope, auth.Ref(address), source)
			if err != nil {
				t.Fatal(err)
			}
			defer captured.Close()
			return captured.Anonymous
		}
		if !stamp().IsNull() {
			t.Fatal("eager admitted source timestamp")
		}
		old := capture(scope)
		statioUserWantPair(t, old, "user_capture_i", 1, 0)
		for range 2 {
			writer.IndexBlock(&e.Statistics, key, statistics.Policy{Counts: true}, true)
		}
		writer.Flush(&e.Statistics, time.Now(), true)
		statioUserWantPair(t, capture(scope), "user_capture_i", 1, 0)
		configureStats(s, "none")
		configureStats(s, "snapshot")
		if !stamp().IsNull() {
			t.Fatal("settings generation retained USER cut")
		}
		statioUserWantPair(t, capture(scope), "user_capture_i", 1, 2)
		statioUserWantPair(t, old, "user_capture_i", 1, 0)
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		op.Done()
		terminal := enginetest.TransactionOperation(t, s)
		if _, err := e.Rollback(terminal, tx); err != nil {
			t.Fatal(err)
		}
		terminal.Done()
		writer.IndexBlock(&e.Statistics, key, statistics.Policy{Counts: true}, true)
		writer.Flush(&e.Statistics, time.Now(), true)
		for _, actor := range []*session.Session{s, peer} {
			op := enginetest.TransactionOperation(t, actor)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context(), Host: core.HostOf(scope)}}
			if v, err := ctx.StatsSnapshotValue(false, stampType); err != nil || !v.IsNull() {
				t.Fatal("terminal/peer inherited USER Reader", v, err)
			}
			statioUserWantPair(t, capture(scope), "user_capture_i", 1, 3)
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			op.Done()
		}
	})
}
