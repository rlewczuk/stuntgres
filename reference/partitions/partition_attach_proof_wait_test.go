package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func partitionAttachProofInvalidRows(t *testing.T) {
	for _, branch := range []string{"candidate", "default_candidate", "incumbent_default"} {
		for _, nested := range []bool{false, true} {
			for _, check := range []string{"nullable", "not_valid"} {
				name := branch + "/regular/" + check
				if nested {
					name = branch + "/nested/" + check
				}
				t.Run("reject_unproven/"+name, func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
							run(fixture.Exec("CREATE TABLE pa_proof_parent(k INT,j INT) PARTITION BY RANGE(k)", 0))
							root := "pa_proof_candidate"
							if branch == "incumbent_default" {
								root = "pa_proof_default"
							}
							predicate := "k>=1 AND k<3"
							if branch != "candidate" {
								predicate = "k<1 OR k>=3"
							}
							if branch != "candidate" && check == "nullable" {
								predicate = "j>=0"
							}
							constraint := ""
							if check == "nullable" {
								constraint = ",CONSTRAINT pa_proof_check CHECK(" + predicate + ")"
							}
							part := ""
							if nested {
								part = " PARTITION BY RANGE(j)"
							}
							if branch == "incumbent_default" && check == "nullable" {
								run(fixture.Exec("CREATE TABLE "+root+" PARTITION OF pa_proof_parent("+constraint[1:]+") DEFAULT"+part, 0))
							} else {
								run(fixture.Exec("CREATE TABLE "+root+"(k INT,j INT"+constraint+")"+part, 0))
							}
							leaf := root
							if nested {
								leaf = root + "_leaf"
								if check == "not_valid" {
									run(fixture.Exec("CREATE TABLE "+leaf+"(k INT,j INT)", 0))
								} else {
									run(fixture.Exec("CREATE TABLE "+leaf+" PARTITION OF "+root+" DEFAULT", 0))
								}
							}
							value := "NULL,9"
							if check == "not_valid" || branch != "candidate" {
								value = "2,9"
							}
							if branch == "candidate" && check == "not_valid" {
								value = "3,9"
							}
							run(fixture.Exec("INSERT INTO "+leaf+" VALUES("+value+")", 1))
							if check == "not_valid" {
								run(fixture.Exec("ALTER TABLE "+leaf+" ADD CONSTRAINT pa_proof_check CHECK("+predicate+") NOT VALID", 0))
								if nested {
									run(fixture.Exec("ALTER TABLE "+root+" ATTACH PARTITION "+leaf+" DEFAULT", 0))
								}
								if branch == "incumbent_default" {
									run(fixture.Exec("ALTER TABLE pa_proof_parent ATTACH PARTITION "+root+" DEFAULT", 0))
								}
							}
							q := "ALTER TABLE pa_proof_parent ATTACH PARTITION pa_proof_candidate FOR VALUES FROM(1) TO(3)"
							message := `partition constraint of relation "` + leaf + `" is violated by some row`
							if branch == "default_candidate" {
								run(fixture.Exec("CREATE TABLE pa_proof_old PARTITION OF pa_proof_parent FOR VALUES FROM(1) TO(3)", 0))
								q = "ALTER TABLE pa_proof_parent ATTACH PARTITION pa_proof_candidate DEFAULT"
							}
							if branch == "incumbent_default" {
								run(fixture.Exec("CREATE TABLE pa_proof_candidate(k INT,j INT)", 0))
								message = `updated partition constraint for default partition "` + leaf + `" would be violated by some row`
							}
							// Nullable CHECK on a DEFAULT candidate cannot admit a row
							// inside a sibling's interval. The meaningful negative is a
							// NOT VALID check; the nullable case instead lacks relevant
							// exclusion evidence entirely (a harmless local predicate).
							t.Run("diagnostic", func(t *testing.T) {
								fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "23514", Message: message, Schema: "public", Table: leaf}))
							})
							run(fixture.Select("SELECT k,j FROM "+leaf, []string{"k", "j"}, []string{"INT4", "INT4"}, func() [][]any {
								if value == "NULL,9" {
									return [][]any{{nil, int64(9)}}
								}
								if value == "3,9" {
									return [][]any{{int64(3), int64(9)}}
								}
								return [][]any{{int64(2), int64(9)}}
							}()))
							run(fixture.Select("SELECT relispartition FROM pg_class WHERE relname='pa_proof_candidate'", []string{"relispartition"}, []string{"BOOL"}, [][]any{{false}}))
						})
					}
				})
			}
		}
	}
}

