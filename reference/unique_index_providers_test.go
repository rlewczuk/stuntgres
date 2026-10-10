package reference_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestReferenceUniqueIndexProviderBoundaries(t *testing.T) {
	for _, c := range []struct{ name, key, message, token string }{
		{"expression", "lower(b)", "unique expression indexes are unsupported", "lower"},
		{"trimmed_hash", "b bpchar_ops", "unique index operator class is unsupported", "bpchar_ops"},
		{"trimmed_pattern_hash", "b bpchar_pattern_ops", "unique index operator class is unsupported", "bpchar_pattern_ops"},
	} {
		for _, prepared := range []bool{false, true} {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(fmt.Sprintf("%s/%s/prepared=%t", c.name, b.name, prepared), func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE TABLE ui_t(b TEXT)", 0))
					run(referenceExec("INSERT INTO ui_t VALUES('a')", 1))
					q := "CREATE UNIQUE INDEX ui_key ON ui_t(" + c.key + ")"
					s := referenceExec(q, 0)
					if !b.pg {
						s = referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: c.message, Position: referencePosition(strings.Index(q, c.token) + 1)})
						s.errorStage = "execute"
					}
					if prepared {
						s = referencePrepared(s)
					}
					run(s)
					if b.pg {
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
						run(referenceErrorDiagnostic("INSERT INTO ui_t VALUES("+value+")", uniqueReferenceDuplicate("ui_t", "ui_key", "Key ("+label+")=("+detail+") already exists.")))
						run(referenceExec("DROP INDEX ui_key", 0))
					} else {
						run(referenceSelect("SELECT relname FROM pg_class WHERE relname='ui_key'", []string{"relname"}, []string{"NAME"}, nil))
					}
					run(referenceSelect("SELECT b FROM ui_t", []string{"b"}, []string{"TEXT"}, [][]any{{"a"}}))
					// Existing ordinary capabilities are unchanged, not rejected along
					// with the unsupported enforcing combination.
					run(referencePrepared(referenceExec("CREATE INDEX ui_key ON ui_t("+c.key+")", 0)))
					run(referenceExec("INSERT INTO ui_t VALUES('a')", 1))
					run(referenceSelect("SELECT b FROM ui_t", []string{"b"}, []string{"TEXT"}, [][]any{{"a"}, {"a"}}))
					malformed := q + " garbage"
					s = referenceImplicitError(malformed, referenceDiagnostic{Code: "42601", Message: `syntax error at or near "garbage"`, Position: referencePosition(len(q) + 2)})
					if prepared {
						s = referencePrepared(s)
						s.errorStage = "prepare"
					}
					run(s)
				})
			}
		}
	}
}

func TestReferenceUniqueIndexQuotedDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec(`CREATE TABLE ui_t("Key" INT)`, 0))
			run(referenceExec(`INSERT INTO ui_t VALUES(1),(1)`, 2))
			want := uniqueIndexReferenceBuildDiagnostic("ui_t", `ui"index`, `Key ("Key")=(1) is duplicated.`)
			run(referencePrepared(referenceErrorDiagnostic(`CREATE UNIQUE INDEX "ui""index" ON ui_t("Key")`, want)))
			run(referenceExec("DELETE FROM ui_t", 2))
			run(referenceExec("INSERT INTO ui_t VALUES(1)", 1))
			run(referenceExec(`CREATE UNIQUE INDEX "ui""index" ON ui_t("Key")`, 0))
			run(referencePrepared(referenceErrorDiagnostic("INSERT INTO ui_t VALUES($1)", uniqueReferenceDuplicate("ui_t", `ui"index`, `Key ("Key")=(1) already exists.`), int64(1))))
		})
	}
}
