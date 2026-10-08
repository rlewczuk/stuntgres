package stuntgres_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

// Public operation admission derives a cancellable child context, and query
// finalization evaluates projections before publishing rows. Observe the real
// worker's selected provider frame, then cancel an ordinary context. Parent Err
// counters and canceling before Execute would not prove entered provider work.
func likeCancelEntered(t *testing.T, provider string, operation func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- operation(ctx) }()
	profiles := make([]runtime.StackRecord, 128)
	observed := false
	for !observed && ctx.Err() == nil {
		select {
		case err := <-result:
			t.Fatal("operation completed before provider observation", err)
		default:
		}
		n, ok := runtime.GoroutineProfile(profiles)
		if !ok {
			profiles = make([]runtime.StackRecord, n)
			continue
		}
		for _, profile := range profiles[:n] {
			frames := runtime.CallersFrames(profile.Stack())
			providerFrame, workerFrame := false, false
			for {
				frame, more := frames.Next()
				providerFrame = providerFrame || strings.Contains(frame.Function, provider)
				workerFrame = workerFrame || strings.Contains(frame.Function, "likeCancelEntered.func1")
				if !more {
					break
				}
			}
			if providerFrame && workerFrame {
				observed = true
				break
			}
		}
		if !observed {
			runtime.Gosched()
		}
	}
	cancel()
	err := <-result
	if !observed || !errors.Is(err, context.Canceled) {
		t.Fatal("entered provider cancellation", provider, observed, err)
	}
}

func TestLikeHeldEnteredCancellation(t *testing.T) {
	for _, tc := range []struct{ name, provider, input, pattern, escape string }{
		{"matcher", "sqltype.matchLike", strings.Repeat("a", 200000), "%" + strings.Repeat("a", 100000) + "b", "\\"},
		{"normalizer", "sqltype.LikeEscapePlan.Evaluate", "a", strings.Repeat("é!", 1000000), "!"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(v TEXT,p TEXT,e TEXT)")
			if _, err := db.Exec("INSERT INTO t VALUES($1,$2,$3)", tc.input, tc.pattern, tc.escape); err != nil {
				t.Fatal(err)
			}
			stmt, err := db.Prepare("SELECT v LIKE p ESCAPE e FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			likeCancelEntered(t, tc.provider, func(ctx context.Context) error {
				rows, err := stmt.QueryContext(ctx)
				if rows != nil {
					defer rows.Close()
					if rows.Next() {
						return errors.New("canceled provider published row")
					}
					if err == nil {
						err = rows.Err()
					}
				}
				return err
			})
			execSQL(t, db, "UPDATE t SET p='%',e='!'")
			rows, err := stmt.Query()
			compareOpsRows(t, rows, err, []string{"BOOL"}, [][]any{{true}})
		})
	}
}

func TestLikeHeldUpdateEnteredCancellation(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v TEXT)")
	input := strings.Repeat("a", 200000)
	if _, err := db.Exec("INSERT INTO t VALUES($1)", input); err != nil {
		t.Fatal(err)
	}
	stmt, err := db.Prepare("UPDATE t SET v='changed' WHERE v LIKE $1")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	likeCancelEntered(t, "sqltype.matchLike", func(ctx context.Context) error {
		_, err := stmt.ExecContext(ctx, "%"+strings.Repeat("a", 100000)+"b")
		return err
	})
	var value string
	if err := db.QueryRow("SELECT v FROM t").Scan(&value); err != nil || value != input {
		t.Fatal("canceled predicate published update", len(value), err)
	}
	r, err := stmt.Exec("%")
	if err != nil {
		t.Fatal("fresh held frame", err)
	}
	if n, err := r.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := db.QueryRow("SELECT v FROM t").Scan(&value); err != nil || value != "changed" {
		t.Fatal(value, err)
	}
}
