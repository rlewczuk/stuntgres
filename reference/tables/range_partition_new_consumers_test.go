package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func referenceRangeExtensionSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	for _, query := range []string{
		"CREATE SEQUENCE rp_ext_seq START 20",
		"CREATE TABLE rp_ext_root(k INT,payload INT DEFAULT nextval('rp_ext_seq')) PARTITION BY RANGE(k)",
		"CREATE TABLE rp_ext_middle PARTITION OF rp_ext_root DEFAULT PARTITION BY RANGE(k)",
		"CREATE TABLE rp_ext_leaf PARTITION OF rp_ext_middle(CONSTRAINT rp_ext_unique UNIQUE(payload)) DEFAULT",
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(query, 0))
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO rp_ext_root VALUES(1,11)", 1))
}

func TestReferenceRangePartitionInsertConflictBoundary(t *testing.T) {
	for _, target := range []string{"rp_ext_root", "rp_ext_middle", "rp_ext_leaf"} {
		for _, prepared := range []bool{false, true} {
			t.Run(target+fmt.Sprint(prepared), func(t *testing.T) {
				referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					referenceRangeExtensionSetup(t, p, b)
					run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step) }
					bad := "INSERT INTO " + target + "(k) VALUES(2) ON CONFLICT ON CONSTRAINT rp_ext_absent DO NOTHING"
					step := referenceRangeError(bad, "42704", `constraint "rp_ext_absent" for table "`+target+`" does not exist`, fixture.Diagnostic{})
					step.Prepared = prepared
					step.ErrorStage = "execute"
					if prepared {
						step.ErrorStage = "prepare"
					}
					run(step)
					query := "INSERT INTO " + target + "(k) VALUES(2) ON CONFLICT DO NOTHING"
					step = fixture.Exec(query, 1)
					if !b.PG {
						step = referenceRangeError(query, "0A000", "ON CONFLICT on partition hierarchies is unsupported", fixture.Diagnostic{})
					}
					step.Prepared = prepared
					run(step)
					expected := [][]any{{int64(1), int64(11)}}
					next := int64(20)
					if b.PG {
						expected = append(expected, []any{int64(2), int64(20)})
						next = 21
					}
					run(fixture.Select("SELECT k,payload FROM rp_ext_root ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, expected))
					run(fixture.Select("SELECT nextval('rp_ext_seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{next}}))
					// The rebased ordinary provider still owns skip/revalidation and affected
					// count semantics; the hierarchy wrapper must not replace them with len(input).
					run(fixture.Exec("CREATE TABLE rp_ext_plain(k INT UNIQUE)", 0))
					run(fixture.Exec("INSERT INTO rp_ext_plain VALUES(1)", 1))
					step = fixture.Exec("INSERT INTO rp_ext_plain VALUES(1),(2),(2) ON CONFLICT DO NOTHING", 1)
					step.Prepared = prepared
					run(step)
					run(fixture.Select("SELECT k FROM rp_ext_plain ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				})
			})
		}
	}
}

func TestReferenceRangePartitionMergeBoundary(t *testing.T) {
	for _, target := range []string{"rp_ext_root", "rp_ext_middle", "rp_ext_leaf"} {
		for _, prepared := range []bool{false, true} {
			t.Run(target+fmt.Sprint(prepared), func(t *testing.T) {
				referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					referenceRangeExtensionSetup(t, p, b)
					run := fixture.MergeInsertRun(t, p, b, prepared)
					query := "MERGE INTO " + target + " AS t USING (VALUES(2)) AS s(k) ON t.k=s.k WHEN NOT MATCHED THEN INSERT(k) VALUES(s.k)"
					step := fixture.Exec(query, 1)
					if !b.PG {
						step = referenceRangeError(query, "0A000", "merging partition hierarchies is unsupported", fixture.Diagnostic{})
					}
					step.Prepared = prepared
					run(step)
					expected := [][]any{{int64(1), int64(11)}}
					next := int64(20)
					if b.PG {
						expected = append(expected, []any{int64(2), int64(20)})
						next = 21
					}
					run(fixture.Select("SELECT k,payload FROM rp_ext_root ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, expected))
					run(fixture.Select("SELECT nextval('rp_ext_seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{next}}))
				})
			})
		}
	}
}

func TestReferenceRangePartitionCursorConsumer(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				referenceRangeExtensionSetup(t, p, b)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO rp_ext_root VALUES(2,12)", 1))
				tx, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				run := func(step fixture.Step) { step.Prepared = prepared; fixture.RunStep(t, p.Ctx, tx, b.Recorder, step) }
				run(fixture.Exec("DECLARE rp_ext_cursor CURSOR FOR SELECT k,payload FROM rp_ext_root ORDER BY k", 0))
				run(fixture.Select("FETCH FORWARD 1 FROM rp_ext_cursor", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}}))
				run(fixture.Select("FETCH ALL FROM rp_ext_cursor", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(12)}}))
				run(fixture.Select("FETCH ALL FROM rp_ext_cursor", []string{"k", "payload"}, []string{"INT4", "INT4"}, nil))
				run(fixture.Exec("CLOSE rp_ext_cursor", 0))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT k,payload FROM rp_ext_root ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(12)}}))
			})
		})
	}
}
