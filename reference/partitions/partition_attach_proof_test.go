package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

type partitionAttachProofCase struct {
	name                       string
	defaultBranch, nested      bool
	predicate, modifier        string
	notNull, expression, multi bool
	wantScan                   bool
}

func TestReferencePartitionAttachProof(t *testing.T) {
	// Scan calibration must precede implication observations. Utility scans may
	// not increment seq_scan; a failed calibration is an evidence gap, not proof
	// of scan elision. No native statistics view is invented here.
	calibrated := map[string]bool{}
	for _, defaultBranch := range []bool{false, true} {
		for _, nested := range []bool{false, true} {
			branch := "candidate"
			if defaultBranch {
				branch = "default"
			}
			if nested {
				branch += "/nested"
			} else {
				branch += "/regular"
			}
			c := partitionAttachProofCase{name: branch + "/no_proof", defaultBranch: defaultBranch, nested: nested, wantScan: true}
			t.Run(c.name+"/scan_calibration", func(t *testing.T) { calibrated[branch] = partitionAttachProofScan(t, c, true) })
			t.Run(c.name+"/locks", func(t *testing.T) { partitionAttachProofLocks(t, c) })
			if nested {
				t.Run(c.name+"/held_leaf", func(t *testing.T) { partitionAttachProofLeafWait(t, c) })
			}
			positive := "k IS NOT NULL AND k>=1 AND k<3"
			nullable := "k>=1 AND k<3"
			if defaultBranch {
				positive = "k IS NULL OR k<1 OR k>=3"
				nullable = "k<1 OR k>=3"
			}
			proofs := []partitionAttachProofCase{
				{name: "valid_check", predicate: positive},
				{name: "nullable_check", predicate: nullable, wantScan: !defaultBranch},
				{name: "not_valid_check", predicate: positive, modifier: "NOT VALID", wantScan: true},
				{name: "inherited_not_null", predicate: nullable, notNull: true},
			}
			if !nested {
				proofs = append(proofs, partitionAttachProofCase{name: "no_inherit_check", predicate: positive, modifier: "NO INHERIT"})
			}
			if !defaultBranch {
				proofs = append(proofs, partitionAttachProofCase{name: "expression_key", predicate: "(k+0) IS NOT NULL AND k+0>=1 AND k+0<3", expression: true})
				proofs = append(proofs, partitionAttachProofCase{name: "lexicographic_key", predicate: "k IS NOT NULL AND j IS NOT NULL AND (k>1 OR (k=1 AND j>=5)) AND (k<2 OR (k=2 AND j<0))", multi: true})
			}
			for _, proof := range proofs {
				proof.defaultBranch, proof.nested = defaultBranch, nested
				proof.name = branch + "/" + proof.name
				t.Run(proof.name+"/scan", func(t *testing.T) { partitionAttachProofScan(t, proof, calibrated[branch]) })
				t.Run(proof.name+"/locks", func(t *testing.T) { partitionAttachProofLocks(t, proof) })
				if nested {
					t.Run(proof.name+"/held_leaf", func(t *testing.T) { partitionAttachProofLeafWait(t, proof) })
				}
			}
		}
	}
	partitionAttachProofInvalidRows(t)
	partitionAttachProofWaitOrder(t)
}

// The same schema, data and ATTACH body supplies the scan and lock witnesses;
// only the observation mechanism differs. No row queries run between counter
// baseline and completion. Nested candidates have a different partition key.
func partitionAttachProofSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c partitionAttachProofCase, x fixture.Executor) (string, []string) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, s) }
	nn := ""
	if c.notNull {
		nn = " NOT NULL"
	}
	key := "k"
	if c.expression {
		key = "(k+0)"
	}
	if c.multi {
		key = "k,j"
	}
	run(fixture.Exec("CREATE TABLE pa_proof_parent(k INT"+nn+",j INT) PARTITION BY RANGE("+key+")", 0))
	name := "pa_proof_candidate"
	if c.defaultBranch {
		name = "pa_proof_default"
	}
	check := ""
	if c.predicate != "" && c.modifier != "NOT VALID" {
		check = ",CONSTRAINT pa_proof_check CHECK(" + c.predicate + ") " + c.modifier
	}
	part := ""
	if c.nested {
		part = " PARTITION BY RANGE(j)"
	}
	run(fixture.Exec("CREATE TABLE "+name+"(k INT"+nn+",j INT"+check+")"+part, 0))
	leaf := name
	if c.nested {
		leaf = name + "_leaf"
		if c.modifier == "NOT VALID" {
			run(fixture.Exec("CREATE TABLE "+leaf+"(k INT"+nn+",j INT)", 0))
		} else {
			run(fixture.Exec("CREATE TABLE "+leaf+" PARTITION OF "+name+" DEFAULT", 0))
		}
	}
	value := "1,9"
	if c.defaultBranch {
		value = "4,9"
	}
	run(fixture.Exec("INSERT INTO "+leaf+" VALUES("+value+")", 1))
	// An unvalidated fact belongs to an ordinary standalone physical table.
	// ATTACH establishes its subtree without enabling hierarchy CHECK ALTER.
	if c.modifier == "NOT VALID" {
		run(fixture.Exec("ALTER TABLE "+leaf+" ADD CONSTRAINT pa_proof_check CHECK("+c.predicate+") NOT VALID", 0))
		if c.nested {
			run(fixture.Exec("ALTER TABLE "+name+" ATTACH PARTITION "+leaf+" DEFAULT", 0))
		}
	}
	if c.defaultBranch {
		run(fixture.Exec("ALTER TABLE pa_proof_parent ATTACH PARTITION "+name+" DEFAULT", 0))
		run(fixture.Exec("CREATE TABLE pa_proof_candidate(k INT"+nn+",j INT)", 0))
	}
	bound := "FROM(1) TO(3)"
	if c.multi {
		bound = "FROM(1,5) TO(2,0)"
	}
	return "ALTER TABLE pa_proof_parent ATTACH PARTITION pa_proof_candidate FOR VALUES " + bound, []string{leaf}
}

