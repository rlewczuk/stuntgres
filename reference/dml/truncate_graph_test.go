package reference_test

import (
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateForeignKeys(t *testing.T) {
	truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		for _, q := range []string{"CREATE TABLE u(id INT PRIMARY KEY)", "CREATE TABLE p(id INT PRIMARY KEY,uid INT REFERENCES u)", "CREATE TABLE c(id INT PRIMARY KEY,pid INT REFERENCES p,other INT REFERENCES p)", "CREATE TABLE g(id INT PRIMARY KEY,cid INT REFERENCES c)"} {
			truncateExec(t, f, f.A, q, 0)
		}
		truncateExec(t, f, f.A, "INSERT INTO u VALUES(90)", 1)
		truncateExec(t, f, f.A, "INSERT INTO p VALUES(1,90),(2,90)", 2)
		// The inbound definition, not child rows, prevents RESTRICT.
		truncateError(t, f, f.A, "TRUNCATE p RESTRICT", truncateFK("p", "c"))
		truncateRows(t, f, f.A, "p", 1, 2)
		s := fixture.ErrorDiagnostic("TRUNCATE p RESTRICT", truncateFK("p", "c"))
		s.Prepared = true
		s.ErrorStage = "execute"
		truncateRun(t, f, f.A, s)
		truncateExec(t, f, f.A, "INSERT INTO c VALUES(7,1,2)", 1)
		truncateExec(t, f, f.A, "INSERT INTO g VALUES(8,7)", 1)
		truncateError(t, f, f.A, "TRUNCATE p,c", truncateFK("c", "g"))
		truncateRows(t, f, f.A, "p", 1, 2)
		truncateRows(t, f, f.A, "c", 7)
		truncateRows(t, f, f.A, "g", 8)
		truncateExec(t, f, f.A, "TRUNCATE p CASCADE", 0)
		truncateNotices(t, f, "c", "g")
		for _, name := range []string{"p", "c", "g"} {
			truncateRows(t, f, f.A, name)
		}
		truncateRows(t, f, f.A, "u", 90)
		truncateExec(t, f, f.A, "INSERT INTO p VALUES(1,90)", 1)
		truncateExec(t, f, f.A, "INSERT INTO c VALUES(7,1,1)", 1)
		truncateExec(t, f, f.A, "INSERT INTO g VALUES(8,7)", 1)
		truncateRun(t, f, f.A, fixture.Prepared(fixture.Exec("TRUNCATE g,c,p RESTRICT", 0)))
		truncateNotices(t, f)
		truncateExec(t, f, f.A, "CREATE TABLE self(id INT PRIMARY KEY,pid INT REFERENCES self)", 0)
		truncateExec(t, f, f.A, "INSERT INTO self VALUES(1,1)", 1)
		truncateExec(t, f, f.A, "TRUNCATE self", 0)
		truncateRows(t, f, f.A, "self")
	})
	t.Run("CompleteCycle", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			// Existing native staged Restore is the real forward-FK provider. ALTER
			// below is PG-only setup, not a native ALTER ADD capability claim.
			if b.PG {
				for _, q := range []string{"CREATE TABLE a(id INT PRIMARY KEY,bid INT)", "CREATE TABLE b(id INT PRIMARY KEY,aid INT)", "INSERT INTO a VALUES(1,2)", "INSERT INTO b VALUES(2,1)", "ALTER TABLE a ADD FOREIGN KEY(bid) REFERENCES b(id)", "ALTER TABLE b ADD FOREIGN KEY(aid) REFERENCES a(id)"} {
					n := int64(0)
					if strings.HasPrefix(q, "INSERT") {
						n = 1
					}
					truncateExec(t, f, f.A, q, n)
				}
			} else {
				script := "CREATE TABLE a(id INT PRIMARY KEY,bid INT REFERENCES b(id));CREATE TABLE b(id INT PRIMARY KEY,aid INT REFERENCES a(id));INSERT INTO a VALUES(1,2);INSERT INTO b VALUES(2,1);"
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
					t.Fatal(err)
				}
			}
			truncateError(t, f, f.A, "TRUNCATE a", truncateFK("a", "b"))
			truncateExec(t, f, f.A, "TRUNCATE a,b", 0)
			truncateRows(t, f, f.A, "a")
			truncateRows(t, f, f.A, "b")
		})
	})
}
func TestReferenceTruncateHierarchy(t *testing.T) {
	t.Run("Inheritance", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			for _, q := range []string{"CREATE TABLE p(id INT)", "CREATE TABLE c() INHERITS(p)", "CREATE TABLE g() INHERITS(c)", "CREATE TABLE other(id INT)", "CREATE TABLE shared() INHERITS(c,other)"} {
				truncateExec(t, f, f.A, q, 0)
			}
			for i, name := range []string{"p", "c", "g", "other", "shared"} {
				truncateExec(t, f, f.A, "INSERT INTO "+name+" VALUES("+fixture.Position(i+1)+")", 1)
			}
			truncateExec(t, f, f.A, "TRUNCATE ONLY(p)", 0)
			// ONLY clears the named parent without clearing physical descendants.
			truncateRows(t, f, f.A, "g", 3)
			truncateRows(t, f, f.A, "shared", 5)
			truncateExec(t, f, f.A, "TRUNCATE p *,other", 0)
			truncateRows(t, f, f.A, "g")
			truncateRows(t, f, f.A, "shared")
			truncateRows(t, f, f.A, "other")
			truncateExec(t, f, f.A, "INSERT INTO g VALUES(9)", 1)
			truncateExec(t, f, f.A, "TRUNCATE p", 0)
			truncateRows(t, f, f.A, "g")
		})
	})
	t.Run("CascadeDoesNotInheritFK", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(id INT,pid INT REFERENCES p)", "CREATE TABLE child() INHERITS(c)"} {
				truncateExec(t, f, f.A, q, 0)
			}
			truncateExec(t, f, f.A, "INSERT INTO p VALUES(1)", 1)
			truncateExec(t, f, f.A, "INSERT INTO c VALUES(2,1)", 1)
			truncateExec(t, f, f.A, "INSERT INTO child VALUES(3,999)", 1)
			truncateExec(t, f, f.A, "TRUNCATE p CASCADE", 0)
			truncateNotices(t, f, "c")
			truncateRows(t, f, f.A, "child", 3)
		})
	})
	t.Run("Partitions", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			for _, q := range []string{"CREATE TABLE root(id INT) PARTITION BY RANGE(id)", "CREATE TABLE low PARTITION OF root FOR VALUES FROM(0) TO(10) PARTITION BY RANGE(id)", "CREATE TABLE leaf PARTITION OF low FOR VALUES FROM(0) TO(10)", "CREATE TABLE high PARTITION OF root FOR VALUES FROM(10) TO(20)", "CREATE TABLE empty_root(id INT) PARTITION BY RANGE(id)"} {
				truncateExec(t, f, f.A, q, 0)
			}
			truncateExec(t, f, f.A, "INSERT INTO root VALUES(1),(11)", 2)
			d := truncateWant("42809", "cannot truncate only a partitioned table")
			d.Hint = "Do not specify the ONLY keyword, or use TRUNCATE ONLY on the partitions directly."
			for _, name := range []string{"root", "empty_root"} {
				truncateError(t, f, f.A, "TRUNCATE ONLY "+name, d)
			}
			truncateExec(t, f, f.A, "TRUNCATE leaf", 0)
			truncateRows(t, f, f.A, "leaf")
			truncateRows(t, f, f.A, "high", 11)
			truncateExec(t, f, f.A, "INSERT INTO root VALUES(2)", 1)
			truncateRun(t, f, f.A, fixture.Prepared(fixture.Exec("TRUNCATE root", 0)))
			truncateRows(t, f, f.A, "leaf")
			truncateRows(t, f, f.A, "high")
		})
	})
}
