package core_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Times the real mixed replacement executor and once-only Finish, not a
// staging helper. Setup, prior commands, exact payload checks and rollback
// are outside timing. Report allocations/bytes per batch and ns/proposal.
func BenchmarkInsertConflictUpdateDistinctStage(b *testing.B) {
	for _, mode := range []string{"replacement", "mixed", "prior_own"} {
		for _, size := range []int{128, 4096, 65536} {
			b.Run(fmt.Sprintf("%s/rows=%d", mode, size), func(b *testing.B) {
				b.StopTimer()
				e := engine.New()
				s, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))})
				if err != nil {
					b.Fatal(err)
				}
				defer func() {
					s.RequestEnd(session.CleanClose)
					if err := s.WaitEnd(context.Background()); err != nil {
						b.Error(err)
					}
				}()
				parse := func(q string) parser.Statement {
					stmt, err := parser.Parse(q)
					if err != nil {
						b.Fatal(err)
					}
					if len(stmt) != 1 {
						b.Fatalf("expected one benchmark statement, got %d", len(stmt))
					}
					return stmt[0]
				}
				execute := func(stmt parser.Statement) core.Result {
					op, err := s.Acquire(context.Background())
					if err != nil {
						b.Fatal(err)
					}
					result, err := e.Execute(op, stmt, 0, nil)
					op.Done()
					if err != nil {
						b.Fatal(err)
					}
					if result.Rows != nil {
						result.Rows.Close()
						b.Fatal("unexpected rowset")
					}
					return result
				}
				execute(parse("CREATE TABLE t(k INT UNIQUE,v INT)"))
				seedSize := size
				if mode == "mixed" {
					seedSize /= 2
				}
				execute(parse("INSERT INTO t VALUES" + conflictUpdateBatchValues(seedSize, 10)))
				begin := parse("BEGIN")
				prior := parse("UPDATE t SET v=11")
				rollback := parse("ROLLBACK")
				command := parse("INSERT INTO t VALUES" + conflictUpdateBatchValues(size, 1) + " ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v")
				ref := func() ids.ObjectAddress {
					for address, descriptor := range e.Catalog.State.Descriptors() {
						if relation, ok := descriptor.(*catalog.Relation); ok && relation.SQLIndex == nil && relation.Name() == "t" {
							return address
						}
					}
					b.Fatal("missing benchmark target")
					return ids.ObjectAddress{}
				}()
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					execute(begin)
					if mode == "prior_own" {
						execute(prior)
					}
					b.StartTimer()
					result := execute(command)
					b.StopTimer()
					if result.Count != int64(size) {
						b.Fatal("exact mixed action count", result.Count, size)
					}
					rows := s.Transactions.Writes[ref]
					if len(rows) != size {
						b.Fatal("exact staged rows", len(rows), size)
					}
					for i, row := range rows {
						want := int64(11)
						if mode == "prior_own" {
							want = 12
						}
						if i >= seedSize {
							want = 1
						}
						if enginetest.Export(row.Values[0]) != int64(i+1) || enginetest.Export(row.Values[1]) != want {
							b.Fatal("exact final row", i, row, want)
						}
					}
					execute(rollback)
				}
				b.ReportMetric(float64(size), "proposals/op")
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(size), "ns/proposal")
			})
		}
	}
}
