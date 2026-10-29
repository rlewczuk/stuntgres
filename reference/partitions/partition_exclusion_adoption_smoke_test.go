package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePartitionExclusionAdoptionFeatureSmoke(t *testing.T) {
	partitionExclusionCase(t, "nested_adoption_and_replay", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionAdoptionNested(run, "attach")
		middle := partitionAdoptionIdentity(t, p, b.DB, "pe_mid")
		leaf := partitionAdoptionIdentity(t, p, b.DB, "pe_leaf")
		fresh := partitionAdoptionIdentity(t, p, b.DB, "pe_fresh")
		q := "ALTER TABLE pe_root ATTACH PARTITION pe_mid DEFAULT"
		ddl, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer ddl.Close()
		conflictSQL := "INSERT INTO pe_mid VALUES(3,'10.0.0.1','[3,7)')"
		conflict, err := b.DB.PrepareContext(p.Ctx, conflictSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer conflict.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: ddl, SQL: q}, b.Recorder, fixture.Exec(q, 0))
		for range 2 {
			run(partitionAdoptionSame("pe_mid", middle))
			run(partitionAdoptionSame("pe_leaf", leaf))
			run(partitionAdoptionSame("pe_fresh", fresh))
			run(partitionAdoptionGraph(partitionAdoptionNestedRows(true)))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: conflict, SQL: conflictSQL}, b.Recorder, partitionExclusionConflict(conflictSQL, "pe_leaf", "pe_saved", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
			run(fixture.Exec("ALTER TABLE pe_root DETACH PARTITION pe_mid", 0))
			run(partitionAdoptionGraph(partitionAdoptionNestedRows(false)))
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO pe_root VALUES(4,'10.0.0.2','[3,7)')", 1))
		run(fixture.Exec("ALTER TABLE pe_leaf ADD CONSTRAINT pe_guard EXCLUDE USING btree(id WITH =)", 0))
		rows := partitionAdoptionNestedRows(true)
		rows = [][]any{rows[0], partitionAdoptionRow("pe_leaf", "pe_guard", nil, true, true, "i", true), rows[1], rows[2], rows[3]}
		check := func(x fixture.Executor, recorder *fixture.Recorder, pg bool) {
			t.Helper()
			apply := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, x, recorder, s) }
			apply(partitionAdoptionGraph(rows))
			s := fixture.Select("SELECT id,tenant,during FROM pe_root ORDER BY id", []string{"id", "tenant", "during"}, []string{"INT4", "INET", "INT4RANGE"}, [][]any{{int64(1), "10.0.0.1", "[1,5)"}, {int64(2), "10.0.0.129", "[1,5)"}, {int64(4), "10.0.0.2", "[3,7)"}})
			s.CheckMetadata = referenceRangeMetadata(pg, []string{"id", "tenant", "during"}, []string{"INT4", "INET", "INT4RANGE"}, []bool{true, true, true})
			apply(s)
			apply(fixture.Select("SELECT relname FROM pg_class WHERE relname LIKE '__stuntgres_partition_replay_%' ORDER BY relname", []string{"relname"}, []string{"NAME"}, nil))
			apply(fixture.Select("SELECT conname,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conname IN ('pe_ex','pe_local','pe_saved') ORDER BY conname", []string{"conname", "definition"}, []string{"NAME", "TEXT"}, [][]any{
				{"pe_ex", "EXCLUDE USING gist (tenant inet_ops WITH =, during WITH &&)"},
				{"pe_local", "EXCLUDE USING gist (tenant inet_ops WITH =, during WITH &&)"},
				{"pe_saved", "EXCLUDE USING gist (tenant inet_ops WITH =, during WITH &&)"},
			}))
			apply(partitionExclusionConflict(conflictSQL, "pe_leaf", "pe_saved", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
		}
		// The physical pe_fresh is deliberately not partitioned. Future membership
		// is exercised under the virtual middle after detaching its DEFAULT leaf.
		future := func(x fixture.Executor, recorder *fixture.Recorder) {
			apply := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, x, recorder, s) }
			apply(fixture.Exec("ALTER TABLE pe_mid DETACH PARTITION pe_fresh", 0))
			apply(fixture.Select("SELECT conislocal,coninhcount,connoinherit,conparentid=0 AS independent FROM pg_constraint WHERE conrelid='pe_fresh'::regclass::OID AND contype='x'", []string{"conislocal", "coninhcount", "connoinherit", "independent"}, []string{"BOOL", "INT2", "BOOL", "BOOL"}, [][]any{{true, int64(0), false, true}}))
			apply(fixture.Exec("CREATE TABLE pe_future PARTITION OF pe_mid DEFAULT", 0))
			apply(fixture.Exec("INSERT INTO pe_root VALUES(10,'10.0.0.130','[1,5)')", 1))
			apply(partitionExclusionConflict("INSERT INTO pe_root VALUES(11,'10.0.0.130','[3,7)')", "pe_future", "pe_future_tenant_during_excl", "10.0.0.130, [3,7)", "10.0.0.130, [1,5)"))
			apply(fixture.Exec("ALTER TABLE pe_root DROP CONSTRAINT pe_ex", 0))
			apply(partitionAdoptionGraph([][]any{
				partitionAdoptionRow("pe_fresh", "pe_fresh_tenant_during_excl", nil, true, false, "i", true),
				partitionAdoptionRow("pe_leaf", "pe_guard", nil, true, true, "i", true),
			}))
			apply(fixture.Exec(conflictSQL, 1))
			apply(fixture.ErrorDiagnostic("INSERT INTO pe_root VALUES(1,'10.0.0.3','[20,25)')", fixture.ExclusionReferenceConflict("pe_leaf", "pe_guard", "id", "1", "1")))
		}
		check(b.DB, b.Recorder, b.PG)
		if b.PG {
			future(b.DB, b.Recorder)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal("adoption restore", err, dump.String())
			}
			check(b.DB, b.Recorder, false)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
				t.Fatal("adoption canonical repeated redump", err, dump.String(), again.String())
			}
		}
		if p.Postgres != nil {
			oracle, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer oracle.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
					if _, err := oracle.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			if _, err := oracle.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("unchanged emitted adoption SQL", err, dump.String())
			}
			check(oracle, p.PostgresRecord, true)
			future(oracle, p.PostgresRecord)
		}
		future(b.DB, b.Recorder)
	})
	partitionExclusionCase(t, "detached_fresh_provenance_recipe", partitionAdoptionDetachedFreshReplay)
}

func partitionAdoptionDetachedFreshReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	partitionExclusionTree(run, "")
	run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)')", 1))
	ids := partitionAdoptionIdentity(t, p, b.DB, "pe_low")
	run(fixture.Exec("ALTER TABLE pe_root DETACH PARTITION pe_low", 0))
	run(fixture.Exec("ALTER TABLE pe_root DROP CONSTRAINT pe_ex", 0))
	run(partitionAdoptionSame("pe_low", ids))
	check := func(x fixture.Executor, recorder *fixture.Recorder) {
		apply := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, x, recorder, s) }
		apply(partitionAdoptionGraph([][]any{partitionAdoptionRow("pe_low", "pe_low_tenant_during_excl", nil, true, false, "i", true)}))
		apply(fixture.Select("SELECT id,tenant,during FROM pe_low ORDER BY id", []string{"id", "tenant", "during"}, []string{"INT4", "INET", "INT4RANGE"}, [][]any{{int64(1), "10.0.0.1", "[1,5)"}}))
		apply(partitionExclusionConflict("INSERT INTO pe_low VALUES(2,'10.0.0.1','[3,7)')", "pe_low", "pe_low_tenant_during_excl", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
	}
	check(b.DB, b.Recorder)
	partitionAdoptionRecipeReplay(t, p, b, check)
}

