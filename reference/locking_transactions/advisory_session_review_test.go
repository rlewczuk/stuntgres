package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceAdvisorySessionReviewCheckPreparation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, a, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			peer := advisorySessionConn(t, p, b)
			run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, a, record, s) }
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				for _, c := range []*sql.Conn{a, peer} {
					if _, err := c.ExecContext(ctx, "SELECT pg_advisory_unlock_all()"); err != nil {
						t.Error("bounded advisory cleanup", err)
					}
				}
			})
			assertQuiet := func() {
				t.Helper()
				if len(notices) != 0 {
					t.Fatalf("constant-NULL CHECK demanded warning-producing child: %#v", notices)
				}
			}
			assertFree := func(key int) {
				t.Helper()
				advisorySessionValue(t, p.Ctx, peer, fmt.Sprintf("SELECT pg_try_advisory_lock(%d)", key), "BOOL", true)
				advisorySessionValue(t, p.Ctx, peer, fmt.Sprintf("SELECT pg_advisory_unlock(%d)", key), "BOOL", true)
			}
			// Each outer pair-key call has a constant NULL, but its siblings are
			// genuinely volatile: count acquisition, unmatched-unlock warning,
			// and blocking acquisition of a peer-owned key. Row-dependent CASE
			// guards retain a real column dependency for ALTER's CHECK rewrite.
			run(fixture.Exec("CREATE TABLE advisory_review_null(v integer,CONSTRAINT review_acquire CHECK(CASE WHEN v>0 THEN pg_try_advisory_lock(NULL::integer,CASE WHEN pg_try_advisory_lock(3201) THEN 2 ELSE 3 END) ELSE true END),CONSTRAINT review_warning CHECK(CASE WHEN v>0 THEN pg_try_advisory_lock(NULL::integer,CASE WHEN pg_advisory_unlock(3202) THEN 2 ELSE 3 END) ELSE true END),CONSTRAINT review_block CHECK(CASE WHEN v>0 THEN pg_try_advisory_lock(NULL::integer,CASE WHEN pg_advisory_lock(3203) IS NOT NULL THEN 2 ELSE 3 END) ELSE true END))", 0))
			assertQuiet()
			assertFree(3201)
			advisorySessionValue(t, p.Ctx, peer, "SELECT pg_advisory_lock(3203)", "VOID", "")
			run(fixture.Exec("SET lock_timeout='150ms'", 0))
			run(fixture.Exec("INSERT INTO advisory_review_null VALUES(1),(2)", 2))
			assertQuiet()
			assertFree(3201)
			run(fixture.Exec("UPDATE advisory_review_null SET v=v+1", 2))
			assertQuiet()
			assertFree(3201)
			// Genuine held preparation stays effect-free, and repeated execution
			// must prepare detached executable CHECKs without mutating descriptors.
			q := "INSERT INTO advisory_review_null VALUES($1)"
			stmt, err := a.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("prepare constant-NULL CHECK insert", err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}
			for _, v := range []int{3, 4} {
				fixture.CheckRun(t, p.Ctx, held, record, fixture.Exec(q, 1, v))
				assertQuiet()
				assertFree(3201)
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("ALTER TABLE advisory_review_null ALTER COLUMN v TYPE bigint USING v::bigint", 0))
			assertQuiet()
			assertFree(3201)
			run(fixture.Select("SELECT v FROM advisory_review_null ORDER BY v", []string{"v"}, []string{"INT8"}, [][]any{{int64(2)}, {int64(3)}, {int64(3)}, {int64(4)}}))
			if !b.PG {
				// Dump retains analyzed syntax, not the prepared NULL replacement.
				// Restore executes the retained checks on its isolated stage; the
				// target's live peer-owned blocking key survives publication.
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"pg_try_advisory_lock", "pg_advisory_unlock", "pg_advisory_lock", "review_acquire", "review_warning", "review_block"} {
					if !strings.Contains(dump.String(), name) {
						t.Fatal("Dump lost stored CHECK syntax/dependency", name, dump.String())
					}
				}
				ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
				err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes()))
				cancel()
				if err != nil {
					t.Fatal("constant-NULL CHECK staged replay", err)
				}
				assertQuiet()
				assertFree(3201)
				advisorySessionValue(t, p.Ctx, a, "SELECT pg_try_advisory_lock(3203)", "BOOL", false)
				run(fixture.Exec("INSERT INTO advisory_review_null VALUES(5)", 1))
				assertQuiet()
				assertFree(3201)
				advisorySessionValue(t, p.Ctx, a, "SELECT count(*) FROM advisory_review_null", "INT8", int64(5))
			}
			advisorySessionValue(t, p.Ctx, peer, "SELECT pg_advisory_unlock(3203)", "BOOL", true)
			assertQuiet()

			// Runtime column NULL is different: children are demanded before
			// strictness. Keep this count witness beside constant-fold suppression.
			run(fixture.Exec("CREATE TABLE advisory_review_runtime(n integer,CONSTRAINT review_runtime CHECK(pg_try_advisory_lock(n,CASE WHEN pg_try_advisory_lock(3204) THEN 2 ELSE 3 END)))", 0))
			run(fixture.Exec("INSERT INTO advisory_review_runtime VALUES(NULL)", 1))
			advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(3204)", "BOOL", false)
			advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_unlock(3204)", "BOOL", true)
			assertFree(3204)
			assertQuiet()

			// PG cooks stored syntax at CREATE without folding, then prepares ALL
			// CHECK expressions before executing any volatile row check. The
			// demanded immutable child errors before the strict-NULL shortcut;
			// alphabetically earlier review_effect must not acquire its key.
			run(fixture.Exec("CREATE TABLE advisory_review_error(v integer,CONSTRAINT review_effect CHECK(pg_try_advisory_lock(3205)),CONSTRAINT review_immutable CHECK(pg_try_advisory_lock(NULL::integer,1/0)))", 0))
			want := fixture.Diagnostic{Code: "22012", Message: "division by zero"}
			run(fixture.ImplicitError("INSERT INTO advisory_review_error VALUES(1)", want))
			assertFree(3205)
			advisorySessionValue(t, p.Ctx, a, "SELECT count(*) FROM advisory_review_error", "INT8", int64(0))
			q = "INSERT INTO advisory_review_error VALUES($1)"
			badStmt, err := a.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("immutable CHECK error belongs to execution, not prepare", err)
			}
			defer badStmt.Close()
			badHeld := fixture.HeldStatement{Executor: a, Statement: badStmt, SQL: q}
			fixture.CheckRun(t, p.Ctx, badHeld, record, fixture.ImplicitError(q, want, 2))
			assertFree(3205)
			if err := badStmt.Close(); err != nil {
				t.Fatal(err)
			}
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_lock(3206)", "VOID", "")
				ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
				err := stuntgres.Restore(ctx, p.Native, strings.NewReader(dump.String()+"\nINSERT INTO advisory_review_error VALUES(3);"))
				cancel()
				full := advisorySessionTimeout("22012", "division by zero")
				fixture.AssertDiagnostic(t, err, &full)
				advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(3206)", "BOOL", false)
				advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_unlock(3206)", "BOOL", true)
				assertFree(3205)
				advisorySessionValue(t, p.Ctx, a, "SELECT count(*) FROM advisory_review_null", "INT8", int64(5))
				advisorySessionValue(t, p.Ctx, a, "SELECT count(*) FROM advisory_review_error", "INT8", int64(0))
			}
			assertQuiet()
		})
	}
}

