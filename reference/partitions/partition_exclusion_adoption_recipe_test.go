package reference_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Exercise the actual native stream unchanged, not a separately reconstructed
// PostgreSQL recipe. Ordinary source construction is checked by both backends.
func partitionAdoptionRecipeReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, check func(fixture.Executor, *fixture.Recorder)) {
	t.Helper()
	if b.PG {
		return
	}
	var script bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &script); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(script.Bytes())); err != nil {
			t.Fatal("recipe stream restore", err, script.String())
		}
		check(b.DB, b.Recorder)
		var again bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(script.Bytes(), again.Bytes()) {
			t.Fatal("recipe canonical replacement", err, script.String(), again.String())
		}
	}
	if p.Postgres == nil {
		return
	}
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
	if _, err := oracle.ExecContext(p.Ctx, script.String()); err != nil {
		t.Fatal("unchanged emitted recipe SQL", err, script.String())
	}
	check(oracle, p.PostgresRecord)
}

func partitionAdoptionRecipeChecker(t *testing.T, p *fixture.Pair, prepared bool, steps ...fixture.Step) func(fixture.Executor, *fixture.Recorder) {
	t.Helper()
	return func(x fixture.Executor, recorder *fixture.Recorder) {
		t.Helper()
		for _, step := range steps {
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, x, recorder, step)
		}
	}
}