// This is an ordinary-SQL representability witness, not a native Dump stream.
// The temporary parent supplies a fresh inherited provider, then detachment
// localizes that provider without changing its connoinherit provenance.
func TestReferencePartitionExclusionAdoptionDetachedFreshRecipe(t *testing.T) {
	for _, shape := range []string{"leaf", "subtree", "reattached_subtree"} {
		partitionExclusionCase(t, "scratch_parent_clone_detach_"+shape, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := partitionConstraintRun(t, p, b, prepared)
			nested := shape != "leaf"
			partitioning, kind := "", "i"
			if nested {
				partitioning, kind = " PARTITION BY RANGE(tenant)", "I"
			}
			run(fixture.Exec("CREATE TABLE pe_low(id INT,tenant INET,during INT4RANGE)"+partitioning, 0))
			if nested {
				run(fixture.Exec("CREATE TABLE pe_leaf PARTITION OF pe_low DEFAULT", 0))
			}
			run(fixture.Exec("INSERT INTO pe_low VALUES(1,'10.0.0.1','[1,5)')", 1))
			run(fixture.Exec("CREATE TABLE pe_scratch(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
			run(fixture.Exec("ALTER TABLE pe_scratch ADD CONSTRAINT pe_seed EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
			run(fixture.Exec("ALTER TABLE pe_scratch ATTACH PARTITION pe_low DEFAULT", 0))
			before := partitionAdoptionIdentity(t, p, b.DB, "pe_low")
			graph := [][]any{
				partitionAdoptionRow("pe_low", "pe_low_tenant_during_excl", "pe_seed", false, false, kind, true),
				partitionAdoptionRow("pe_scratch", "pe_seed", nil, true, true, "I", true),
			}
			var leaf [2]int64
			if nested {
				leaf = partitionAdoptionIdentity(t, p, b.DB, "pe_leaf")
				graph = append([][]any{partitionAdoptionRow("pe_leaf", "pe_leaf_tenant_during_excl", "pe_low_tenant_during_excl", false, false, "i", true)}, graph...)
			}
			run(partitionAdoptionGraph(graph))
			run(fixture.Exec("ALTER TABLE pe_scratch DETACH PARTITION pe_low", 0))
			run(fixture.Exec("DROP TABLE pe_scratch", 0))
			run(partitionAdoptionSame("pe_low", before))
			run(fixture.Exec("ALTER INDEX pe_low_tenant_during_excl RENAME TO pe_saved", 0))
			graph = [][]any{partitionAdoptionRow("pe_low", "pe_saved", nil, true, false, kind, true)}
			if nested {
				run(partitionAdoptionSame("pe_leaf", leaf))
				graph = append([][]any{partitionAdoptionRow("pe_leaf", "pe_leaf_tenant_during_excl", "pe_saved", false, false, "i", true)}, graph...)
			}
			run(partitionAdoptionGraph(graph))
			if shape == "reattached_subtree" {
				run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,during INT4RANGE,CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")) PARTITION BY RANGE(tenant)", 0))
				run(fixture.Exec("ALTER TABLE pe_root ATTACH PARTITION pe_low DEFAULT", 0))
				run(partitionAdoptionSame("pe_low", before))
				run(partitionAdoptionSame("pe_leaf", leaf))
				graph = [][]any{
					partitionAdoptionRow("pe_leaf", "pe_leaf_tenant_during_excl", "pe_saved", false, false, "i", true),
					partitionAdoptionRow("pe_low", "pe_saved", "pe_ex", false, false, "I", true),
					partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", true),
				}
				run(partitionAdoptionGraph(graph))
			}
			rows := fixture.Select("SELECT id,tenant,during FROM pe_low ORDER BY id",
				[]string{"id", "tenant", "during"}, []string{"INT4", "INET", "INT4RANGE"},
				[][]any{{int64(1), "10.0.0.1", "[1,5)"}})
			run(rows)
			physical, provider := "pe_low", "pe_saved"
			if nested {
				physical, provider = "pe_leaf", "pe_leaf_tenant_during_excl"
			}
			conflict := partitionExclusionConflict("INSERT INTO pe_low VALUES(2,'10.0.0.1','[3,7)')", physical, provider, "10.0.0.1, [3,7)", "10.0.0.1, [1,5)")
			run(conflict)
			partitionAdoptionRecipeReplay(t, p, b, partitionAdoptionRecipeChecker(t, p, prepared, partitionAdoptionGraph(graph), rows, conflict))
			run(fixture.Exec("INSERT INTO pe_low VALUES(3,'10.0.0.2','[3,7)')", 1))
			if shape == "reattached_subtree" {
				run(fixture.Exec("ALTER TABLE pe_root DROP CONSTRAINT pe_ex", 0))
			} else {
				run(fixture.Exec("ALTER TABLE pe_low DROP CONSTRAINT pe_saved", 0))
			}
			run(partitionAdoptionGraph(nil))
			run(fixture.Exec("INSERT INTO pe_low VALUES(2,'10.0.0.1','[3,7)')", 1))
		})
	}
	partitionExclusionCase(t, "equivalent_multi_seed", partitionAdoptionEquivalentFreshRecipe)
	partitionExclusionCase(t, "mixed_gap_invalid_child_index_attach", partitionAdoptionMixedInvalidRecipe)
	partitionExclusionCase(t, "competing_true_local_recipe", partitionAdoptionCompetingLocalRecipe)
	partitionExclusionCase(t, "recursive_fresh_invalid_recipe", partitionAdoptionRecursiveFreshRecipe)
	for _, stale := range []bool{false, true} {
		name := "competing_final_explicit_valid"
		if stale {
			name = "competing_final_explicit_stale_invalid"
		}
		partitionExclusionCase(t, name, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			partitionAdoptionCompetingFinalRecipe(t, p, b, prepared, stale)
		})
	}
	partitionExclusionCase(t, "equivalent_stale_and_retained_invalid_interleaving", partitionAdoptionInterleavedFreshRecipe)
	// Final TABLE ATTACH is admitted only for RANGE. LIST/HASH target
	// construction/replay remains covered by dropped_invalid_fresh_child below;
	// this adoption cutover does not broaden their final ATTACH SQL surface.
	partitionExclusionCase(t, "competing_empty_invalid_parent_RANGE", partitionAdoptionEmptyInvalidCompetingRecipe)
	for _, method := range []string{"RANGE", "LIST", "HASH"} {
		for _, covered := range []bool{false, true} {
			name := "dropped_invalid_fresh_child_" + method + "_empty"
			if covered {
				name = "dropped_invalid_fresh_child_" + method + "_covered"
			}
			partitionExclusionCase(t, name, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				partitionAdoptionDroppedInvalidFreshRecipe(t, p, b, prepared, method, covered)
			})
		}
	}
}

