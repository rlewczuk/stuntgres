package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func queryWindowReviewReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, name string, expected fixture.Step) []fixture.Step {
	t.Helper()
	steps := []fixture.Step{expected}
	for _, prepared := range []bool{false, true} {
		var definition string
		q := "SELECT definition FROM pg_catalog.pg_views WHERE viewname='" + name + "'"
		if prepared {
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			err = stmt.QueryRowContext(p.Ctx).Scan(&definition)
			closeErr := stmt.Close()
			if err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
		} else if err := b.DB.QueryRowContext(p.Ctx, q).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if name == "named_capture" {
			lowered := strings.ToLower(definition)
			start := strings.LastIndex(lowered, "unused")
			if start < 0 {
				t.Fatal("unused named declaration omitted", definition)
			}
			for _, fragment := range []string{"regclass", "1.00", "inner_t"} {
				if !strings.Contains(lowered[start:], fragment) {
					t.Fatal("unused named key omitted", fragment, definition)
				}
			}
		}
		replay := name + "_direct"
		if prepared {
			replay = name + "_prepared"
		}
		create := fixture.Exec("CREATE VIEW "+replay+" AS "+definition, 0)
		create.Prepared = prepared
		queryWindowRun(t, p, b, create)
		result := expected
		result.Query = "SELECT * FROM " + replay + " ORDER BY id"
		queryWindowRoutes(t, p, b, result)
		steps = append(steps, result)
	}
	return steps
}

func queryWindowReviewRestore(t *testing.T, p *fixture.Pair, steps []fixture.Step) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	fresh := fixture.NewFixture(t, false, true)
	if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		for _, prepared := range []bool{false, true} {
			step.Prepared = prepared
			fresh.Step(t, step)
		}
	}
}

func TestReferenceQueryWindowReviewPersistentPaths(t *testing.T) {
	t.Run("named_capture", func(t *testing.T) {
		queryWindowReviewScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			queryWindowSeed(t, p, b)
			queryWindowRun(t, p, b, fixture.Exec("CREATE VIEW named_capture AS SELECT id,count(*) OVER w AS n FROM t WINDOW w AS(PARTITION BY 't'::REGCLASS,1.00::NUMERIC ORDER BY (SELECT k FROM t AS inner_t WHERE inner_t.id=1),k),unused AS(PARTITION BY 't'::REGCLASS,1.00::NUMERIC ORDER BY (SELECT k FROM t AS inner_t WHERE inner_t.id=1))", 0))
			expected := fixture.Select("SELECT id,n FROM named_capture ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT8"}, queryWindowValues(5, 5, 6, 3, 7, 2, 2))
			queryWindowRoutes(t, p, b, expected)
			steps := queryWindowReviewReplay(t, p, b, "named_capture", expected)
			if !b.PG {
				queryWindowReviewRestore(t, p, steps)
			}
		})
	})
	t.Run("enum_domain", func(t *testing.T) {
		queryWindowReviewScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			for _, step := range []fixture.Step{
				fixture.Exec("CREATE TYPE window_enum AS ENUM('z','a')", 0),
				fixture.Exec("CREATE DOMAIN window_domain AS window_enum", 0),
				fixture.Exec("CREATE TABLE enum_source(id INT,e window_domain)", 0),
				fixture.Exec("INSERT INTO enum_source VALUES(1,'a'),(2,'z'),(3,'a'),(4,NULL)", 4),
				fixture.Exec("CREATE VIEW enum_window AS SELECT id,row_number() OVER(PARTITION BY e ORDER BY id) AS rn,count(*) OVER(ORDER BY e) AS n FROM enum_source", 0),
			} {
				queryWindowRun(t, p, b, step)
			}
			expected := fixture.Select("SELECT id,rn,n FROM enum_window ORDER BY id", []string{"id", "rn", "n"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(3)}, {int64(2), int64(1), int64(1)}, {int64(3), int64(2), int64(3)}, {int64(4), int64(1), int64(4)}})
			queryWindowRoutes(t, p, b, expected)
			steps := queryWindowReviewReplay(t, p, b, "enum_window", expected)
			if !b.PG {
				queryWindowReviewRestore(t, p, steps)
			}
		})
	})
	t.Run("key_only_dependency", func(t *testing.T) {
		queryWindowReviewScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			for _, step := range []fixture.Step{
				fixture.Exec("CREATE TABLE key_source(id INT,k INT)", 0),
				fixture.Exec("INSERT INTO key_source VALUES(1,2),(2,1)", 2),
				fixture.Exec("CREATE VIEW key_only AS SELECT id,row_number() OVER(ORDER BY k) AS rn FROM key_source", 0),
			} {
				queryWindowRun(t, p, b, step)
			}
			expected := fixture.Select("SELECT id,rn FROM key_only ORDER BY id", []string{"id", "rn"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}})
			source := fixture.Select("SELECT id,k FROM key_source ORDER BY id", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}})
			failure := fixture.ImplicitError("ALTER TABLE key_source DROP COLUMN k RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column k of table key_source because other objects depend on it", Detail: "view key_only depends on column k of table key_source", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			failure.ErrorStage = "execute"
			queryWindowRoutes(t, p, b, expected)
			for _, prepared := range []bool{false, true} {
				failure.Prepared = prepared
				queryWindowRun(t, p, b, failure)
				queryWindowRoutes(t, p, b, source)
				queryWindowRoutes(t, p, b, expected)
			}
			if !b.PG {
				queryWindowReviewRestore(t, p, []fixture.Step{failure, source, expected})
			}
		})
	})
}
