package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferencePartialIndexUniqueTypeFinalPredicate(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
				run(referenceExec("CREATE TABLE u(id INT,k INT,n INT)", 0))
				run(referenceExec("INSERT INTO u VALUES(1,7,-1),(2,7,-2)", 2))
				run(referenceExec("CREATE UNIQUE INDEX pu ON u(k) WHERE n>0", 0))
				before := ""
				if !b.pg {
					before = partialIndexDump(t, p)
				}
				failure := referenceErrorDiagnostic("ALTER TABLE u ALTER n TYPE BIGINT USING 1", uniqueIndexReferenceBuildDiagnostic("u", "pu", "Key (k)=(7) is duplicated."))
				failure.errorStage = "execute"
				if held {
					failure = referencePrepared(failure)
				}
				run(failure)
				if !b.pg && before != partialIndexDump(t, p) {
					t.Fatal("failed TYPE changed Dump")
				}
				old := referenceSelect("SELECT id,k,n FROM u ORDER BY id", []string{"id", "k", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(-1)}, {int64(2), int64(7), int64(-2)}})
				run(old)
				run(deparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('pu'::regclass) AS definition", "CREATE UNIQUE INDEX pu ON public.u USING btree (k) WHERE (n > 0)"))
				tx, err := b.db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				trun := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.recorder, s) }
				trun(referenceExec("SAVEPOINT original", 0))
				trun(referenceExec("INSERT INTO u VALUES(3,7,-3)", 1))
				trun(referenceExec("SAVEPOINT attempt", 0))
				trun(failure)
				trun(referenceExec("ROLLBACK TO attempt", 0))
				trun(referenceExec("DELETE FROM u WHERE id=2 OR id=3", 2))
				trun(referencePrepared(referenceExec("ALTER TABLE u ALTER n TYPE BIGINT USING 1", 0)))
				trun(referenceSelect("SELECT id,k,n FROM u", []string{"id", "k", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(7), int64(1)}}))
				trun(referenceExec("SAVEPOINT baseline", 0))
				trun(referencePrepared(referenceErrorDiagnostic("INSERT INTO u VALUES(4,7,1)", uniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists."))))
				trun(referenceExec("ROLLBACK TO baseline", 0))
				trun(referenceExec("INSERT INTO u VALUES(4,7,-1)", 1))
				trun(referenceExec("ROLLBACK TO baseline", 0))
				trun(referenceExec("ROLLBACK TO original", 0))
				trun(old)
				trun(referenceExec("DELETE FROM u WHERE id=2", 1))
				trun(referenceExec("ALTER TABLE u ALTER n TYPE BIGINT USING 1", 0))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				run(referenceSelect("SELECT id,k,n FROM u", []string{"id", "k", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(7), int64(1)}}))
				run(referenceErrorDiagnostic("INSERT INTO u VALUES(5,7,1)", uniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists.")))
			})
		})
	}
}

func TestReferencePartialIndexUniqueTypeCombined(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE u(id INT,k INT,n INT)", 0))
		run(referenceExec("INSERT INTO u VALUES(1,1,-1),(2,2,-2)", 2))
		run(referenceExec("CREATE UNIQUE INDEX pu ON u(k) WHERE n>0", 0))
		run(referencePrepared(referenceErrorDiagnostic("ALTER TABLE u ALTER k TYPE BIGINT USING 7, ALTER n TYPE BIGINT USING 1", uniqueIndexReferenceBuildDiagnostic("u", "pu", "Key (k)=(7) is duplicated."))))
		run(referenceSelect("SELECT id,k,n FROM u ORDER BY id", []string{"id", "k", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(-1)}, {int64(2), int64(2), int64(-2)}}))
		run(referenceExec("ALTER TABLE u ALTER k TYPE BIGINT USING 7", 0))
		run(referenceSelect("SELECT k FROM u ORDER BY id", []string{"k"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(7)}}))
		run(referenceErrorDiagnostic("UPDATE u SET n=1", uniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists.")))
		run(partialIndexIDs("SELECT id FROM u WHERE n>0 ORDER BY id"))
		run(referenceExec("UPDATE u SET n=1 WHERE id=1", 1))
		run(referenceExec("REINDEX INDEX pu", 0))
		run(referenceErrorDiagnostic("INSERT INTO u VALUES(3,7,1)", uniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists.")))
	})
}

func TestReferencePartialIndexUniqueTypeDiagnosticPositions(t *testing.T) {
	for _, nnd := range []bool{false, true} {
		t.Run(map[bool]string{false: "distinct", true: "nnd"}[nnd], func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
				run(referenceExec("CREATE TABLE u(id INT,k INT,n INT)", 0))
				run(referenceExec("INSERT INTO u VALUES(1,7,1)", 1))
				suffix := ""
				if nnd {
					suffix = " NULLS NOT DISTINCT"
				}
				run(referenceExec("CREATE UNIQUE INDEX pu ON u(k) INCLUDE(id)"+suffix+" WHERE n>0", 0))
				// PostgreSQL positions refer to reconstructed index text, independent
				// of the ALTER input prefix; count Unicode characters rather than bytes.
				reconstructed := "CREATE UNIQUE INDEX pu ON public.u USING btree (k) INCLUDE (id)" + suffix + " WHERE (n > 0)"
				want := controlsReferenceDiagnostic("42883", "operator does not exist: text > integer")
				want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
				want.Position = referencePosition(utf8.RuneCountInString(reconstructed[:strings.Index(reconstructed, ">")]) + 1)
				for _, prefix := range []string{"", "/*é*/ "} {
					s := referenceErrorDiagnostic(prefix+"ALTER TABLE u ALTER n TYPE TEXT USING n::text", want)
					s.errorStage = "execute"
					run(s)
					run(referencePrepared(s))
				}
				run(referenceSelect("SELECT id,k,n FROM u", []string{"id", "k", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(1)}}))
			})
		})
	}
}

func TestReferencePartialIndexUniqueTypeUnicodePosition(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		run(referenceExec(`CREATE TABLE u(id INT,k INT,"né" INT)`, 0))
		run(referenceExec(`INSERT INTO u VALUES(1,7,1)`, 1))
		run(referenceExec(`CREATE UNIQUE INDEX "pü" ON u(k) INCLUDE(id) NULLS NOT DISTINCT WHERE "né">0`, 0))
		reconstructed := `CREATE UNIQUE INDEX "pü" ON public.u USING btree (k) INCLUDE (id) NULLS NOT DISTINCT WHERE ("né" > 0)`
		want := controlsReferenceDiagnostic("42883", "operator does not exist: text > integer")
		want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		want.Position = referencePosition(utf8.RuneCountInString(reconstructed[:strings.Index(reconstructed, ">")]) + 1)
		s := referenceErrorDiagnostic(`/*é*/ ALTER TABLE u ALTER "né" TYPE TEXT USING "né"::text`, want)
		s.errorStage = "execute"
		run(s)
		run(referencePrepared(s))
		run(referenceSelect(`SELECT id,k,"né" FROM u`, []string{"id", "k", "né"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(1)}}))
	})
}
