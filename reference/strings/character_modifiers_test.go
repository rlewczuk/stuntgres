package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceCharacterModifierGrammarDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, base := range []string{"char", "character", "varchar", "character varying", "bpchar", `"bpchar"`, `"varchar"`} {
		for _, mod := range []string{"-1", "+2", "2,3", "2.5", "'2'", "'oops'", "2147483648"} {
			q := "/*é🙂*/ CREATE TABLE modifier_capture(v " + base + "(" + mod + "))"
			generic := base == "bpchar" || strings.HasPrefix(base, `"`)
			if generic && mod == "'2'" {
				for _, prepared := range []bool{true, false} {
					s := fixture.Exec(q, 0)
					s.Prepared = prepared
					p.Step(t, s)
					typ := "BPCHAR"
					if base == `"varchar"` {
						typ = "VARCHAR"
					}
					for _, backend := range p.Backends() {
						fixture.CharacterMetadata(t, p, backend.DB, "SELECT v FROM modifier_capture", prepared, !backend.PG, []string{"v"}, []string{typ}, []int64{2}, []fixture.CharacterNullability{{true, true}})
					}
					p.Step(t, fixture.Exec("DROP TABLE modifier_capture", 0))
				}
				continue
			}
			want := fixture.Diagnostic{}
			if !generic {
				token := mod
				if mod == "-1" {
					token = "-"
				}
				if mod == "+2" {
					token = "+"
				}
				if mod == "2,3" {
					token = ","
				}
				position := utf8.RuneCountInString(q[:strings.LastIndex(q, "(")+1]) + 1
				if mod == "2,3" {
					position++
				}
				want = fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "` + token + `"`, Position: strconv.Itoa(position)}
			} else {
				want.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, base)]) + 1)
				switch mod {
				case "-1":
					name := "char"
					if base == `"varchar"` {
						name = "varchar"
					}
					want.Code, want.Message = "22023", "length for type "+name+" must be at least 1"
				case "+2":
					want.Code, want.Message = "42601", "type modifiers must be simple constants or identifiers"
				case "2,3":
					want.Code, want.Message = "22023", "invalid type modifier"
				case "2.5":
					want.Code, want.Message = "22P02", `invalid input syntax for type integer: "2.5"`
				case "'oops'":
					want.Code, want.Message = "22P02", `invalid input syntax for type integer: "oops"`
				case "2147483648":
					want.Code, want.Message = "22003", `value "2147483648" is out of range for type integer`
				}
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(q, want)
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared && !generic {
					s.ErrorStage = "prepare"
				}
				p.Step(t, s)
			}
		}
	}
}
