package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceHashPartitionDiagnostics(t *testing.T) {
	referenceHashScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := referenceHashRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE hp_diag(k INT) PARTITION BY HASH(k)", 0))
		run(fixture.Exec("CREATE TABLE hp_range(k INT) PARTITION BY RANGE(k)", 0))
		for _, c := range []struct {
			bound, code, message, token string
			parse                       bool
		}{
			{"WITH(MODULUS 4,MODULUS 8,REMAINDER 0)", "42710", "modulus for hash partition provided more than once", "MODULUS 8", true},
			{"WITH(MODULUS 4,REMAINDER 0,REMAINDER 1)", "42710", "remainder for hash partition provided more than once", "REMAINDER 1", true},
			{"WITH(MODULUS 4,foo 0,REMAINDER 0)", "42601", `unrecognized hash partition bound specification "foo"`, "foo", true},
			{"WITH(REMAINDER 0)", "42601", "modulus for hash partition must be specified", "WITH", true},
			{"WITH(MODULUS 4)", "42601", "remainder for hash partition must be specified", "WITH", true},
			{"WITH(MODULUS 0,REMAINDER 0)", "42P16", "modulus for hash partition must be an integer value greater than zero", "", false},
			{"WITH(MODULUS 4,REMAINDER 4)", "42P16", "remainder for hash partition must be less than modulus", "", false},
			{"DEFAULT", "42P16", "a hash-partitioned table may not have a default partition", "", false},
			{"FROM(0) TO(4)", "42P16", "invalid bound specification for a hash partition", "FROM", false},
		} {
			q := `CREATE TABLE "hp_échec" PARTITION OF hp_diag FOR VALUES ` + c.bound
			if c.bound == "DEFAULT" {
				q = `CREATE TABLE "hp_échec" PARTITION OF hp_diag DEFAULT`
			}
			d := fixture.Diagnostic{Code: c.code, Message: c.message}
			if c.token != "" {
				d.Position = referenceRangePosition(q, c.token)
			}
			s := fixture.ImplicitError(q, d)
			s.ErrorStage = "execute"
			if prepared && c.parse {
				s.ErrorStage = "prepare"
			}
			run(s)
			run(fixture.Select(`SELECT relname FROM pg_class WHERE relname='hp_échec'`, []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Exec(`CREATE TYPE "hp_échec" AS (probe INT)`, 0))
			run(fixture.Exec(`DROP TYPE "hp_échec"`, 0))
		}
		for _, token := range []string{"-", "+", "1.5", "2147483648", "$1"} {
			value := token
			if token == "-" || token == "+" {
				value += "1"
			}
			q := "CREATE TABLE hp_bad PARTITION OF hp_diag FOR VALUES WITH(MODULUS " + value + ",REMAINDER 0)"
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "` + token + `"`, Position: referenceRangePosition(q, token)})
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
		}
		q := "CREATE TABLE hp_bad PARTITION OF hp_range FOR VALUES WITH(MODULUS 2,REMAINDER 0)"
		run(referenceRangeError(q, "42P16", "invalid bound specification for a range partition", fixture.Diagnostic{Position: referenceRangePosition(q, "WITH")}))
		run(referenceRangeError("CREATE TABLE hp_json(k JSON) PARTITION BY HASH(k)", "42704", `data type json has no default operator class for access method "hash"`, fixture.Diagnostic{Hint: "You must specify a hash operator class or define a default hash operator class for the data type."}))
		for _, c := range []struct{ old, new, detail string }{
			{"4,REMAINDER 0", "6,REMAINDER 0", `The new modulus 6 is not divisible by 4, the modulus of existing partition "hp_old".`},
			{"6,REMAINDER 0", "4,REMAINDER 0", `The new modulus 4 is not a factor of 6, the modulus of existing partition "hp_old".`},
		} {
			run(fixture.Exec("CREATE TABLE hp_factor(k INT) PARTITION BY HASH(k)", 0))
			run(fixture.Exec("CREATE TABLE hp_old PARTITION OF hp_factor FOR VALUES WITH(MODULUS "+c.old+")", 0))
			run(referenceRangeError("CREATE TABLE hp_new PARTITION OF hp_factor FOR VALUES WITH(MODULUS "+c.new+")", "42P17", "every hash partition modulus must be a factor of the next larger modulus", fixture.Diagnostic{Detail: c.detail}))
			run(fixture.Exec("DROP TABLE hp_factor CASCADE", 0))
		}
		run(fixture.Exec("CREATE TABLE hp_overlap(k INT) PARTITION BY HASH(k)", 0))
		run(fixture.Exec("CREATE TABLE hp_first PARTITION OF hp_overlap FOR VALUES WITH(MODULUS 4,REMAINDER 2)", 0))
		run(fixture.Exec("CREATE TABLE hp_second PARTITION OF hp_overlap FOR VALUES WITH(MODULUS 4,REMAINDER 0)", 0))
		q = "CREATE TABLE hp_new PARTITION OF hp_overlap FOR VALUES WITH(MODULUS 2,REMAINDER 0)"
		run(referenceRangeError(q, "42P17", `partition "hp_new" would overlap partition "hp_second"`, fixture.Diagnostic{Position: referenceRangePosition(q, "WITH")}))
		// A valid retry proves failures did not reserve the child name.
		run(fixture.Exec(strings.Replace(q, "MODULUS 2,REMAINDER 0", "MODULUS 4,REMAINDER 1", 1), 0))
	})
}
