package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePartitionDetachReplay(t *testing.T) {
	cases := []string{"childless_only", "attachment_completion", "detach_reattach", "real_child_replay", "real_default_replay", "childless_replay", "nested_constraint_replay"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					if name == "real_child_replay" || name == "real_default_replay" || name == "childless_replay" {
						partitionDetachMinimalReplayOracle(run, name)
						return
					}
					if name == "nested_constraint_replay" {
						partitionDetachNestedReplayOracle(t, p, b, run)
						return
					}
					run(fixture.Exec("CREATE TABLE pd_replay(k INT) PARTITION BY RANGE(k)", 0))
					if name == "childless_only" {
						// Pinned18 tests9 established the childless exception: valid.
						run(fixture.Exec("CREATE UNIQUE INDEX pd_replay_idx ON ONLY pd_replay(k)", 0))
						run(partitionConstraintIndexState("pd_replay_idx", true))
						run(fixture.Select("SELECT k FROM pd_replay", []string{"k"}, []string{"INT4"}, nil))
						return
					}
					run(fixture.Exec("CREATE TABLE pd_replay_a PARTITION OF pd_replay FOR VALUES FROM(1) TO(3)", 0))
					run(fixture.Exec("INSERT INTO pd_replay VALUES(1)", 1))
					if name == "detach_reattach" {
						run(fixture.Exec("CREATE TABLE pd_replay_b PARTITION OF pd_replay FOR VALUES FROM(3) TO(5)", 0))
					}
					run(fixture.Exec("CREATE UNIQUE INDEX pd_replay_idx ON ONLY pd_replay(k)", 0))
					run(partitionConstraintIndexState("pd_replay_idx", false))
					run(fixture.Exec("CREATE UNIQUE INDEX pd_replay_a_idx ON pd_replay_a(k)", 0))
					run(fixture.Exec("ALTER INDEX pd_replay_idx ATTACH PARTITION pd_replay_a_idx", 0))
					if name == "attachment_completion" || name == "detach_reattach" {
						if name == "attachment_completion" {
							run(partitionConstraintIndexState("pd_replay_idx", true))
						} else {
							run(partitionConstraintIndexState("pd_replay_idx", false))
						}
						run(partitionConstraintEdge("pd_replay_a_idx", "pd_replay_idx", "pd_replay_a"))
						if name == "detach_reattach" {
							run(fixture.Exec("ALTER TABLE pd_replay DETACH PARTITION pd_replay_b", 0))
							run(partitionConstraintIndexState("pd_replay_idx", false))
							run(fixture.Exec("ALTER TABLE pd_replay DETACH PARTITION pd_replay_a", 0))
							run(partitionConstraintIndexState("pd_replay_idx", false))
							run(partitionDetachTopEdges("pd_replay_a_idx", false))
							run(fixture.Exec("ALTER TABLE pd_replay ATTACH PARTITION pd_replay_a FOR VALUES FROM(1) TO(3)", 0))
							run(partitionConstraintIndexState("pd_replay_idx", false))
							run(partitionConstraintEdge("pd_replay_a_idx", "pd_replay_idx", "pd_replay_a"))
						}
						run(referenceRangeRows("SELECT k FROM pd_replay", 1))
						return
					}
				})
			}
		})
	}
}

