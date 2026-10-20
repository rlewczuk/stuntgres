package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestTransactionUtilityCompletionBoundary(t *testing.T) {
	for _, query := range []string{"SET application_name TO changed", "SET application_name TO DEFAULT", "SHOW application_name"} {
		for _, boundary := range []string{"cancel_before", "end_before", "cancel_after", "replace_after"} {
			t.Run(query+"/"+boundary, func(t *testing.T) {
				e := New()
				s := sessionStart(t, e, nil)
				sessionSQL(t, s, "SET application_name TO initial")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, err := s.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				before := boundary == "cancel_before" || boundary == "end_before"
				if before {
					if err = scope.gate(statement(t, query)); err != nil {
						t.Fatal(err)
					}
				}
				if boundary == "cancel_before" {
					cancel()
				}
				if boundary == "end_before" {
					s.RequestEnd(session.Terminated)
				}
				var result Result
				var execErr error
				if before {
					// Exercise the settings installation seam itself, after the
					// scope's admission preflight but before installation.
					result, execErr = e.executeSetting(op, statement(t, query), 0, scope.implicit)
				} else {
					result, execErr = scope.Execute(statement(t, query), 0, nil)
				}
				if boundary == "cancel_after" {
					cancel()
				}
				if boundary == "replace_after" {
					if err = e.Reset(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				finishErr := scope.Finish(execErr)
				if boundary == "cancel_before" {
					if !errors.Is(execErr, context.Canceled) || !errors.Is(finishErr, context.Canceled) {
						t.Fatal(execErr, finishErr)
					}
				} else if boundary == "end_before" {
					if execErr == nil || finishErr == nil {
						t.Fatal("terminal admission installed utility")
					}
				} else if execErr != nil || finishErr != nil {
					t.Fatal(execErr, finishErr)
				}
				want := "initial"
				if boundary == "cancel_after" || boundary == "replace_after" {
					if query == "SET application_name TO changed" {
						want = "changed"
					}
					if query == "SET application_name TO DEFAULT" {
						want = ""
					}
				}
				// Inspect before releasing the lease: terminal cleanup cannot erase
				// the evidence of whether pre-install admission prevented mutation.
				s.Settings.Mu.Lock()
				got, ok := s.Settings.Values["application_name"]
				if !ok {
					got = s.Settings.Defaults["application_name"]
				}
				s.Settings.Mu.Unlock()
				if got != want {
					t.Fatalf("setting got %q want %q", got, want)
				}
				if result.Rows != nil {
					values := make([]sqltype.Value, 1)
					present, nextErr := result.Rows.Next(context.Background(), values)
					value, err := values[0].Text()
					if !present || nextErr != nil || err != nil || value != "initial" {
						t.Fatal("SHOW capture changed")
					}
					result.Rows.Close()
				}
				op.Done()
			})
		}
	}
}

func TestTransactionAppendScalingAndRelationRetention(t *testing.T) {
	for _, n := range []int{256, 512, 1024} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(v INT)")
			sessionSQL(t, s, "CREATE TABLE u(v INT)")
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			store := e.storage.Relations[ref.Address]
			changes, copied := 0, 0
			var retained *exec.FinalRows
			for i := range n {
				old := store.Versions
				sessionSQL(t, s, "INSERT INTO t VALUES(1)")
				if len(old) == 0 || &old[0] != &store.Versions[0] {
					changes++
					copied += len(old)
				}
				if i == 0 {
					retained = transactionRows(t, s, "SELECT v FROM t", 1)
				}
			}
			// Geometric growth copies 1+2+...+N/2=N-1 entries. This
			// structural work bound is deterministic, not a timing threshold.
			maxChanges := 1
			for size := 1; size < n; size *= 2 {
				maxChanges++
			}
			if changes > maxChanges || copied >= n {
				t.Fatalf("N=%d roots=%d copied=%d", n, changes, copied)
			}
			value, err := retained.Source.Row(0)[0].Integer()
			if retained.Count != 1 || err != nil || value != 1 {
				t.Fatal("append widened retained prefix")
			}
			for range n {
				sessionSQL(t, s, "INSERT INTO u VALUES(9)")
			}
			tx := transactionBegin(t, s)
			sessionSQL(t, s, "INSERT INTO t VALUES(2)")
			sessionSQL(t, s, "INSERT INTO u VALUES(3)")
			own := sessionSQL(t, s, "SELECT v FROM t").Rows.(*exec.FinalRows)
			defer own.Close()
			data := own.Source.View.Data()
			if len(data.Committed) != 1 || len(data.Own) != 1 || len(data.Committed[ref.Address]) != n || len(data.Own[ref.Address]) != 1 {
				t.Fatal("query retained unrelated committed/private relation roots")
			}
			transactionEnd(t, s, tx, false)
			if err := e.Reset(context.Background()); err != nil {
				t.Fatal(err)
			}
			value, err = own.Source.Row(n)[0].Integer()
			if own.Count != n+1 || err != nil || value != 2 || retained.Count != 1 {
				t.Fatal("replacement lost retained relation-local roots")
			}
			t.Logf("N=%d backing-root changes=%d copied-prefix entries=%d retained committed relations=%d private relations=%d", n, changes, copied, len(data.Committed), len(data.Own))
		})
	}
}

// These bounded probes report real allocation/time evidence when run by root;
// correctness tests above specify structural bounds independently of timing.
func BenchmarkTransactionAppend(b *testing.B) {
	for _, n := range []int{256, 512, 1024} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				e := New()
				s, err := e.StartSession(context.Background(), session.SessionStart{ID: 1})
				if err != nil {
					b.Fatal(err)
				}
				benchmarkTransactionSQL(b, s, "CREATE TABLE t(v INT)")
				stmt, err := parser.Parse("INSERT INTO t VALUES(1)")
				if err != nil {
					b.Fatal(err)
				}
				for range n {
					op, err := s.Acquire(context.Background())
					if err != nil {
						b.Fatal(err)
					}
					_, err = e.Execute(op, stmt[0], 0, nil)
					op.Done()
					if err != nil {
						b.Fatal(err)
					}
				}
				s.RequestEnd(session.CleanClose)
				if err := s.WaitEnd(context.Background()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchmarkTransactionSQL(b *testing.B, s *session.Session, query string) {
	b.Helper()
	stmt, err := parser.Parse(query)
	if err != nil {
		b.Fatal(err)
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		b.Fatal(err)
	}
	result, err := engineOf(s).Execute(op, stmt[0], 0, nil)
	op.Done()
	if err != nil {
		b.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
}

func BenchmarkTransactionRelationCapture(b *testing.B) {
	for _, relations := range []int{1, 256, 512} {
		b.Run(fmt.Sprint(relations), func(b *testing.B) {
			e := New()
			s, err := e.StartSession(context.Background(), session.SessionStart{ID: 1})
			if err != nil {
				b.Fatal(err)
			}
			defer func() {
				s.RequestEnd(session.CleanClose)
				_ = s.WaitEnd(context.Background())
			}()
			for i := range relations {
				benchmarkTransactionSQL(b, s, fmt.Sprintf("CREATE TABLE t%d(v INT)", i))
			}
			stmt, err := parser.Parse("SELECT v FROM t0")
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				op, err := s.Acquire(context.Background())
				if err != nil {
					b.Fatal(err)
				}
				result, err := e.Execute(op, stmt[0], 0, nil)
				op.Done()
				if err != nil {
					b.Fatal(err)
				}
				result.Rows.Close()
			}
		})
	}
}
