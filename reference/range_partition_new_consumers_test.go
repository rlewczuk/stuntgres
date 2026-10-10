package reference_test

import (
	"fmt"
	"testing"
)

func referenceRangeExtensionSetup(t *testing.T, p *referencePair, b referenceBackend) {
	for _, query := range []string{
		"CREATE SEQUENCE rp_ext_seq START 20",
		"CREATE TABLE rp_ext_root(k INT,payload INT DEFAULT nextval('rp_ext_seq')) PARTITION BY RANGE(k)",
		"CREATE TABLE rp_ext_middle PARTITION OF rp_ext_root DEFAULT PARTITION BY RANGE(k)",
		"CREATE TABLE rp_ext_leaf PARTITION OF rp_ext_middle(CONSTRAINT rp_ext_unique UNIQUE(payload)) DEFAULT",
	} {
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(query, 0))
	}
	referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO rp_ext_root VALUES(1,11)", 1))
}

func TestReferenceRangePartitionInsertConflictBoundary(t *testing.T) {
	for _, target := range []string{"rp_ext_root", "rp_ext_middle", "rp_ext_leaf"} {
		for _, prepared := range []bool{false, true} {
			t.Run(target+fmt.Sprint(prepared), func(t *testing.T) {
				referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					referenceRangeExtensionSetup(t, p, b)
					run := func(step referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, step) }
					bad := "INSERT INTO " + target + "(k) VALUES(2) ON CONFLICT ON CONSTRAINT rp_ext_absent DO NOTHING"
					step := referenceRangeError(bad, "42704", `constraint "rp_ext_absent" for table "`+target+`" does not exist`, referenceDiagnostic{})
					step.prepared = prepared
					step.errorStage = "execute"
					if prepared {
						step.errorStage = "prepare"
					}
					run(step)
					query := "INSERT INTO " + target + "(k) VALUES(2) ON CONFLICT DO NOTHING"
					step = referenceExec(query, 1)
					if !b.pg {
						step = referenceRangeError(query, "0A000", "ON CONFLICT on partition hierarchies is unsupported", referenceDiagnostic{})
					}
					step.prepared = prepared
					run(step)
					expected := [][]any{{int64(1), int64(11)}}
					next := int64(20)
					if b.pg {
						expected = append(expected, []any{int64(2), int64(20)})
						next = 21
					}
					run(referenceSelect("SELECT k,payload FROM rp_ext_root ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, expected))
					run(referenceSelect("SELECT nextval('rp_ext_seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{next}}))
					// The rebased ordinary provider still owns skip/revalidation and affected
					// count semantics; the hierarchy wrapper must not replace them with len(input).
					run(referenceExec("CREATE TABLE rp_ext_plain(k INT UNIQUE)", 0))
					run(referenceExec("INSERT INTO rp_ext_plain VALUES(1)", 1))
					step = referenceExec("INSERT INTO rp_ext_plain VALUES(1),(2),(2) ON CONFLICT DO NOTHING", 1)
					step.prepared = prepared
					run(step)
					run(referenceSelect("SELECT k FROM rp_ext_plain ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				})
			})
		}
	}
}

func TestReferenceRangePartitionMergeBoundary(t *testing.T) {
	for _, target := range []string{"rp_ext_root", "rp_ext_middle", "rp_ext_leaf"} {
		for _, prepared := range []bool{false, true} {
			t.Run(target+fmt.Sprint(prepared), func(t *testing.T) {
				referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					referenceRangeExtensionSetup(t, p, b)
					run := mergeInsertRun(t, p, b, prepared)
					query := "MERGE INTO " + target + " AS t USING (VALUES(2)) AS s(k) ON t.k=s.k WHEN NOT MATCHED THEN INSERT(k) VALUES(s.k)"
					step := referenceExec(query, 1)
					if !b.pg {
						step = referenceRangeError(query, "0A000", "merging partition hierarchies is unsupported", referenceDiagnostic{})
					}
					step.prepared = prepared
					run(step)
					expected := [][]any{{int64(1), int64(11)}}
					next := int64(20)
					if b.pg {
						expected = append(expected, []any{int64(2), int64(20)})
						next = 21
					}
					run(referenceSelect("SELECT k,payload FROM rp_ext_root ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, expected))
					run(referenceSelect("SELECT nextval('rp_ext_seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{next}}))
				})
			})
		}
	}
}

func TestReferenceRangePartitionCursorConsumer(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				referenceRangeExtensionSetup(t, p, b)
				referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO rp_ext_root VALUES(2,12)", 1))
				tx, err := b.db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				run := func(step referenceStep) { step.prepared = prepared; referenceRunStep(t, p.ctx, tx, b.recorder, step) }
				run(referenceExec("DECLARE rp_ext_cursor CURSOR FOR SELECT k,payload FROM rp_ext_root ORDER BY k", 0))
				run(referenceSelect("FETCH FORWARD 1 FROM rp_ext_cursor", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}}))
				run(referenceSelect("FETCH ALL FROM rp_ext_cursor", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(12)}}))
				run(referenceSelect("FETCH ALL FROM rp_ext_cursor", []string{"k", "payload"}, []string{"INT4", "INT4"}, nil))
				run(referenceExec("CLOSE rp_ext_cursor", 0))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT k,payload FROM rp_ext_root ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(12)}}))
			})
		})
	}
}