func partitionAdoptionEquivalentFreshRecipe(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	run(fixture.Exec("CREATE TABLE pe_low(id INT,tenant INET,during INT4RANGE)", 0))
	run(fixture.Exec("INSERT INTO pe_low VALUES(1,'10.0.0.1','[1,5)')", 1))
	run(fixture.Exec("CREATE TABLE pe_scratch(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
	for _, name := range []string{"pe_seed_a", "pe_seed_b"} {
		run(fixture.Exec("ALTER TABLE pe_scratch ADD CONSTRAINT "+name+" EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	}
	run(fixture.Exec("ALTER TABLE pe_scratch ATTACH PARTITION pe_low DEFAULT", 0))
	names := []string{"pe_low_tenant_during_excl", "pe_low_tenant_during_excl1"}
	var identities [2][2]int64
	for i, name := range names {
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid,conindid FROM pg_constraint WHERE conrelid='pe_low'::regclass::OID AND conname=$1", name).Scan(&identities[i][0], &identities[i][1]); err != nil {
			t.Fatal(err)
		}
		if identities[i][0] <= 0 || identities[i][1] <= 0 || identities[i][0] == identities[i][1] {
			t.Fatal("invalid provider identities", identities)
		}
	}
	if identities[0][0] == identities[1][0] || identities[0][1] == identities[1][1] {
		t.Fatal("equivalent providers lost multiplicity", identities)
	}
	run(partitionAdoptionGraph([][]any{
		partitionAdoptionRow("pe_low", names[0], "pe_seed_a", false, false, "i", true),
		partitionAdoptionRow("pe_low", names[1], "pe_seed_b", false, false, "i", true),
		partitionAdoptionRow("pe_scratch", "pe_seed_a", nil, true, true, "I", true),
		partitionAdoptionRow("pe_scratch", "pe_seed_b", nil, true, true, "I", true),
	}))
	run(fixture.Exec("ALTER TABLE pe_scratch DETACH PARTITION pe_low", 0))
	run(fixture.Exec("DROP TABLE pe_scratch", 0))
	for i, name := range names {
		run(fixture.Select("SELECT oid=$1::OID AS same_constraint,conindid=$2::OID AS same_index FROM pg_constraint WHERE conrelid='pe_low'::regclass::OID AND conname=$3",
			[]string{"same_constraint", "same_index"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, identities[i][0], identities[i][1], name))
		run(fixture.Exec("ALTER INDEX "+name+" RENAME TO "+[]string{"pe_saved_a", "pe_saved_b"}[i], 0))
	}
	check := partitionAdoptionRecipeChecker(t, p, prepared,
		partitionAdoptionGraph([][]any{
			partitionAdoptionRow("pe_low", "pe_saved_a", nil, true, false, "i", true),
			partitionAdoptionRow("pe_low", "pe_saved_b", nil, true, false, "i", true),
		}),
		fixture.Select("SELECT id,tenant,during FROM pe_low ORDER BY id",
			[]string{"id", "tenant", "during"}, []string{"INT4", "INET", "INT4RANGE"}, [][]any{{int64(1), "10.0.0.1", "[1,5)"}}),
		partitionExclusionConflict("INSERT INTO pe_low VALUES(2,'10.0.0.1','[3,7)')", "pe_low", "pe_saved_a", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
	check(b.DB, b.Recorder)
	partitionAdoptionRecipeReplay(t, p, b, check)
	run(fixture.Exec("ALTER TABLE pe_low DROP CONSTRAINT pe_saved_a", 0))
	run(partitionAdoptionGraph([][]any{partitionAdoptionRow("pe_low", "pe_saved_b", nil, true, false, "i", true)}))
	run(partitionExclusionConflict("INSERT INTO pe_low VALUES(2,'10.0.0.1','[3,7)')", "pe_low", "pe_saved_b", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
	run(fixture.Exec("INSERT INTO pe_low VALUES(3,'10.0.0.2','[3,7)')", 1))
	run(fixture.Exec("ALTER TABLE pe_low DROP CONSTRAINT pe_saved_b", 0))
	run(partitionAdoptionGraph(nil))
	run(fixture.Exec("INSERT INTO pe_low VALUES(2,'10.0.0.1','[3,7)')", 1))
}

func partitionAdoptionMixedInvalidRecipe(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
	run(fixture.Exec("CREATE TABLE pe_gap PARTITION OF pe_root FOR VALUES FROM('10.0.0.0') TO('10.0.0.128')", 0))
	run(fixture.Exec("CREATE TABLE pe_mid PARTITION OF pe_root DEFAULT PARTITION BY RANGE(tenant)", 0))
	run(fixture.Exec("CREATE TABLE pe_mid_gap PARTITION OF pe_mid FOR VALUES FROM('10.0.0.128') TO('10.0.0.192')", 0))
	run(fixture.Exec("ALTER TABLE ONLY pe_mid ADD CONSTRAINT pe_local EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	run(fixture.Exec("CREATE TABLE pe_mid_cov PARTITION OF pe_mid DEFAULT", 0))
	middle := partitionAdoptionIdentity(t, p, b.DB, "pe_mid")
	covered := partitionAdoptionIdentity(t, p, b.DB, "pe_mid_cov")
	run(fixture.Exec("ALTER TABLE ONLY pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	for range 2 {
		run(fixture.Exec("ALTER INDEX pe_ex ATTACH PARTITION pe_local", 0))
		run(partitionAdoptionSame("pe_mid", middle))
		run(partitionAdoptionSame("pe_mid_cov", covered))
		run(partitionAdoptionGraph([][]any{
			partitionAdoptionRow("pe_mid", "pe_local", "pe_ex", false, true, "I", false),
			partitionAdoptionRow("pe_mid_cov", "pe_mid_cov_tenant_during_excl", "pe_local", false, false, "i", true),
			partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", false),
		}))
	}
	run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.200','[1,5)'),(2,'10.0.0.129','[1,5)'),(3,'10.0.0.1','[1,5)')", 3))
	run(partitionExclusionConflict("INSERT INTO pe_root VALUES(4,'10.0.0.200','[3,7)')", "pe_mid_cov", "pe_mid_cov_tenant_during_excl", "10.0.0.200, [3,7)", "10.0.0.200, [1,5)"))
	run(fixture.Exec("INSERT INTO pe_root VALUES(4,'10.0.0.129','[3,7)'),(5,'10.0.0.1','[3,7)')", 2))
	rows := fixture.Select("SELECT id,tenant,during FROM pe_root ORDER BY id",
		[]string{"id", "tenant", "during"}, []string{"INT4", "INET", "INT4RANGE"},
		[][]any{{int64(1), "10.0.0.200", "[1,5)"}, {int64(2), "10.0.0.129", "[1,5)"}, {int64(3), "10.0.0.1", "[1,5)"}, {int64(4), "10.0.0.129", "[3,7)"}, {int64(5), "10.0.0.1", "[3,7)"}})
	run(rows)
	partitionAdoptionRecipeReplay(t, p, b, partitionAdoptionRecipeChecker(t, p, prepared,
		partitionAdoptionGraph([][]any{
			partitionAdoptionRow("pe_mid", "pe_local", "pe_ex", false, true, "I", false),
			partitionAdoptionRow("pe_mid_cov", "pe_mid_cov_tenant_during_excl", "pe_local", false, false, "i", true),
			partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", false),
		}), rows,
		partitionExclusionConflict("INSERT INTO pe_root VALUES(6,'10.0.0.200','[3,7)')", "pe_mid_cov", "pe_mid_cov_tenant_during_excl", "10.0.0.200, [3,7)", "10.0.0.200, [1,5)")))
	run(fixture.Exec("ALTER TABLE pe_root DROP CONSTRAINT pe_ex", 0))
	run(partitionAdoptionGraph(nil))
	run(fixture.Exec("INSERT INTO pe_root VALUES(6,'10.0.0.200','[3,7)')", 1))
}
