package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePartitionAttachFeatureSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE pa_smoke_parent(k INT,payload TEXT,CONSTRAINT pa_smoke_key UNIQUE(k)) PARTITION BY RANGE(k)", 0))
					run(fixture.Exec("CREATE TABLE pa_smoke_candidate(payload TEXT,k INT,CONSTRAINT pa_smoke_existing UNIQUE(k),CONSTRAINT pa_smoke_local CHECK(length(payload)>0))", 0))
					run(fixture.Exec("INSERT INTO pa_smoke_candidate VALUES('one',1),('two',2)", 2))
					run(fixture.Exec("CREATE TABLE pa_smoke_sibling PARTITION OF pa_smoke_parent FOR VALUES FROM(3) TO(5)", 0))
					run(fixture.Exec("INSERT INTO pa_smoke_sibling VALUES(3,'three')", 1))
					insertQuery := "INSERT INTO pa_smoke_parent VALUES($1,$2)"
					held, err := b.DB.PrepareContext(p.Ctx, insertQuery)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					q := "ALTER TABLE pa_smoke_parent ATTACH PARTITION pa_smoke_candidate FOR VALUES FROM(1) TO(3)"
					if prepared {
						utility, err := b.DB.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer utility.Close()
						// Genuine effect-free Prepare, not fresh prepare-and-execute.
						run(fixture.Select("SELECT k,payload FROM pa_smoke_parent", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "three"}}))
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: utility, SQL: q}, b.Recorder, fixture.Exec(q, 0))
					} else {
						run(fixture.Exec(q, 0))
					}
					s := fixture.Select("SELECT k,payload FROM pa_smoke_parent ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}})
					s.CheckMetadata = referenceRangeMetadata(b.PG, s.Columns, s.Types, []bool{true, true})
					run(s)
					s = fixture.Select("SELECT * FROM pa_smoke_candidate ORDER BY k", []string{"payload", "k"}, []string{"TEXT", "INT4"}, [][]any{{"one", int64(1)}, {"two", int64(2)}})
					s.CheckMetadata = referenceRangeMetadata(b.PG, s.Columns, s.Types, []bool{true, true})
					run(s)
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: insertQuery}, b.Recorder, fixture.Exec(insertQuery, 1, 4, "four"))
					run(referenceRangeLeafMiss("INSERT INTO pa_smoke_candidate VALUES('five',5)", "pa_smoke_candidate", "five, 5"))
					run(fixture.Exec("CREATE TABLE pa_smoke_bad(k INT,payload TEXT)", 0))
					run(fixture.Exec("INSERT INTO pa_smoke_bad VALUES(9,'nine')", 1))
					run(fixture.ImplicitError("ALTER TABLE pa_smoke_parent ATTACH PARTITION pa_smoke_bad FOR VALUES FROM(5) TO(7)", fixture.Diagnostic{Code: "23514", Message: `partition constraint of relation "pa_smoke_bad" is violated by some row`, Schema: "public", Table: "pa_smoke_bad"}))
					run(fixture.Exec("INSERT INTO pa_smoke_bad VALUES(8,'eight')", 1))
					run(fixture.Select("SELECT k,payload FROM pa_smoke_bad ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(8), "eight"}, {int64(9), "nine"}}))
					run(fixture.Exec("CREATE TABLE pa_smoke_default(k INT,payload TEXT)", 0))
					run(fixture.Exec("INSERT INTO pa_smoke_default VALUES(NULL,'null'),(10,'ten')", 2))
					run(fixture.Exec("ALTER TABLE pa_smoke_parent ATTACH PARTITION pa_smoke_default DEFAULT", 0))
					run(fixture.Exec("CREATE TABLE pa_smoke_conflict(k INT,payload TEXT)", 0))
					run(fixture.ImplicitError("ALTER TABLE pa_smoke_parent ATTACH PARTITION pa_smoke_conflict FOR VALUES FROM(10) TO(11)", fixture.Diagnostic{Code: "23514", Message: `updated partition constraint for default partition "pa_smoke_default" would be violated by some row`, Schema: "public", Table: "pa_smoke_default"}))
					partitionAttachSmokeState(t, p, b.DB, b.Recorder, b.PG)
					if b.PG {
						return
					}
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					partitionAttachSmokeState(t, p, b.DB, b.Recorder, false)
					run(partitionConstraintDuplicate("INSERT INTO pa_smoke_parent VALUES(1,'duplicate')", "pa_smoke_candidate", "pa_smoke_existing", "Key (k)=(1) already exists."))
					run(referenceRangeLeafMiss("INSERT INTO pa_smoke_candidate VALUES('five',5)", "pa_smoke_candidate", "five, 5"))
					if p.Postgres != nil {
						partitionAttachReplay(t, p, dump.String(), partitionAttachSmokeState)
					}
				})
			}
		})
	}
}

func partitionAttachSmokeState(t *testing.T, p *fixture.Pair, x fixture.Executor, record *fixture.Recorder, pg bool) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, record, s) }
	s := fixture.Select("SELECT k,payload FROM pa_smoke_parent ORDER BY k NULLS LAST", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}, {int64(4), "four"}, {int64(10), "ten"}, {nil, "null"}})
	s.CheckMetadata = referenceRangeMetadata(pg, s.Columns, s.Types, []bool{true, true})
	run(s)
	run(fixture.Select("SELECT * FROM pa_smoke_candidate ORDER BY k", []string{"payload", "k"}, []string{"TEXT", "INT4"}, [][]any{{"one", int64(1)}, {"two", int64(2)}}))
	run(fixture.Select("SELECT relname,relispartition FROM pg_class WHERE relname='pa_smoke_bad' OR relname='pa_smoke_conflict' ORDER BY relname", []string{"relname", "relispartition"}, []string{"NAME", "BOOL"}, [][]any{{"pa_smoke_bad", false}, {"pa_smoke_conflict", false}}))
	run(partitionConstraintEdge("pa_smoke_existing", "pa_smoke_key", "pa_smoke_candidate"))
	run(partitionConstraintEdge("pa_smoke_sibling_k_key", "pa_smoke_key", "pa_smoke_sibling"))
	run(partitionConstraintEdge("pa_smoke_default_k_key", "pa_smoke_key", "pa_smoke_default"))
	run(fixture.Select("SELECT k,payload FROM pa_smoke_bad ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(8), "eight"}, {int64(9), "nine"}}))
}