func TestReferenceAdvisorySessionReviewQuotedArgumentLimit(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := advisorySessionConn(t, p, b)
			for _, name := range []string{"pg_advisory_lock", "pg_advisory_lock_shared", "pg_advisory_unlock", "pg_advisory_unlock_shared", "pg_advisory_unlock_all", "pg_try_advisory_lock", "pg_try_advisory_lock_shared", "pg_catalog.PG_ADVISORY_LOCK"} {
				quoted := `"` + strings.ToUpper(name) + `"`
				if strings.Contains(name, ".") {
					quoted = `"pg_catalog"."PG_ADVISORY_LOCK"`
				}
				for _, badChild := range []bool{false, true} {
					last := "1"
					if badChild {
						last = "missing_advisory_limit_child"
					}
					q := "SELECT " + quoted + "(" + strings.Repeat("1,", 100) + last + ")"
					want := advisorySessionTimeout("54023", "cannot pass more than 100 arguments to a function")
					want.Position = "8"
					if badChild {
						want = advisorySessionTimeout("42703", `column "missing_advisory_limit_child" does not exist`)
						want.Position = fmt.Sprint(strings.Index(q, last) + 1)
					}
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, want)
						s.SelectRows = true
						if prepared {
							s = fixture.Prepared(s)
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				}
			}
		})
	}
}