// Hold both roots before starting ATTACH. Real pg_locks establishes the first
// wait, then the second after release; worker start/order is never queue proof.
// Native has no public queue observer: shared candidate/default timeout probes
// below exercise genuine manager contention; private manager units will cover
// exact native grant order after the oracle policy is settled.
func partitionAttachProofWaitOrder(t *testing.T) {
	t.Run("root_wait_order", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE pa_proof_parent(k INT,j INT) PARTITION BY RANGE(k)", 0))
				run(fixture.Exec("CREATE TABLE pa_proof_default PARTITION OF pa_proof_parent DEFAULT", 0))
				run(fixture.Exec("CREATE TABLE pa_proof_candidate(k INT,j INT)", 0))
				candidate, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer candidate.Rollback()
				def, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer def.Rollback()
				fixture.RunStep(t, p.Ctx, candidate, b.Recorder, fixture.Select("SELECT k FROM pa_proof_candidate", []string{"k"}, []string{"INT4"}, nil))
				fixture.RunStep(t, p.Ctx, def, b.Recorder, fixture.Select("SELECT k FROM pa_proof_default", []string{"k"}, []string{"INT4"}, nil))
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				q := "ALTER TABLE pa_proof_parent ATTACH PARTITION pa_proof_candidate FOR VALUES FROM(1) TO(3)"
				if b.PG {
					pid := fixture.LockPID(t, p.Ctx, conn)
					ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
					defer cancel()
					type outcome struct {
						result sql.Result
						err    error
					}
					done := make(chan outcome, 1)
					joined := false
					defer func() {
						cancel()
						def.Rollback()
						candidate.Rollback()
						if !joined {
							select {
							case <-done:
							case <-time.After(5 * time.Second):
								t.Error("ATTACH worker did not join")
							}
						}
					}()
					go func() { r, e := conn.ExecContext(ctx, q); done <- outcome{r, e} }()
					fixture.LockWait(t, ctx, pid)
					// Pinned PostgreSQL18.0 obtains DEFAULT AX before candidate AX.
					partitionAttachWaitingLocks(t, ctx, pid, "pa_proof_default", false)
					if err := def.Rollback(); err != nil {
						t.Fatal(err)
					}
					var holder int
					if err := candidate.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&holder); err != nil {
						t.Fatal(err)
					}
					fixture.LockWaitBehind(t, ctx, pid, holder, fixture.LockOID(t, ctx, "pa_proof_candidate"), "AccessExclusiveLock")
					partitionAttachWaitingLocks(t, ctx, pid, "pa_proof_candidate", true)
					// SUE admits a parent-only reader while AX waits on candidate.
					fixture.RunStep(t, ctx, b.DB, b.Recorder, fixture.Select("SELECT k FROM ONLY pa_proof_parent", []string{"k"}, []string{"INT4"}, nil))
					if err := candidate.Rollback(); err != nil {
						t.Fatal(err)
					}
					select {
					case got := <-done:
						joined = true
						if got.err != nil {
							t.Fatal(got.err)
						}
						if got.result == nil {
							t.Fatal("successful ATTACH lost result")
						}
						n, e := got.result.RowsAffected()
						if e != nil || n != 0 {
							t.Fatalf("ATTACH count %d %v", n, e)
						}
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				} else {
					// Independent public contention, not fabricated pg_locks rows.
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET lock_timeout='50ms'", 0))
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"}))
					if err := def.Rollback(); err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"}))
					if err := candidate.Rollback(); err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, 0))
				}
				run(fixture.Select("SELECT d.deptype FROM pg_depend d JOIN pg_class c ON c.oid=d.objid JOIN pg_class p ON p.oid=d.refobjid WHERE c.relname='pa_proof_candidate' AND p.relname='pa_proof_parent' AND d.classid=1259 AND d.refclassid=1259 AND d.objsubid=0 AND d.refobjsubid=0", []string{"deptype"}, []string{"CHAR"}, [][]any{{"a"}}))
			})
		}
	})
}

func partitionAttachWaitingLocks(t *testing.T, ctx context.Context, pid int, waiting string, candidateReached bool) {
	t.Helper()
	rows, err := fixture.Admin.QueryContext(ctx, "SELECT c.relname,l.mode,l.granted FROM pg_locks l JOIN pg_class c ON c.oid=l.relation WHERE l.pid=$1 AND c.relname IN ('pa_proof_parent','pa_proof_candidate','pa_proof_default') ORDER BY c.relname,l.mode", pid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var name, mode string
		var granted bool
		if err := rows.Scan(&name, &mode, &granted); err != nil {
			t.Fatal(err)
		}
		wantMode := "AccessExclusiveLock"
		if name == "pa_proof_parent" {
			wantMode = "ShareUpdateExclusiveLock"
		}
		if mode != wantMode || granted != (name != waiting) {
			t.Errorf("lock %s %s granted=%t; expected %s granted=%t", name, mode, granted, wantMode, name != waiting)
		}
		got[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !got["pa_proof_parent"] || !got["pa_proof_default"] || got["pa_proof_candidate"] != candidateReached {
		t.Errorf("unexpected wait frontier %#v; candidate reached=%t", got, candidateReached)
	}
}
