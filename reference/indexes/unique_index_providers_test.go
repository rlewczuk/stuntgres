package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUniqueIndexProviderBoundaries(t *testing.T) {
	for _, c := range []struct{ name, key, message, token string }{
		{"expression", "lower(b)", "unique expression indexes are unsupported", "lower"},
		{"trimmed_hash", "b bpchar_ops", "unique index operator class is unsupported", "bpchar_ops"},
		{"trimmed_pattern_hash", "b bpchar_pattern_ops", "unique index operator class is unsupported", "bpchar_pattern_ops"},
	} {
		for _, prepared := range []bool{false, true} {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(fmt.Sprintf("%s/%s/prepared=%t", c.name, b.Name, prepared), func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE ui_t(b TEXT)", 0))
					run(fixture.Exec("INSERT INTO ui_t VALUES('a')", 1))
					q := "CREATE UNIQUE INDEX ui_key ON ui_t(" + c.key + ")"
					s := fixture.Exec(q, 0)
					if !b.PG {
						s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: c.message, Position: fixture.Position(strings.Index(q, c.token) + 1)})
						s.ErrorStage = "execute"
					}
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					if b.PG {
						value, detail := "'A'", "A"
						if c.name != "expression" {
							value, detail = "'a '", "a "
						} else {
							detail = "a"
						}
						label := "b"
						if c.name == "expression" {
							label = "lower(b)"
						}
						run(fixture.ErrorDiagnostic("INSERT INTO ui_t VALUES("+value+")", fixture.UniqueReferenceDuplicate("ui_t", "ui_key", "Key ("+label+")=("+detail+") already exists.")))
						run(fixture.Exec("DROP INDEX ui_key", 0))
					} else {
						run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ui_key'", []string{"relname"}, []string{"NAME"}, nil))
					}
					run(fixture.Select("SELECT b FROM ui_t", []string{"b"}, []string{"TEXT"}, [][]any{{"a"}}))
					// Existing ordinary capabilities are unchanged, not rejected along
					// with the unsupported enforcing combination.
					run(fixture.Prepared(fixture.Exec("CREATE INDEX ui_key ON ui_t("+c.key+")", 0)))
					run(fixture.Exec("INSERT INTO ui_t VALUES('a')", 1))
					run(fixture.Select("SELECT b FROM ui_t", []string{"b"}, []string{"TEXT"}, [][]any{{"a"}, {"a"}}))
					malformed := q + " garbage"
					s = fixture.ImplicitError(malformed, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "garbage"`, Position: fixture.Position(len(q) + 2)})
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					run(s)
				})
			}
		}
	}
}

func TestReferenceUniqueIndexQuotedDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec(`CREATE TABLE ui_t("Key" INT)`, 0))
			run(fixture.Exec(`INSERT INTO ui_t VALUES(1),(1)`, 2))
			want := uniqueIndexReferenceBuildDiagnostic("ui_t", `ui"index`, `Key ("Key")=(1) is duplicated.`)
			run(fixture.Prepared(fixture.ErrorDiagnostic(`CREATE UNIQUE INDEX "ui""index" ON ui_t("Key")`, want)))
			run(fixture.Exec("DELETE FROM ui_t", 2))
			run(fixture.Exec("INSERT INTO ui_t VALUES(1)", 1))
			run(fixture.Exec(`CREATE UNIQUE INDEX "ui""index" ON ui_t("Key")`, 0))
			run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO ui_t VALUES($1)", fixture.UniqueReferenceDuplicate("ui_t", `ui"index`, `Key ("Key")=(1) already exists.`), int64(1))))
		})
	}
}
