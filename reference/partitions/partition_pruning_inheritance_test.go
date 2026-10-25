package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func referencePruningInheritanceSetup(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE TABLE pp_inh(k INT)",
		"CREATE TABLE pp_inh_pos(CHECK(k>0)) INHERITS(pp_inh)",
		"CREATE TABLE pp_inh_neg(CHECK(k<0)) INHERITS(pp_inh)",
		"CREATE TABLE pp_inh_nn(k INT NOT NULL,CHECK(k>10)) INHERITS(pp_inh)",
		"CREATE TABLE pp_inh_old() INHERITS(pp_inh)",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO pp_inh VALUES(0),(0)", 2))
	run(fixture.Exec("INSERT INTO pp_inh_pos VALUES(1),(NULL)", 2))
	run(fixture.Exec("INSERT INTO pp_inh_neg VALUES(-1),(NULL)", 2))
	run(fixture.Exec("INSERT INTO pp_inh_nn VALUES(11)", 1))
	run(fixture.Exec("INSERT INTO pp_inh_old VALUES(-7)", 1))
	run(fixture.Exec("ALTER TABLE pp_inh_old ADD CONSTRAINT pp_inh_unvalidated CHECK(k>100) NOT VALID", 0))
	for _, q := range []string{
		"CREATE TABLE pp_noinh(k INT,CONSTRAINT pp_local CHECK(k>0) NO INHERIT)",
		"CREATE TABLE pp_noinh_child(CONSTRAINT pp_child_local CHECK(k<0) NO INHERIT) INHERITS(pp_noinh)",
		"CREATE TABLE pp_noinh_grand() INHERITS(pp_noinh_child)",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO pp_noinh VALUES(1)", 1))
	run(fixture.Exec("INSERT INTO pp_noinh_child VALUES(-1)", 1))
	run(fixture.Exec("INSERT INTO pp_noinh_grand VALUES(2)", 1))
}

func referencePruningInheritanceChecks(run func(fixture.Step), pg bool) {
	referencePruningKeys(run, pg, "SELECT k FROM pp_inh WHERE k IS NULL", nil, nil)
	referencePruningKeys(run, pg, "SELECT k FROM pp_inh WHERE k<0", int64(-1), int64(-7))
	referencePruningKeys(run, pg, "SELECT k FROM pp_inh WHERE k=0", int64(0), int64(0))
	referencePruningKeys(run, pg, "SELECT k FROM pp_inh WHERE k>0", int64(1), int64(11))
	referencePruningKeys(run, pg, "SELECT k FROM ONLY pp_inh WHERE k<0")
	referencePruningKeys(run, pg, "SELECT k FROM pp_inh_old WHERE k=-7", int64(-7))
	referencePruningKeys(run, pg, "SELECT k FROM pp_noinh WHERE k<0", int64(-1))
	referencePruningKeys(run, pg, "SELECT k FROM pp_noinh_child WHERE k>0", int64(2))
	referencePruningKeys(run, pg, "SELECT k FROM ONLY pp_noinh_child WHERE k>0")
}

func TestReferencePartitionPruningInheritance(t *testing.T) {
	referencePruningScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		referencePruningInheritanceSetup(run)
		referencePruningInheritanceChecks(run, b.PG)
		// Reuse the established diamond/layout fixture instead of a second
		// implementation of inherited column mapping or multiplicity setup.
		fixture.InheritanceQueryDiamond(run)
		step := fixture.Select("SELECT a,x FROM p WHERE a=7", []string{"a", "x"}, []string{"INT4", "TEXT"}, fixture.InheritanceQueryFive())
		step.Bag = true
		run(step)
		run(fixture.Select("SELECT count(*) AS n FROM p a JOIN p b ON a.a=b.a WHERE a.a=7", []string{"n"}, []string{"INT8"}, [][]any{{int64(25)}}))
		run(fixture.Select("SELECT count(*) AS n FROM ONLY p WHERE a=7", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(fixture.Select("SELECT a FROM c WHERE a=7 ORDER BY z", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}))
	})
}
