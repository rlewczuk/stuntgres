package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONPredicateUnicode(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()

			for _, escape := range []struct {
				name, source, code, message, detail string
				// A high surrogate is rejected at the closing quote; the other
				// failures stop at the last hexadecimal digit of the escape.
				closingQuote bool
			}{
				{"nul", `\u0000`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, false},
				{"high", `\uD800`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", true},
				{"low", `\uDC00`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", false},
				{"double_high", `\uD800\uD800`, "22P02", "invalid input syntax for type json", "Unicode high surrogate must not follow a high surrogate.", false},
			} {
				for _, shape := range []struct {
					name, prefix, suffix, kind string
				}{
					{"scalar", `"`, `"`, " SCALAR"},
					{"array", `["`, `"]`, " ARRAY"},
					{"object_value", `{"x":"`, `"}`, " OBJECT"},
					{"object_key", `{"`, `":1}`, " OBJECT"},
				} {
					t.Run(escape.name+"/"+shape.name, func(t *testing.T) {
						input := shape.prefix + escape.source + shape.suffix
						for _, kind := range []string{"", " VALUE", " SCALAR", " ARRAY", " OBJECT"} {
							for _, unique := range []string{"", " WITHOUT UNIQUE KEYS", " WITH UNIQUE KEYS"} {
								for _, not := range []bool{false, true} {
									predicate := " IS JSON"
									want := (kind == "" || kind == " VALUE" || kind == shape.kind) && unique != " WITH UNIQUE KEYS"
									if not {
										predicate, want = " IS NOT JSON", !want
									}
									t.Run(predicate+kind+unique, func(t *testing.T) {
										sqljsonBoth(t, p, b, c, fixture.Select(`SELECT $1::text`+predicate+kind+unique+` AS v`,
											[]string{"v"}, []string{"BOOL"}, [][]any{{want}}, input))
									})
								}
							}
						}

						// Nondecoding input/construction must preserve the source,
						// not reject it merely because decoding would fail.
						for _, expr := range []string{`$1::json`, `JSON($1::text)`, `JSON($1::text WITHOUT UNIQUE KEYS)`} {
							t.Run(expr, func(t *testing.T) {
								sqljsonBoth(t, p, b, c, fixture.Select("SELECT "+expr+" AS v",
									[]string{"v"}, []string{"JSON"}, [][]any{{input}}, input))
							})
						}

						d := fixture.ControlsReferenceDiagnostic(escape.code, escape.message)
						d.Detail = escape.detail
						d.Context = "JSON data, line 1: " + shape.prefix + escape.source
						if escape.closingQuote {
							d.Context += `"`
						}
						if !escape.closingQuote || shape.name != "scalar" {
							d.Context += "..."
						}
						for _, expr := range []string{`$1::text::jsonb`, `JSON($1::text WITH UNIQUE KEYS)`, `JSON_ARRAY($1::text FORMAT JSON RETURNING jsonb)`} {
							t.Run("throwing/"+expr, func(t *testing.T) {
								// Parameters keep input evaluation at execute even for
								// prepared calls. All optional fields, including both
								// positions and Hint, are independently required empty.
								sqljsonDiagnostic(t, p, b, c, "SELECT "+expr+" AS v", d, false, input)
							})
						}
					})
				}
			}

			// Valid supplementary code points must not be confused with either
			// unpaired surrogate boundary. NULL remains SQL NULL in both polarities.
			for _, tc := range []struct {
				input any
				kind  string
				want  any
			}{
				{`"\uD834\uDD1E"`, " SCALAR", true},
				{`["\uD834\uDD1E"]`, " ARRAY", true},
				{`{"\uD834\uDD1E":1}`, " OBJECT", true},
				{nil, " SCALAR", nil},
			} {
				for _, not := range []bool{false, true} {
					predicate, want := " IS JSON", tc.want
					if not {
						predicate = " IS NOT JSON"
						if want != nil {
							want = !want.(bool)
						}
					}
					sqljsonBoth(t, p, b, c, fixture.Select(`SELECT $1::text`+predicate+tc.kind+` WITH UNIQUE KEYS AS v`,
						[]string{"v"}, []string{"BOOL"}, [][]any{{want}}, tc.input))
				}
			}
		})
	}
}
