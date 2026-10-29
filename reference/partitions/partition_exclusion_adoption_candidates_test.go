package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionExclusionAdoptionCandidates(t *testing.T) {
	for _, operation := range []string{"attach", "recursive_add"} {
		partitionExclusionCase(t, "invalid_subtree/"+operation, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := partitionConstraintRun(t, p, b, prepared)
			run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
			if operation == "attach" {
				run(fixture.Exec("ALTER TABLE pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
				run(fixture.Exec("CREATE TABLE pe_mid(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
			} else {
				run(fixture.Exec("CREATE TABLE pe_mid PARTITION OF pe_root DEFAULT PARTITION BY RANGE(tenant)", 0))
			}
			run(fixture.Exec("CREATE TABLE pe_gap PARTITION OF pe_mid FOR VALUES FROM('10.0.0.0') TO('10.0.0.128')", 0))
			run(fixture.Exec("ALTER TABLE ONLY pe_mid ADD CONSTRAINT pe_local EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
			run(fixture.Exec("CREATE TABLE pe_fresh PARTITION OF pe_mid DEFAULT", 0))
			run(fixture.Exec("INSERT INTO pe_mid VALUES(1,'10.0.0.1','[1,5)'),(2,'10.0.0.129','[1,5)')", 2))
			ids := partitionAdoptionIdentity(t, p, b.DB, "pe_mid")
			leaf := partitionAdoptionIdentity(t, p, b.DB, "pe_fresh")
			if operation == "attach" {
				run(fixture.Exec("ALTER TABLE pe_root ATTACH PARTITION pe_mid DEFAULT", 0))
				run(partitionAdoptionGraph([][]any{
					partitionAdoptionRow("pe_fresh", "pe_fresh_tenant_during_excl", "pe_local", false, false, "i", true),
					partitionAdoptionRow("pe_fresh", "pe_fresh_tenant_during_excl1", "pe_mid_tenant_during_excl", false, false, "i", true),
					partitionAdoptionRow("pe_gap", "pe_gap_tenant_during_excl", "pe_mid_tenant_during_excl", false, false, "i", true),
					partitionAdoptionRow("pe_mid", "pe_local", nil, true, true, "I", false),
					partitionAdoptionRow("pe_mid", "pe_mid_tenant_during_excl", "pe_ex", false, false, "I", true),
					partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", true),
				}))
				run(fixture.Select("SELECT oid=$1::OID AS same_constraint,conindid=$2::OID AS same_index FROM pg_constraint WHERE conname='pe_local'", []string{"same_constraint", "same_index"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, ids[0], ids[1]))
				run(partitionExclusionConflict("INSERT INTO pe_root VALUES(3,'10.0.0.1','[3,7)')", "pe_gap", "pe_gap_tenant_during_excl", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
				run(fixture.Exec("ALTER TABLE pe_root DROP CONSTRAINT pe_ex", 0))
			} else {
				run(fixture.Exec("ALTER TABLE pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
				run(partitionAdoptionSame("pe_mid", ids))
				run(partitionAdoptionSame("pe_fresh", leaf))
				run(partitionAdoptionGraph([][]any{
					partitionAdoptionRow("pe_fresh", "pe_fresh_tenant_during_excl", "pe_local", false, false, "i", true),
					partitionAdoptionRow("pe_mid", "pe_local", "pe_ex", false, true, "I", false),
					partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", false),
				}))
			}
			// Recursive adoption does not heal the gap; ATTACH's fresh tree was
			// removed above, leaving the original incomplete local scope intact.
			run(fixture.Exec("INSERT INTO pe_root VALUES(3,'10.0.0.1','[3,7)')", 1))
			run(partitionExclusionConflict("INSERT INTO pe_root VALUES(4,'10.0.0.129','[3,7)')", "pe_fresh", "pe_fresh_tenant_during_excl", "10.0.0.129, [3,7)", "10.0.0.129, [1,5)"))
			run(partitionExclusionIDs(1, 2, 3))
		})
	}
	for _, operation := range []string{"attach", "recursive_add", "explicit"} {
		for _, childDeferred := range []bool{false, true} {
			name := "parent_deferred"
			if childDeferred {
				name = "child_deferred"
			}
			partitionExclusionCase(t, "timing/"+operation+"/"+name, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				c := fixture.ConcurrentIndexConn(t, p, b)
				run := func(s fixture.Step) { s.Prepared = prepared; fixture.ExclusionReferenceRun(t, p, b, c, s) }
				parentFlags, childFlags := " DEFERRABLE INITIALLY DEFERRED", ""
				if childDeferred {
					parentFlags, childFlags = "", " DEFERRABLE INITIALLY DEFERRED"
				}
				run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
				if operation == "attach" {
					run(fixture.Exec("ALTER TABLE pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")"+parentFlags, 0))
					run(fixture.Exec("CREATE TABLE pe_child(id INT,tenant INET,during INT4RANGE)", 0))
				} else {
					run(fixture.Exec("CREATE TABLE pe_child PARTITION OF pe_root DEFAULT", 0))
				}
				run(fixture.Exec("ALTER TABLE pe_child ADD CONSTRAINT pe_local EXCLUDE USING gist("+partitionExclusionKeys+")"+childFlags, 0))
				ids := partitionAdoptionIdentity(t, p, c, "pe_child")
				switch operation {
				case "attach":
					run(fixture.Exec("ALTER TABLE pe_root ATTACH PARTITION pe_child DEFAULT", 0))
				case "recursive_add":
					run(fixture.Exec("ALTER TABLE pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")"+parentFlags, 0))
				case "explicit":
					run(fixture.Exec("ALTER TABLE ONLY pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")"+parentFlags, 0))
					run(fixture.Exec("ALTER INDEX pe_ex ATTACH PARTITION pe_local", 0))
				}
				run(partitionAdoptionSame("pe_child", ids))
				run(fixture.Select("SELECT conname,condeferrable,condeferred,connoinherit FROM pg_constraint WHERE conname IN ('pe_ex','pe_local') ORDER BY conname", []string{"conname", "condeferrable", "condeferred", "connoinherit"}, []string{"NAME", "BOOL", "BOOL", "BOOL"}, [][]any{{"pe_ex", !childDeferred, !childDeferred, true}, {"pe_local", childDeferred, childDeferred, true}}))
				run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)')", 1))
				for _, target := range []string{"pe_ex", "ALL"} {
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("SAVEPOINT before_conflict", 0))
					q := "INSERT INTO pe_root VALUES(2,'10.0.0.1','[3,7)')"
					if childDeferred {
						run(fixture.Exec(q, 1))
						if target == "pe_ex" {
							// IMMEDIATE on a nondeferrable named parent is a no-op:
							// it does not change this adopted child's deferred state.
							run(fixture.Exec("SET CONSTRAINTS pe_ex IMMEDIATE", 0))
							run(partitionExclusionIDs(1, 2))
							target = "pe_local"
							run(referenceRangeError("SET CONSTRAINTS pe_ex DEFERRED", "42809", `constraint "pe_ex" is not deferrable`, fixture.Diagnostic{}))
							run(fixture.Exec("ROLLBACK TO before_conflict", 0))
							run(fixture.Exec(q, 1))
						}
						run(partitionExclusionConflict("SET CONSTRAINTS "+target+" IMMEDIATE", "pe_child", "pe_local", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
					} else {
						run(partitionExclusionConflict(q, "pe_child", "pe_local", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
					}
					run(fixture.Exec("ROLLBACK TO before_conflict", 0))
					run(fixture.Exec("COMMIT", 0))
				}
				if childDeferred {
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO pe_root VALUES(2,'10.0.0.1','[3,7)')", 1))
					run(partitionExclusionConflict("COMMIT", "pe_child", "pe_local", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
				}
				run(partitionExclusionIDs(1))
			})
		}
	}
	partitionExclusionCase(t, "explicit_mismatch_and_precedence", partitionAdoptionExplicitErrors)
	partitionExclusionCase(t, "candidate_order_and_already_owned", partitionAdoptionCandidateOrder)
	partitionExclusionCase(t, "explicit_incomplete_idempotence", partitionAdoptionExplicitIncomplete)
}

func partitionAdoptionExplicitErrors(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
	run(fixture.Exec("CREATE TABLE pe_child PARTITION OF pe_root DEFAULT", 0))
	run(fixture.Exec("ALTER TABLE ONLY pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	run(fixture.Exec("ALTER TABLE ONLY pe_root ADD CONSTRAINT pe_second EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	run(fixture.Exec("ALTER TABLE pe_child ADD CONSTRAINT pe_wrong EXCLUDE USING gist(during WITH &&)", 0))
	run(referenceRangeError("ALTER INDEX pe_ex ATTACH PARTITION pe_wrong", "42P17", `cannot attach index "pe_wrong" as a partition of index "pe_ex"`, fixture.Diagnostic{Detail: "The index definitions do not match."}))
	run(fixture.Exec("CREATE TABLE pe_other(id INT,tenant INET,during INT4RANGE,CONSTRAINT pe_unrelated EXCLUDE USING gist(during WITH &&))", 0))
	run(referenceRangeError("ALTER INDEX pe_ex ATTACH PARTITION pe_unrelated", "55000", `cannot attach index "pe_unrelated" as a partition of index "pe_ex"`, fixture.Diagnostic{Detail: `Index "pe_unrelated" is not an index on any partition of table "pe_root".`}))
	run(fixture.Exec("ALTER TABLE pe_child ADD CONSTRAINT pe_local EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	run(fixture.Exec("ALTER INDEX pe_ex ATTACH PARTITION pe_local", 0))
	run(referenceRangeError("ALTER INDEX pe_ex ATTACH PARTITION pe_wrong", "55000", `cannot attach index "pe_wrong" as a partition of index "pe_ex"`, fixture.Diagnostic{Detail: `Another index is already attached for partition "pe_child".`}))
	run(referenceRangeError("ALTER INDEX pe_second ATTACH PARTITION pe_local", "55000", `cannot attach index "pe_local" as a partition of index "pe_second"`, fixture.Diagnostic{Detail: `Index "pe_local" is already attached to another index.`}))
	run(referenceRangeError("DROP INDEX pe_local", "2BP01", "cannot drop index pe_local because index pe_ex requires it", fixture.Diagnostic{Hint: "You can drop index pe_ex instead."}))
	run(partitionAdoptionGraph([][]any{
		partitionAdoptionRow("pe_child", "pe_local", "pe_ex", false, true, "i", true),
		partitionAdoptionRow("pe_child", "pe_wrong", nil, true, true, "i", true),
		partitionAdoptionRow("pe_other", "pe_unrelated", nil, true, true, "i", true),
		partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", true),
		partitionAdoptionRow("pe_root", "pe_second", nil, true, true, "I", false),
	}))
}

func partitionAdoptionCandidateOrder(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
	run(fixture.Exec("CREATE TABLE pe_child PARTITION OF pe_root DEFAULT", 0))
	run(fixture.Exec("ALTER TABLE pe_child ADD CONSTRAINT pe_z EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	run(fixture.Exec("ALTER TABLE pe_child ADD CONSTRAINT pe_a EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	run(fixture.Exec("ALTER TABLE pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	run(fixture.Exec("ALTER TABLE pe_root ADD CONSTRAINT pe_second EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	run(partitionAdoptionGraph([][]any{
		partitionAdoptionRow("pe_child", "pe_a", "pe_second", false, true, "i", true),
		partitionAdoptionRow("pe_child", "pe_z", "pe_ex", false, true, "i", true),
		partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", true),
		partitionAdoptionRow("pe_root", "pe_second", nil, true, true, "I", true),
	}))
	run(fixture.Exec("ALTER TABLE pe_root DROP CONSTRAINT pe_ex", 0))
	run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)')", 1))
	run(partitionExclusionConflict("INSERT INTO pe_root VALUES(2,'10.0.0.1','[3,7)')", "pe_child", "pe_a", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
}

func partitionAdoptionExplicitIncomplete(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
	run(fixture.Exec("CREATE TABLE pe_child PARTITION OF pe_root FOR VALUES FROM('10.0.0.0') TO('10.0.0.128')", 0))
	run(fixture.Exec("CREATE TABLE pe_gap PARTITION OF pe_root DEFAULT", 0))
	run(fixture.Exec("ALTER TABLE ONLY pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	run(fixture.Exec("ALTER TABLE pe_child ADD CONSTRAINT pe_local EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	ids := partitionAdoptionIdentity(t, p, b.DB, "pe_child")
	for range 2 {
		run(fixture.Exec("ALTER INDEX pe_ex ATTACH PARTITION pe_local", 0))
		run(partitionAdoptionSame("pe_child", ids))
		run(partitionAdoptionGraph([][]any{
			partitionAdoptionRow("pe_child", "pe_local", "pe_ex", false, true, "i", true),
			partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", false),
		}))
	}
	run(fixture.Exec("ALTER TABLE pe_gap ADD CONSTRAINT pe_gap_local EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	run(fixture.Exec("ALTER INDEX pe_ex ATTACH PARTITION pe_gap_local", 0))
	run(partitionAdoptionGraph([][]any{
		partitionAdoptionRow("pe_child", "pe_local", "pe_ex", false, true, "i", true),
		partitionAdoptionRow("pe_gap", "pe_gap_local", "pe_ex", false, true, "i", true),
		partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", true),
	}))
}
