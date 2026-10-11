package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePosixTitlecaseConstructors(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			// Independent REL_18_0 expectations: regcomp.c brackpart/onechr
			// route literals, plain brackets and equivalence to allcases, but
			// singleton ranges and collating elements to range. regc_locale.c
			// allcases contains only lower/upper (ǆ/Ǆ); range also retains ǅ.
			// regc_pg_locale.c leaves non-ASCII unchanged in C/POSIX and uses
			// simple Unicode mappings in both admitted builtin providers.
			for _, coll := range []string{"C", "POSIX", "pg_c_utf8", "pg_unicode_fast"} {
				unicode := coll == "pg_c_utf8" || coll == "pg_unicode_fast"
				copyName := "posix_title_copy_" + coll
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(fmt.Sprintf(`CREATE COLLATION "%s" FROM "%s"`, copyName, coll), 0))
				for _, name := range []string{coll, copyName} {
					for _, constructor := range []struct {
						name, pattern string
						retainsTitle  bool
					}{
						{"literal", `^ǅ$`, false},
						{"plain_bracket", `^[ǅ]$`, false},
						{"equivalence", `^[[=ǅ=]]$`, false},
						{"singleton_range", `^[ǅ-ǅ]$`, true},
						{"collating_element", `^[[.ǅ.]]$`, true},
					} {
						t.Run(name+"/"+constructor.name, func(t *testing.T) {
							for _, source := range []string{"Ǆ", "ǅ", "ǆ"} {
								sensitive := source == "ǅ"
								insensitive := sensitive
								if unicode {
									insensitive = source != "ǅ" || constructor.retainsTitle
								}
								var piece any
								if insensitive {
									piece = source
								}
								q := fmt.Sprintf(`SELECT $1::text ~ $2::text COLLATE "%[1]s" AS s,$1::text ~* $2::text COLLATE "%[1]s" AS i,$1::text !~ $2::text COLLATE "%[1]s" AS n,$1::text !~* $2::text COLLATE "%[1]s" AS ni,substring($1::text,$3::text COLLATE "%[1]s") AS piece`, name)
								similarReferenceRoutes(t, p, b, posixSelect(q, []string{"s", "i", "n", "ni", "piece"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "TEXT"}, [][]any{{sensitive, insensitive, !sensitive, !insensitive, piece}}, source, constructor.pattern, "(?i)"+constructor.pattern))
							}
						})
					}
					// C controls still perform ASCII case closure, not exact-only matching.
					similarReferenceRoutes(t, p, b, posixSelect(fmt.Sprintf(`SELECT 'a' ~* '^A$' COLLATE "%s" AS m`, name), []string{"m"}, []string{"BOOL"}, [][]any{{true}}))
				}
			}
		})
	}
}

func TestReferencePosixNestedAssertions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			// REL_18_0 rege_dfa.c lacon evaluates child lookahead up to the
			// original v->stop, even when its parent lookbehind ends earlier.
			// Positive/negative polarity applies after child satisfaction;
			// assertions consume nothing and their inner groups do not number.
			for _, c := range []struct {
				name, source, pattern string
				piece                 any
			}{
				{"positive_continuation", "abc", `^(?=a(?=b(?=c))bc)(a)bc$`, "a"},
				{"positive_child_fails", "abd", `^(?=a(?=b(?=c))bd)(a)bd$`, nil},
				{"positive_negative_child", "abd", `^(?=a(?!bc)b)(a)bd$`, "a"},
				{"negative_child_fails", "abc", `^(?=a(?!bc)b)(a)bc$`, nil},
				{"negative_positive_child", "abd", `^(?!a(?=bc))(a)bd$`, "a"},
				{"negative_parent_fails", "abc", `^(?!a(?=bc))(a)bc$`, nil},
				{"double_negative", "abc", `^(?!a(?!bc))(a)bc$`, "a"},
				{"double_negative_fails", "abd", `^(?!a(?!bc))(a)bd$`, nil},
				{"inner_group_unnumbered", "ab", `^(?=a(?=(b)))(a)b$`, "a"},
				{"behind_ahead_beyond_endpoint", "aabc", `(?<=a+(?=bc))(b)c`, "b"},
				{"behind_ahead_longer_width", "aaaabc", `(?<=a+(?=bc))(b)c`, "b"},
				{"behind_ahead_fails", "aabd", `(?<=a+(?=bc))(b)d`, nil},
				{"negative_behind_ahead", "aabd", `(?<!a+(?=bc))(b)d`, "b"},
				{"negative_behind_fails", "aabc", `(?<!a+(?=bc))(b)c`, nil},
				{"behind_negative_ahead", "aabd", `(?<=a+(?!bc))(b)d`, "b"},
				{"behind_negative_ahead_fails", "aabc", `(?<=a+(?!bc))(b)c`, nil},
				{"negative_behind_negative_ahead", "aabc", `(?<!a+(?!bc))(b)c`, "b"},
				{"negative_behind_negative_ahead_fails", "aabd", `(?<!a+(?!bc))(b)d`, nil},
				{"behind_nested_ahead", "aabc", `(?<=a+(?=b(?=c)))(b)c`, "b"},
				{"behind_nested_ahead_fails", "aabd", `(?<=a+(?=b(?=c)))(b)d`, nil},
				{"ahead_nested_behind", "aab", `^aa(?=(?<=a+)b)(b)$`, "b"},
				{"ahead_nested_behind_fails", "ccb", `^cc(?=(?<=a+)b)(b)$`, nil},
				{"utf8_endpoint", "éé🙂x", `(?<=é+(?=🙂x))(🙂)x`, "🙂"},
				{"utf8_endpoint_fails", "éé🙂y", `(?<=é+(?=🙂x))(🙂)y`, nil},
			} {
				t.Run(c.name, func(t *testing.T) {
					matched := c.piece != nil
					similarReferenceRoutes(t, p, b, posixSelect(`SELECT $1::text ~ $2::text AS m,$1::text !~ $2::text AS n,substring($1::text,$2::text) AS piece`, []string{"m", "n", "piece"}, []string{"BOOL", "BOOL", "TEXT"}, [][]any{{matched, !matched, c.piece}}, c.source, c.pattern))
				})
			}
		})
	}
}
