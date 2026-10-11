package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferencePartialIndexUniqueTypeFinalPredicate(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE u(id INT,k INT,n INT)", 0))
				run(fixture.Exec("INSERT INTO u VALUES(1,7,-1),(2,7,-2)", 2))
				run(fixture.Exec("CREATE UNIQUE INDEX pu ON u(k) WHERE n>0", 0))
				before := ""
				if !b.PG {
					before = partialIndexDump(t, p)
				}
				failure := fixture.ErrorDiagnostic("ALTER TABLE u ALTER n TYPE BIGINT USING 1", uniqueIndexReferenceBuildDiagnostic("u", "pu", "Key (k)=(7) is duplicated."))
				failure.ErrorStage = "execute"
				if held {
					failure = fixture.Prepared(failure)
				}
				run(failure)
				if !b.PG && before != partialIndexDump(t, p) {
					t.Fatal("failed TYPE changed Dump")
				}
				old := fixture.Select("SELECT id,k,n FROM u ORDER BY id", []string{"id", "k", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(-1)}, {int64(2), int64(7), int64(-2)}})
				run(old)
				run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('pu'::regclass) AS definition", "CREATE UNIQUE INDEX pu ON public.u USING btree (k) WHERE (n > 0)"))
				tx, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				trun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
				trun(fixture.Exec("SAVEPOINT original", 0))
				trun(fixture.Exec("INSERT INTO u VALUES(3,7,-3)", 1))
				trun(fixture.Exec("SAVEPOINT attempt", 0))
				trun(failure)
				trun(fixture.Exec("ROLLBACK TO attempt", 0))
				trun(fixture.Exec("DELETE FROM u WHERE id=2 OR id=3", 2))
				trun(fixture.Prepared(fixture.Exec("ALTER TABLE u ALTER n TYPE BIGINT USING 1", 0)))
				trun(fixture.Select("SELECT id,k,n FROM u", []string{"id", "k", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(7), int64(1)}}))
				trun(fixture.Exec("SAVEPOINT baseline", 0))
				trun(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO u VALUES(4,7,1)", fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists."))))
				trun(fixture.Exec("ROLLBACK TO baseline", 0))
				trun(fixture.Exec("INSERT INTO u VALUES(4,7,-1)", 1))
				trun(fixture.Exec("ROLLBACK TO baseline", 0))
				trun(fixture.Exec("ROLLBACK TO original", 0))
				trun(old)
				trun(fixture.Exec("DELETE FROM u WHERE id=2", 1))
				trun(fixture.Exec("ALTER TABLE u ALTER n TYPE BIGINT USING 1", 0))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				run(fixture.Select("SELECT id,k,n FROM u", []string{"id", "k", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(7), int64(1)}}))
				run(fixture.ErrorDiagnostic("INSERT INTO u VALUES(5,7,1)", fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists.")))
			})
		})
	}
}

func TestReferencePartialIndexUniqueTypeCombined(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE u(id INT,k INT,n INT)", 0))
		run(fixture.Exec("INSERT INTO u VALUES(1,1,-1),(2,2,-2)", 2))
		run(fixture.Exec("CREATE UNIQUE INDEX pu ON u(k) WHERE n>0", 0))
		run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE u ALTER k TYPE BIGINT USING 7, ALTER n TYPE BIGINT USING 1", uniqueIndexReferenceBuildDiagnostic("u", "pu", "Key (k)=(7) is duplicated."))))
		run(fixture.Select("SELECT id,k,n FROM u ORDER BY id", []string{"id", "k", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(-1)}, {int64(2), int64(2), int64(-2)}}))
		run(fixture.Exec("ALTER TABLE u ALTER k TYPE BIGINT USING 7", 0))
		run(fixture.Select("SELECT k FROM u ORDER BY id", []string{"k"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(7)}}))
		run(fixture.ErrorDiagnostic("UPDATE u SET n=1", fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists.")))
		run(fixture.PartialIndexIDs("SELECT id FROM u WHERE n>0 ORDER BY id"))
		run(fixture.Exec("UPDATE u SET n=1 WHERE id=1", 1))
		run(fixture.Exec("REINDEX INDEX pu", 0))
		run(fixture.ErrorDiagnostic("INSERT INTO u VALUES(3,7,1)", fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists.")))
	})
}

func TestReferencePartialIndexUniqueTypeDiagnosticPositions(t *testing.T) {
	for _, nnd := range []bool{false, true} {
		t.Run(map[bool]string{false: "distinct", true: "nnd"}[nnd], func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE u(id INT,k INT,n INT)", 0))
				run(fixture.Exec("INSERT INTO u VALUES(1,7,1)", 1))
				suffix := ""
				if nnd {
					suffix = " NULLS NOT DISTINCT"
				}
				run(fixture.Exec("CREATE UNIQUE INDEX pu ON u(k) INCLUDE(id)"+suffix+" WHERE n>0", 0))
				// PostgreSQL positions refer to reconstructed index text, independent
				// of the ALTER input prefix; count Unicode characters rather than bytes.
				reconstructed := "CREATE UNIQUE INDEX pu ON public.u USING btree (k) INCLUDE (id)" + suffix + " WHERE (n > 0)"
				want := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: text > integer")
				want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
				want.Position = fixture.Position(utf8.RuneCountInString(reconstructed[:strings.Index(reconstructed, ">")]) + 1)
				for _, prefix := range []string{"", "/*é*/ "} {
					s := fixture.ErrorDiagnostic(prefix+"ALTER TABLE u ALTER n TYPE TEXT USING n::text", want)
					s.ErrorStage = "execute"
					run(s)
					run(fixture.Prepared(s))
				}
				run(fixture.Select("SELECT id,k,n FROM u", []string{"id", "k", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(1)}}))
			})
		})
	}
}

func TestReferencePartialIndexUniqueTypeUnicodePosition(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE u(id INT,k INT,"né" INT)`, 0))
		run(fixture.Exec(`INSERT INTO u VALUES(1,7,1)`, 1))
		run(fixture.Exec(`CREATE UNIQUE INDEX "pü" ON u(k) INCLUDE(id) NULLS NOT DISTINCT WHERE "né">0`, 0))
		reconstructed := `CREATE UNIQUE INDEX "pü" ON public.u USING btree (k) INCLUDE (id) NULLS NOT DISTINCT WHERE ("né" > 0)`
		want := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: text > integer")
		want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		want.Position = fixture.Position(utf8.RuneCountInString(reconstructed[:strings.Index(reconstructed, ">")]) + 1)
		s := fixture.ErrorDiagnostic(`/*é*/ ALTER TABLE u ALTER "né" TYPE TEXT USING "né"::text`, want)
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Prepared(s))
		run(fixture.Select(`SELECT id,k,"né" FROM u`, []string{"id", "k", "né"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(1)}}))
	})
}