func partitionAdoptionCompetingLocalRecipe(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	run(fixture.Exec("CREATE TABLE pe_low(id INT,tenant INET,during INT4RANGE)", 0))
	run(fixture.Exec("INSERT INTO pe_low VALUES(1,'10.0.0.1','[1,5)')", 1))
	run(fixture.Exec("CREATE TABLE pe_scratch(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
	run(fixture.Exec("ALTER TABLE pe_scratch ADD CONSTRAINT pe_seed EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	run(fixture.Exec("ALTER TABLE pe_scratch ATTACH PARTITION pe_low DEFAULT", 0))
	identity := partitionAdoptionIdentity(t, p, b.DB, "pe_low")
	run(fixture.Exec("ALTER TABLE pe_scratch DETACH PARTITION pe_low", 0))
	run(fixture.Exec("DROP TABLE pe_scratch", 0))
	run(partitionAdoptionSame("pe_low", identity))
	run(fixture.Exec("ALTER INDEX pe_low_tenant_during_excl RENAME TO pe_fresh", 0))
	// Delaying only the equivalent true-local declaration prevents its adoption
	// in place of the fresh false provider that the scratch parent must create.
	run(fixture.Exec("ALTER TABLE pe_low ADD CONSTRAINT pe_local EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	check := partitionAdoptionRecipeChecker(t, p, prepared,
		partitionAdoptionGraph([][]any{
			partitionAdoptionRow("pe_low", "pe_fresh", nil, true, false, "i", true),
			partitionAdoptionRow("pe_low", "pe_local", nil, true, true, "i", true),
		}),
		fixture.Select("SELECT id,tenant,during FROM pe_low ORDER BY id", []string{"id", "tenant", "during"}, []string{"INT4", "INET", "INT4RANGE"}, [][]any{{int64(1), "10.0.0.1", "[1,5)"}}),
		partitionExclusionConflict("INSERT INTO pe_low VALUES(2,'10.0.0.1','[3,7)')", "pe_low", "pe_fresh", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
	check(b.DB, b.Recorder)
	partitionAdoptionRecipeReplay(t, p, b, check)
	run(fixture.Exec("ALTER TABLE pe_low DROP CONSTRAINT pe_fresh", 0))
	run(partitionAdoptionGraph([][]any{partitionAdoptionRow("pe_low", "pe_local", nil, true, true, "i", true)}))
	run(partitionExclusionConflict("INSERT INTO pe_low VALUES(2,'10.0.0.1','[3,7)')", "pe_low", "pe_local", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
	run(fixture.Exec("ALTER TABLE pe_low DROP CONSTRAINT pe_local", 0))
	run(partitionAdoptionGraph(nil))
	run(fixture.Exec("INSERT INTO pe_low VALUES(2,'10.0.0.1','[3,7)')", 1))
}

func partitionAdoptionRecursiveFreshRecipe(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	run(fixture.Exec("CREATE TABLE pe_mid(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
	run(fixture.Exec("CREATE TABLE pe_low PARTITION OF pe_mid DEFAULT PARTITION BY RANGE(tenant)", 0))
	run(fixture.Exec("CREATE TABLE pe_gap PARTITION OF pe_low FOR VALUES FROM('10.0.0.128') TO('10.0.0.192')", 0))
	run(fixture.Exec("ALTER TABLE ONLY pe_low ADD CONSTRAINT pe_local EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	run(fixture.Exec("CREATE TABLE pe_cov PARTITION OF pe_low DEFAULT", 0))
	local := partitionAdoptionIdentity(t, p, b.DB, "pe_low")
	covered := partitionAdoptionIdentity(t, p, b.DB, "pe_cov")
	run(fixture.Exec("CREATE TABLE pe_scratch(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
	run(fixture.Exec("ALTER TABLE pe_scratch ATTACH PARTITION pe_mid DEFAULT", 0))
	// Recursive ADD, unlike ATTACH after a seed declaration, adopts the existing
	// invalid local subtree without filling its gap and propagates invalidity.
	run(fixture.Exec("ALTER TABLE pe_scratch ADD CONSTRAINT pe_seed EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	fresh := partitionAdoptionIdentity(t, p, b.DB, "pe_mid")
	run(partitionAdoptionSame("pe_low", local))
	run(partitionAdoptionSame("pe_cov", covered))
	run(partitionAdoptionGraph([][]any{
		partitionAdoptionRow("pe_cov", "pe_cov_tenant_during_excl", "pe_local", false, false, "i", true),
		partitionAdoptionRow("pe_low", "pe_local", "pe_mid_tenant_during_excl", false, true, "I", false),
		partitionAdoptionRow("pe_mid", "pe_mid_tenant_during_excl", "pe_seed", false, false, "I", false),
		partitionAdoptionRow("pe_scratch", "pe_seed", nil, true, true, "I", false),
	}))
	run(fixture.Exec("ALTER TABLE pe_scratch DETACH PARTITION pe_mid", 0))
	run(fixture.Exec("DROP TABLE pe_scratch", 0))
	run(partitionAdoptionSame("pe_mid", fresh))
	run(fixture.Exec("INSERT INTO pe_mid VALUES(1,'10.0.0.200','[1,5)'),(2,'10.0.0.129','[1,5)'),(3,'10.0.0.129','[3,7)')", 3))
	rows := fixture.Select("SELECT id,tenant,during FROM pe_mid ORDER BY id", []string{"id", "tenant", "during"}, []string{"INT4", "INET", "INT4RANGE"}, [][]any{{int64(1), "10.0.0.200", "[1,5)"}, {int64(2), "10.0.0.129", "[1,5)"}, {int64(3), "10.0.0.129", "[3,7)"}})
	conflict := partitionExclusionConflict("INSERT INTO pe_mid VALUES(4,'10.0.0.200','[3,7)')", "pe_cov", "pe_cov_tenant_during_excl", "10.0.0.200, [3,7)", "10.0.0.200, [1,5)")
	graph := [][]any{
		partitionAdoptionRow("pe_cov", "pe_cov_tenant_during_excl", "pe_local", false, false, "i", true),
		partitionAdoptionRow("pe_low", "pe_local", "pe_mid_tenant_during_excl", false, true, "I", false),
		partitionAdoptionRow("pe_mid", "pe_mid_tenant_during_excl", nil, true, false, "I", false),
	}
	check := partitionAdoptionRecipeChecker(t, p, prepared, partitionAdoptionGraph(graph), rows, conflict)
	check(b.DB, b.Recorder)
	partitionAdoptionRecipeReplay(t, p, b, check)
	// The same false-origin subtree can be attached explicitly under an ancestor
	// that also retains a direct gap, without turning the false origin into true.
	run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
	run(fixture.Exec("ALTER TABLE pe_root ATTACH PARTITION pe_mid DEFAULT", 0))
	run(fixture.Exec("CREATE TABLE pe_root_gap PARTITION OF pe_root FOR VALUES FROM('10.0.0.0') TO('10.0.0.128')", 0))
	run(fixture.Exec("ALTER TABLE ONLY pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	run(fixture.Exec("ALTER INDEX pe_ex ATTACH PARTITION pe_mid_tenant_during_excl", 0))
	graph[2] = partitionAdoptionRow("pe_mid", "pe_mid_tenant_during_excl", "pe_ex", false, false, "I", false)
	graph = append(graph, partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", false))
	check = partitionAdoptionRecipeChecker(t, p, prepared, partitionAdoptionGraph(graph), rows, conflict)
	check(b.DB, b.Recorder)
	partitionAdoptionRecipeReplay(t, p, b, check)
	run(fixture.Exec("ALTER TABLE pe_root DROP CONSTRAINT pe_ex", 0))
	run(partitionAdoptionGraph(nil))
	run(fixture.Exec("INSERT INTO pe_mid VALUES(4,'10.0.0.200','[3,7)')", 1))
}

// A temporary invalid child supplies a real invalidity witness during recursive
// creation. Dropping it preserves the freshly inherited root's stale invalid
// flag, including after captured valid children are installed.
func partitionAdoptionDroppedInvalidFreshRecipe(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool, method string, covered bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	bound := "DEFAULT"
	if method == "HASH" {
		bound = "FOR VALUES WITH(MODULUS 1,REMAINDER 0)"
	}
	// Integer partition equality and B-tree EXCLUDE are already admitted on all
	// three methods; this recipe introduces no new hash type or access method.
	const definition = "EXCLUDE USING btree(tenant WITH =,id WITH =)"
	run(fixture.Exec("CREATE TABLE pe_mid(id INT,tenant INT,during INT4RANGE) PARTITION BY "+method+"(tenant)", 0))
	run(fixture.Exec("CREATE TABLE pe_dummy PARTITION OF pe_mid "+bound+" PARTITION BY "+method+"(tenant)", 0))
	run(fixture.Exec("CREATE TABLE pe_dummy_gap PARTITION OF pe_dummy "+bound, 0))
	run(fixture.Exec("ALTER TABLE ONLY pe_dummy ADD CONSTRAINT pe_dummy_ex "+definition, 0))
	run(fixture.Exec("CREATE TABLE pe_scratch(id INT,tenant INT,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
	run(fixture.Exec("ALTER TABLE pe_scratch ATTACH PARTITION pe_mid DEFAULT", 0))
	run(fixture.Exec("ALTER TABLE pe_scratch ADD CONSTRAINT pe_seed "+definition, 0))
	ids := partitionAdoptionIdentity(t, p, b.DB, "pe_mid")
	run(partitionAdoptionGraph([][]any{
		partitionAdoptionRow("pe_dummy", "pe_dummy_ex", "pe_mid_tenant_id_excl", false, true, "I", false),
		partitionAdoptionRow("pe_mid", "pe_mid_tenant_id_excl", "pe_seed", false, false, "I", false),
		partitionAdoptionRow("pe_scratch", "pe_seed", nil, true, true, "I", false),
	}))
	run(fixture.Exec("DROP TABLE pe_dummy", 0))
	run(partitionAdoptionSame("pe_mid", ids))
	run(partitionAdoptionGraph([][]any{
		partitionAdoptionRow("pe_mid", "pe_mid_tenant_id_excl", "pe_seed", false, false, "I", false),
		partitionAdoptionRow("pe_scratch", "pe_seed", nil, true, true, "I", false),
	}))
	run(fixture.Exec("ALTER TABLE pe_scratch DETACH PARTITION pe_mid", 0))
	run(fixture.Exec("DROP TABLE pe_scratch", 0))
	run(partitionAdoptionSame("pe_mid", ids))
	graph := [][]any{partitionAdoptionRow("pe_mid", "pe_mid_tenant_id_excl", nil, true, false, "I", false)}
	rows := [][]any{}
	conflict := func(physical string) fixture.Step {
		return fixture.ErrorDiagnostic("INSERT INTO pe_mid VALUES(1,1,'[3,7)')", fixture.ExclusionReferenceConflict(physical, physical+"_tenant_id_excl", "tenant, id", "1, 1", "1, 1"))
	}
	if covered {
		run(fixture.Exec("CREATE TABLE pe_cov PARTITION OF pe_mid "+bound, 0))
		run(fixture.Exec("INSERT INTO pe_mid VALUES(1,1,'[1,5)')", 1))
		graph = append([][]any{partitionAdoptionRow("pe_cov", "pe_cov_tenant_id_excl", "pe_mid_tenant_id_excl", false, false, "i", true)}, graph...)
		rows = [][]any{{int64(1), int64(1), "[1,5)"}}
	}
	checks := []fixture.Step{
		partitionAdoptionGraph(graph),
		fixture.Select("SELECT id,tenant,during FROM pe_mid ORDER BY id", []string{"id", "tenant", "during"}, []string{"INT4", "INT4", "INT4RANGE"}, rows),
		fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('pe_dummy','pe_dummy_gap','pe_dummy_ex','pe_scratch','pe_seed') ORDER BY relname", []string{"relname"}, []string{"NAME"}, nil),
	}
	if covered {
		checks = append(checks, conflict("pe_cov"))
	}
	check := partitionAdoptionRecipeChecker(t, p, prepared, checks...)
	check(b.DB, b.Recorder)
	partitionAdoptionRecipeReplay(t, p, b, check)
	physical := "pe_cov"
	if !covered {
		physical = "pe_future"
		run(fixture.Exec("CREATE TABLE pe_future PARTITION OF pe_mid "+bound, 0))
		run(fixture.Exec("INSERT INTO pe_mid VALUES(1,1,'[1,5)')", 1))
		run(partitionAdoptionGraph([][]any{
			partitionAdoptionRow("pe_future", "pe_future_tenant_id_excl", "pe_mid_tenant_id_excl", false, false, "i", true),
			partitionAdoptionRow("pe_mid", "pe_mid_tenant_id_excl", nil, true, false, "I", false),
		}))
	}
	run(conflict(physical))
	run(fixture.Exec("ALTER TABLE pe_mid DROP CONSTRAINT pe_mid_tenant_id_excl", 0))
	run(partitionAdoptionGraph(nil))
	run(fixture.Exec("INSERT INTO pe_mid VALUES(1,1,'[3,7)')", 1))
}

// The chosen final edge is an explicit actual provider, not whichever
// equivalent local happened to be created first by the replay recipe.
func partitionAdoptionCompetingFinalRecipe(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool, stale bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	for _, q := range []string{
		"CREATE TABLE pe_low(id INT,tenant INET,during INT4RANGE)",
		"ALTER TABLE pe_low ADD CONSTRAINT pe_old EXCLUDE USING gist(" + partitionExclusionKeys + ")",
		"CREATE TABLE pe_scratch(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)",
		"ALTER TABLE pe_scratch ADD CONSTRAINT pe_seed_old EXCLUDE USING gist(" + partitionExclusionKeys + ")",
		"ALTER TABLE pe_scratch ADD CONSTRAINT pe_seed_fresh EXCLUDE USING gist(" + partitionExclusionKeys + ")",
		"ALTER TABLE pe_scratch ATTACH PARTITION pe_low DEFAULT",
		"ALTER TABLE pe_scratch DETACH PARTITION pe_low",
		"DROP TABLE pe_scratch",
		"ALTER INDEX pe_low_tenant_during_excl RENAME TO pe_fresh",
	} {
		run(fixture.Exec(q, 0))
	}
	identity := partitionAdoptionNamedIdentity(t, p, b.DB, "pe_low", "pe_old")
	fresh := partitionAdoptionNamedIdentity(t, p, b.DB, "pe_low", "pe_fresh")
	run(partitionAdoptionGraph([][]any{
		partitionAdoptionRow("pe_low", "pe_fresh", nil, true, false, "i", true),
		partitionAdoptionRow("pe_low", "pe_old", nil, true, true, "i", true),
	}))
	run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
	run(fixture.Exec("ALTER TABLE pe_root ATTACH PARTITION pe_low DEFAULT", 0))
	if stale {
		// Ordinary MINVALUE/MAXVALUE needs no invented typed sentinel. Data is
		// replayed only after the temporary non-NULL gap has been removed.
		run(fixture.Exec("CREATE TABLE pe_promotion_gap PARTITION OF pe_root FOR VALUES FROM(MINVALUE) TO(MAXVALUE)", 0))
	}
	run(fixture.Exec("ALTER TABLE ONLY pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	run(fixture.Exec("ALTER INDEX pe_ex ATTACH PARTITION pe_old", 0))
	if stale {
		run(fixture.Exec("DROP TABLE pe_promotion_gap", 0))
	}
	run(partitionAdoptionSame("pe_low", identity))
	run(partitionAdoptionSame("pe_low", fresh))
	run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)')", 1))
	graph := [][]any{
		partitionAdoptionRow("pe_low", "pe_fresh", nil, true, false, "i", true),
		partitionAdoptionRow("pe_low", "pe_old", "pe_ex", false, true, "i", true),
		partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", !stale),
	}
	check := partitionAdoptionRecipeChecker(t, p, prepared,
		partitionAdoptionGraph(graph),
		fixture.Select("SELECT id,tenant,during FROM pe_root ORDER BY id", []string{"id", "tenant", "during"}, []string{"INT4", "INET", "INT4RANGE"}, [][]any{{int64(1), "10.0.0.1", "[1,5)"}}),
		partitionExclusionConflict("INSERT INTO pe_root VALUES(2,'10.0.0.1','[3,7)')", "pe_low", "pe_old", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
	check(b.DB, b.Recorder)
	partitionAdoptionRecipeReplay(t, p, b, check)
	run(fixture.Exec("ALTER TABLE pe_root DROP CONSTRAINT pe_ex", 0))
	run(partitionAdoptionGraph([][]any{partitionAdoptionRow("pe_low", "pe_fresh", nil, true, false, "i", true)}))
	run(partitionExclusionConflict("INSERT INTO pe_root VALUES(2,'10.0.0.1','[3,7)')", "pe_low", "pe_fresh", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
	run(fixture.Exec("ALTER TABLE pe_low DROP CONSTRAINT pe_fresh", 0))
	run(partitionAdoptionGraph(nil))
	run(fixture.Exec("INSERT INTO pe_root VALUES(2,'10.0.0.1','[3,7)')", 1))
}

// Equivalent seeds remain under one temporary-parent ownership lifetime while
// their real prerequisite frontiers interleave. The first root needs children
// after DROP; the second needs the same retained invalid child before ADD.
func partitionAdoptionInterleavedFreshRecipe(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	for _, q := range []string{
		"CREATE TABLE pe_mid(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)",
		"CREATE TABLE pe_discard PARTITION OF pe_mid DEFAULT PARTITION BY RANGE(tenant)",
		"CREATE TABLE pe_discard_gap PARTITION OF pe_discard DEFAULT",
		"ALTER TABLE ONLY pe_discard ADD CONSTRAINT pe_dead EXCLUDE USING gist(" + partitionExclusionKeys + ")",
		"CREATE TABLE pe_scratch(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)",
		"ALTER TABLE pe_scratch ATTACH PARTITION pe_mid DEFAULT",
		"ALTER TABLE pe_scratch ADD CONSTRAINT pe_seed_stale EXCLUDE USING gist(" + partitionExclusionKeys + ")",
		"DROP TABLE pe_discard",
		"ALTER INDEX pe_mid_tenant_during_excl RENAME TO pe_stale",
		"CREATE TABLE pe_low PARTITION OF pe_mid DEFAULT PARTITION BY RANGE(tenant)",
		"CREATE TABLE pe_gap PARTITION OF pe_low FOR VALUES FROM('10.0.0.0') TO('10.0.0.128')",
		"ALTER TABLE ONLY pe_low ADD CONSTRAINT pe_local EXCLUDE USING gist(" + partitionExclusionKeys + ")",
		"CREATE TABLE pe_cov PARTITION OF pe_low DEFAULT",
	} {
		run(fixture.Exec(q, 0))
	}
	local := partitionAdoptionNamedIdentity(t, p, b.DB, "pe_low", "pe_local")
	inherited := partitionAdoptionNamedIdentity(t, p, b.DB, "pe_low", "pe_low_tenant_during_excl")
	covered := partitionAdoptionNamedIdentity(t, p, b.DB, "pe_cov", "pe_cov_tenant_during_excl1")
	coveredInherited := partitionAdoptionNamedIdentity(t, p, b.DB, "pe_cov", "pe_cov_tenant_during_excl")
	run(fixture.Exec("ALTER TABLE pe_scratch ADD CONSTRAINT pe_seed_retained EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	run(partitionAdoptionSame("pe_low", local))
	run(partitionAdoptionSame("pe_cov", covered))
	run(partitionAdoptionSame("pe_low", inherited))
	run(partitionAdoptionSame("pe_cov", coveredInherited))
	run(fixture.Exec("ALTER TABLE pe_scratch DETACH PARTITION pe_mid", 0))
	run(fixture.Exec("DROP TABLE pe_scratch", 0))
	run(fixture.Exec("INSERT INTO pe_mid VALUES(1,'10.0.0.200','[1,5)'),(2,'10.0.0.1','[1,5)')", 2))
	graph := [][]any{
		partitionAdoptionRow("pe_cov", "pe_cov_tenant_during_excl", "pe_low_tenant_during_excl", false, false, "i", true),
		partitionAdoptionRow("pe_cov", "pe_cov_tenant_during_excl1", "pe_local", false, false, "i", true),
		partitionAdoptionRow("pe_gap", "pe_gap_tenant_during_excl", "pe_low_tenant_during_excl", false, false, "i", true),
		partitionAdoptionRow("pe_low", "pe_local", "pe_mid_tenant_during_excl", false, true, "I", false),
		partitionAdoptionRow("pe_low", "pe_low_tenant_during_excl", "pe_stale", false, false, "I", true),
		partitionAdoptionRow("pe_mid", "pe_mid_tenant_during_excl", nil, true, false, "I", false),
		partitionAdoptionRow("pe_mid", "pe_stale", nil, true, false, "I", false),
	}
	check := partitionAdoptionRecipeChecker(t, p, prepared,
		partitionAdoptionGraph(graph),
		fixture.Select("SELECT id,tenant,during FROM pe_mid ORDER BY id", []string{"id", "tenant", "during"}, []string{"INT4", "INET", "INT4RANGE"}, [][]any{{int64(1), "10.0.0.200", "[1,5)"}, {int64(2), "10.0.0.1", "[1,5)"}}),
		partitionExclusionConflict("INSERT INTO pe_mid VALUES(3,'10.0.0.200','[3,7)')", "pe_cov", "pe_cov_tenant_during_excl", "10.0.0.200, [3,7)", "10.0.0.200, [1,5)"),
		partitionExclusionConflict("INSERT INTO pe_mid VALUES(4,'10.0.0.1','[3,7)')", "pe_gap", "pe_gap_tenant_during_excl", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
	check(b.DB, b.Recorder)
	partitionAdoptionRecipeReplay(t, p, b, check)
	run(fixture.Exec("ALTER TABLE pe_mid DROP CONSTRAINT pe_stale", 0))
	run(partitionAdoptionGraph([][]any{
		partitionAdoptionRow("pe_cov", "pe_cov_tenant_during_excl1", "pe_local", false, false, "i", true),
		partitionAdoptionRow("pe_low", "pe_local", "pe_mid_tenant_during_excl", false, true, "I", false),
		partitionAdoptionRow("pe_mid", "pe_mid_tenant_during_excl", nil, true, false, "I", false),
	}))
	run(fixture.Exec("INSERT INTO pe_mid VALUES(4,'10.0.0.1','[3,7)')", 1))
	run(partitionExclusionConflict("INSERT INTO pe_mid VALUES(3,'10.0.0.200','[3,7)')", "pe_cov", "pe_cov_tenant_during_excl1", "10.0.0.200, [3,7)", "10.0.0.200, [1,5)"))
	run(fixture.Exec("ALTER TABLE pe_mid DROP CONSTRAINT pe_mid_tenant_during_excl", 0))
	run(partitionAdoptionGraph(nil))
	run(fixture.Exec("INSERT INTO pe_mid VALUES(3,'10.0.0.200','[3,7)')", 1))
}

// Establish empty invalidity before the admitted RANGE final membership.
// LIST/HASH final TABLE ATTACH is an existing unimplemented adjacent surface,
// not enabled by adoption. No trailing INDEX ATTACH may silently promote it.
func partitionAdoptionEmptyInvalidCompetingRecipe(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	const definition = "EXCLUDE USING btree(tenant WITH =,id WITH =)"
	for _, q := range []string{
		"CREATE TABLE pe_low(id INT,tenant INT)",
		"ALTER TABLE pe_low ADD CONSTRAINT pe_old " + definition,
		"CREATE TABLE pe_scratch(id INT,tenant INT) PARTITION BY RANGE(tenant)",
		"ALTER TABLE pe_scratch ADD CONSTRAINT pe_guard " + definition,
		"ALTER TABLE pe_scratch ADD CONSTRAINT pe_seed " + definition,
		"ALTER TABLE pe_scratch ATTACH PARTITION pe_low DEFAULT",
		"ALTER TABLE pe_scratch DETACH PARTITION pe_low",
		"DROP TABLE pe_scratch",
		"ALTER INDEX pe_low_tenant_id_excl RENAME TO pe_fresh",
	} {
		run(fixture.Exec(q, 0))
	}
	old := partitionAdoptionNamedIdentity(t, p, b.DB, "pe_low", "pe_old")
	fresh := partitionAdoptionNamedIdentity(t, p, b.DB, "pe_low", "pe_fresh")
	run(partitionAdoptionGraph([][]any{
		partitionAdoptionRow("pe_low", "pe_fresh", nil, true, false, "i", true),
		partitionAdoptionRow("pe_low", "pe_old", nil, true, true, "i", true),
	}))
	const bound = "DEFAULT"
	for _, q := range []string{
		"CREATE TABLE pe_root(id INT,tenant INT) PARTITION BY RANGE(tenant)",
		"CREATE TABLE pe_invalid_gap PARTITION OF pe_root " + bound,
		"ALTER TABLE ONLY pe_root ADD CONSTRAINT pe_ex " + definition,
		"DROP TABLE pe_invalid_gap",
	} {
		run(fixture.Exec(q, 0))
	}
	root := partitionAdoptionNamedIdentity(t, p, b.DB, "pe_root", "pe_ex")
	run(partitionAdoptionGraph([][]any{
		partitionAdoptionRow("pe_low", "pe_fresh", nil, true, false, "i", true),
		partitionAdoptionRow("pe_low", "pe_old", nil, true, true, "i", true),
		partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", false),
	}))
	run(fixture.Exec("ALTER TABLE pe_root ATTACH PARTITION pe_low "+bound, 0))
	run(partitionAdoptionSame("pe_low", old))
	run(partitionAdoptionSame("pe_low", fresh))
	run(partitionAdoptionSame("pe_root", root))
	run(fixture.Exec("INSERT INTO pe_root VALUES(1,1)", 1))
	conflict := func(name string) fixture.Step {
		return fixture.ErrorDiagnostic("INSERT INTO pe_root VALUES(1,1)", fixture.ExclusionReferenceConflict("pe_low", name, "tenant, id", "1, 1", "1, 1"))
	}
	check := partitionAdoptionRecipeChecker(t, p, prepared,
		partitionAdoptionGraph([][]any{
			partitionAdoptionRow("pe_low", "pe_fresh", nil, true, false, "i", true),
			partitionAdoptionRow("pe_low", "pe_old", "pe_ex", false, true, "i", true),
			partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", false),
		}),
		fixture.Select("SELECT id,tenant FROM pe_root ORDER BY id", []string{"id", "tenant"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}),
		fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('pe_scratch','pe_guard','pe_seed','pe_invalid_gap') ORDER BY relname", []string{"relname"}, []string{"NAME"}, nil),
		conflict("pe_old"))
	check(b.DB, b.Recorder)
	partitionAdoptionRecipeReplay(t, p, b, check)
	// Replacement remints identities. Capture the actual current survivor before
	// testing the subsequent DROP, rather than comparing it to a retired epoch.
	fresh = partitionAdoptionNamedIdentity(t, p, b.DB, "pe_low", "pe_fresh")
	run(fixture.Exec("ALTER TABLE pe_root DROP CONSTRAINT pe_ex", 0))
	run(partitionAdoptionSame("pe_low", fresh))
	run(partitionAdoptionGraph([][]any{partitionAdoptionRow("pe_low", "pe_fresh", nil, true, false, "i", true)}))
	run(conflict("pe_fresh"))
	run(fixture.Exec("ALTER TABLE pe_low DROP CONSTRAINT pe_fresh", 0))
	run(partitionAdoptionGraph(nil))
	run(fixture.Exec("INSERT INTO pe_root VALUES(1,1)", 1))
}