func partitionDetachMinimalReplayOracle(run func(fixture.Step), name string) {
	run(fixture.Exec("CREATE TABLE pd_minimal(k INT) PARTITION BY RANGE(k)", 0))
	if name == "childless_replay" {
		run(fixture.Exec("CREATE TABLE pd_minimal_transient PARTITION OF pd_minimal DEFAULT", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pd_minimal_idx ON ONLY pd_minimal(k)", 0))
		run(partitionConstraintIndexState("pd_minimal_idx", false))
		run(fixture.Exec("ALTER TABLE pd_minimal DETACH PARTITION pd_minimal_transient", 0))
		run(fixture.Exec("DROP TABLE pd_minimal_transient", 0))
		run(partitionConstraintIndexState("pd_minimal_idx", false))
		run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='pd_minimal_transient'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT k FROM pd_minimal", []string{"k"}, []string{"INT4"}, nil))
		return
	}
	run(fixture.Exec("CREATE TABLE pd_minimal_a PARTITION OF pd_minimal FOR VALUES FROM(1) TO(3)", 0))
	run(fixture.Exec("INSERT INTO pd_minimal VALUES(1)", 1))
	if name == "real_default_replay" {
		run(fixture.Exec("CREATE TABLE pd_minimal_def PARTITION OF pd_minimal DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pd_minimal VALUES(9)", 1))
	}
	run(fixture.Exec("CREATE UNIQUE INDEX pd_minimal_idx ON ONLY pd_minimal(k)", 0))
	run(partitionConstraintIndexState("pd_minimal_idx", false))
	target, index, bound := "pd_minimal_a", "pd_minimal_a_idx", " FOR VALUES FROM(1) TO(3)"
	if name == "real_default_replay" {
		run(fixture.Exec("CREATE UNIQUE INDEX pd_minimal_a_idx ON pd_minimal_a(k)", 0))
		run(fixture.Exec("ALTER INDEX pd_minimal_idx ATTACH PARTITION pd_minimal_a_idx", 0))
		target, index, bound = "pd_minimal_def", "pd_minimal_def_idx", " DEFAULT"
	}
	run(fixture.Exec("ALTER TABLE pd_minimal DETACH PARTITION "+target, 0))
	run(partitionConstraintIndexState("pd_minimal_idx", false))
	// Build AFTER detachment, so replay does not rely on prior index history.
	run(fixture.Exec("CREATE UNIQUE INDEX "+index+" ON "+target+"(k)", 0))
	run(fixture.Exec("ALTER TABLE pd_minimal ATTACH PARTITION "+target+bound, 0))
	run(partitionConstraintIndexState("pd_minimal_idx", false))
	run(partitionConstraintEdge(index, "pd_minimal_idx", target))
	run(partitionConstraintIndexState(index, true))
	if name == "real_default_replay" {
		run(referenceRangeRows("SELECT k FROM pd_minimal ORDER BY k", 1, 9))
	} else {
		run(referenceRangeRows("SELECT k FROM pd_minimal", 1))
	}
	run(partitionConstraintDuplicate("INSERT INTO pd_minimal VALUES(1)", "pd_minimal_a", "pd_minimal_a_idx", "Key (k)=(1) already exists."))
}

func partitionDetachNestedReplayOracle(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE pd_nr(k INT NOT NULL,j INT NOT NULL,payload TEXT,CONSTRAINT pd_nr_pk PRIMARY KEY(k,j)) PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE pd_nm PARTITION OF pd_nr DEFAULT PARTITION BY RANGE(j)", 0))
	run(fixture.Exec("CREATE TABLE pd_nl PARTITION OF pd_nm DEFAULT", 0))
	run(fixture.Exec("INSERT INTO pd_nr VALUES(1,1,'a')", 1))
	q := "SELECT oid::BIGINT,conname,conindid::BIGINT FROM pg_constraint WHERE conrelid='pd_nm'::regclass::OID OR conrelid='pd_nl'::regclass::OID ORDER BY oid"
	identities := partitionDetachCapture(t, p, b.DB, q)
	// Assemble final tables/PK first. Reconstruct the invalid-complete lower
	// index through its real VALID physical child, without detaching the mid.
	run(fixture.Exec("CREATE UNIQUE INDEX pd_nm_extra ON ONLY pd_nm(k,j,payload)", 0))
	run(partitionConstraintIndexState("pd_nm_extra", false))
	run(fixture.Exec("ALTER TABLE pd_nm DETACH PARTITION pd_nl", 0))
	run(partitionConstraintIndexState("pd_nm_extra", false))
	run(fixture.Exec("CREATE UNIQUE INDEX pd_nl_extra ON pd_nl(k,j,payload)", 0))
	run(fixture.Exec("ALTER TABLE pd_nm ATTACH PARTITION pd_nl DEFAULT", 0))
	run(partitionConstraintIndexState("pd_nm_extra", false))
	// An attached INVALID child is itself the reason for root invalidity.
	// Explicit index attachment cannot complete valid coverage, and must not
	// invent the duplicate valid mid/leaf seen with the rejected table recipe.
	run(fixture.Exec("CREATE UNIQUE INDEX pd_nr_extra ON ONLY pd_nr(k,j,payload)", 0))
	run(fixture.Exec("ALTER INDEX pd_nr_extra ATTACH PARTITION pd_nm_extra", 0))
	partitionDetachNestedState(t, p, b.DB, b.Recorder)
	// Stable source identities are distinct from relational remint assertions.
	run(fixture.Select(q, []string{"oid", "conname", "conindid"}, []string{"INT8", "NAME", "INT8"}, identities))
	if b.PG {
		partitionDetachNestedFuture(t, p, b.DB, b.Recorder)
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	fresh := fixture.NewFixture(t, false, true)
	if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	partitionDetachNestedState(t, fresh, fresh.Memory, fresh.MemoryRecord)
	partitionDetachNestedFuture(t, fresh, fresh.Memory, fresh.MemoryRecord)
	for range 2 {
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		partitionDetachNestedState(t, p, b.DB, b.Recorder)
		var again bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
			t.Fatal("combined nested replay changed canonical stream", err)
		}
	}
	if p.Postgres != nil {
		partitionAttachReplay(t, p, dump.String(), func(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, pg bool) {
			partitionDetachNestedState(t, p, x, r)
			partitionDetachNestedFuture(t, p, x, r)
		})
	}
	partitionDetachNestedFuture(t, p, b.DB, b.Recorder)
}

func partitionDetachNestedState(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, r, s) }
	run(partitionConstraintIndexState("pd_nr_extra", false))
	run(partitionConstraintIndexState("pd_nm_extra", false))
	run(partitionConstraintIndexState("pd_nr_pk", true))
	run(partitionConstraintIndexState("pd_nm_pkey", true))
	run(partitionConstraintIndexState("pd_nl_pkey", true))
	run(partitionConstraintEdge("pd_nm_extra", "pd_nr_extra", "pd_nm"))
	run(partitionConstraintEdge("pd_nl_extra", "pd_nm_extra", "pd_nl"))
	run(partitionConstraintEdge("pd_nm_pkey", "pd_nr_pk", "pd_nm"))
	run(partitionConstraintEdge("pd_nl_pkey", "pd_nm_pkey", "pd_nl"))
	run(partitionDetachMember("pd_nm", "pd_nr", true))
	run(partitionDetachMember("pd_nl", "pd_nm", true))
	run(fixture.Select("SELECT i.relname,t.relname AS table_name,i.relispartition,x.indisvalid,x.indisready,x.indislive FROM pg_class i JOIN pg_index x ON x.indexrelid=i.oid JOIN pg_class t ON t.oid=x.indrelid WHERE t.relname='pd_nr' OR t.relname='pd_nm' OR t.relname='pd_nl' ORDER BY i.relname", []string{"relname", "table_name", "relispartition", "indisvalid", "indisready", "indislive"}, []string{"NAME", "NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"pd_nl_extra", "pd_nl", true, true, true, true}, {"pd_nl_pkey", "pd_nl", true, true, true, true}, {"pd_nm_extra", "pd_nm", true, false, true, true}, {"pd_nm_pkey", "pd_nm", true, true, true, true}, {"pd_nr_extra", "pd_nr", false, false, true, true}, {"pd_nr_pk", "pd_nr", false, true, true, true}}))
	run(fixture.Select("SELECT c.conname,i.relname AS backing_name,c.conparentid=0 AS no_parent FROM pg_constraint c JOIN pg_class i ON i.oid=c.conindid WHERE c.contype='p' AND (c.conrelid='pd_nr'::regclass::OID OR c.conrelid='pd_nm'::regclass::OID OR c.conrelid='pd_nl'::regclass::OID) ORDER BY c.conname", []string{"conname", "backing_name", "no_parent"}, []string{"NAME", "NAME", "BOOL"}, [][]any{{"pd_nl_pkey", "pd_nl_pkey", false}, {"pd_nm_pkey", "pd_nm_pkey", false}, {"pd_nr_pk", "pd_nr_pk", true}}))
	run(fixture.Select("SELECT child.conname,parent.conname AS parent_name FROM pg_constraint child JOIN pg_constraint parent ON parent.oid=child.conparentid WHERE child.contype='p' AND (child.conrelid='pd_nm'::regclass::OID OR child.conrelid='pd_nl'::regclass::OID) ORDER BY child.conname", []string{"conname", "parent_name"}, []string{"NAME", "NAME"}, [][]any{{"pd_nl_pkey", "pd_nm_pkey"}, {"pd_nm_pkey", "pd_nr_pk"}}))
	run(fixture.Select("SELECT k,j,payload FROM pd_nr", []string{"k", "j", "payload"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), "a"}}))
	run(partitionConstraintDuplicate("INSERT INTO pd_nr VALUES(1,1,'duplicate')", "pd_nl", "pd_nl_pkey", "Key (k, j)=(1, 1) already exists."))
}

func partitionDetachNestedFuture(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, r, s) }
	run(fixture.Exec("INSERT INTO pd_nr VALUES(2,2,'new')", 1))
	run(fixture.Select("SELECT k,j,payload FROM pd_nl ORDER BY k", []string{"k", "j", "payload"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), "a"}, {int64(2), int64(2), "new"}}))
	run(partitionConstraintDuplicate("INSERT INTO pd_nr VALUES(2,2,'different')", "pd_nl", "pd_nl_pkey", "Key (k, j)=(2, 2) already exists."))
	run(fixture.ImplicitError("INSERT INTO pd_nr VALUES(2,NULL,'bad')", fixture.Diagnostic{Code: "23502", Message: `null value in column "j" of relation "pd_nl" violates not-null constraint`, Detail: "Failing row contains (2, null, bad).", Schema: "public", Table: "pd_nl", Column: "j"}))
}