func partitionAttachProofScan(t *testing.T, c partitionAttachProofCase, calibrated bool) bool {
	t.Helper()
	p := fixture.NewScenario(t)
	observable := false
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			q, leaves := partitionAttachProofSetup(t, p, b, c, conn)
			var before map[string]int64
			if b.PG {
				before = partitionAttachScanCounters(t, p.Ctx, conn, leaves)
			}
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, 0))
			if b.PG {
				after := partitionAttachScanCounters(t, p.Ctx, conn, leaves)
				observable = true
				for _, leaf := range leaves {
					delta := after[leaf] - before[leaf]
					if delta < 0 {
						t.Fatalf("scan counter decreased for %s: %d -> %d", leaf, before[leaf], after[leaf])
					}
					if !calibrated {
						observable = false
						t.Errorf("scan observation not calibrated: %s delta=%d; cannot infer elision", leaf, delta)
						continue
					}
					if c.wantScan && delta == 0 {
						observable = false
						t.Errorf("expected observable validation scan on %s; seq_scan %d -> %d (utility calibration may be unavailable)", leaf, before[leaf], after[leaf])
					}
					if !c.wantScan && delta != 0 {
						t.Errorf("expected CHECK-proven scan elision on %s; seq_scan %d -> %d", leaf, before[leaf], after[leaf])
					}
				}
			}
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT k,j FROM pa_proof_parent ORDER BY k", []string{"k", "j"}, []string{"INT4", "INT4"}, func() [][]any {
				if c.defaultBranch {
					return [][]any{{int64(4), int64(9)}}
				}
				return [][]any{{int64(1), int64(9)}}
			}()))
		})
	}
	return observable
}

func partitionAttachScanCounters(t *testing.T, ctx context.Context, conn *sql.Conn, names []string) map[string]int64 {
	t.Helper()
	// Flush on the executing physical session in a completed autocommit command.
	// The next command drops its cached statistics snapshot before observation.
	for _, q := range []string{"SELECT pg_stat_force_next_flush()", "SELECT pg_stat_clear_snapshot()"} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	out := make(map[string]int64, len(names))
	for _, name := range names {
		var count int64
		if err := conn.QueryRowContext(ctx, "SELECT seq_scan FROM pg_stat_user_tables WHERE relname=$1", name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		out[name] = count
	}
	return out
}

func partitionAttachProofLocks(t *testing.T, c partitionAttachProofCase) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			q, _ := partitionAttachProofSetup(t, p, b, c, conn)
			pid := 0
			if b.PG {
				pid = fixture.LockPID(t, p.Ctx, conn)
			}
			tx, err := conn.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec(q, 0))
			if b.PG {
				rows, err := fixture.Admin.QueryContext(p.Ctx, "SELECT c.relname,l.mode,l.granted FROM pg_locks l JOIN pg_class c ON c.oid=l.relation WHERE l.pid=$1 AND c.relname IN ('pa_proof_parent','pa_proof_candidate','pa_proof_candidate_leaf','pa_proof_default','pa_proof_default_leaf') ORDER BY c.relname,l.mode", pid)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var got [][]any
				for rows.Next() {
					var name, mode string
					var granted bool
					if err := rows.Scan(&name, &mode, &granted); err != nil {
						t.Fatal(err)
					}
					got = append(got, []any{name, mode, granted})
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				want := [][]any{{"pa_proof_candidate", "AccessExclusiveLock", true}}
				// Candidate descendants retain AX even when CHECK elides scans;
				// incumbent DEFAULT recursion is proof-dependent in PG18.0.
				if c.nested && !c.defaultBranch {
					want = append(want, []any{"pa_proof_candidate_leaf", "AccessExclusiveLock", true})
				}
				if c.defaultBranch {
					want = append(want, []any{"pa_proof_default", "AccessExclusiveLock", true})
					if c.nested && c.wantScan {
						want = append(want, []any{"pa_proof_default_leaf", "AccessExclusiveLock", true})
					}
				}
				want = append(want, []any{"pa_proof_parent", "ShareUpdateExclusiveLock", true})
				if !reflect.DeepEqual(got, want) {
					t.Errorf("retained attachment locks got %#v want %#v", got, want)
				}
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			// Shared public state witness follows rollback; no synthetic native
			// pg_locks surface substitutes for the actual private manager proof.
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT relispartition FROM pg_class WHERE relname='pa_proof_candidate'", []string{"relispartition"}, []string{"BOOL"}, [][]any{{false}}))
		})
	}
}
