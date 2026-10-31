package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Literal expectations traced independently from REL_18_6 generated Nepali C.
// The pinned PostgreSQL oracle adjudicates these before production changes.
func TestReferenceNepaliSnowballMorphology(t *testing.T) {
	t.Run("staged-rules", func(t *testing.T) {
		nepaliCorpus(t, [][2]string{
			{"घरहरू", "घर"}, {"बालकहरू", "बालक"}, {"किताबहरूलाई", "किताब"},
			{"विद्यालयमा", "विद्यालय"}, {"नेपालीको", "नेपाली"}, {"गरेको", "गर"}, {"गर्दै", "गर्"},
			{"घरहरूनु", "घर"}, {"घरमामा", "घरमा"}, {"घरहरूमामा", "घरहरूमा"},
			{"घरएकि", "घरएकि"}, {"घरेकि", "घरेकि"}, {"घरएकी", "घर"}, {"घरेकी", "घर"},
			{"घरएको", "घर"}, {"घरेको", "घर"}, {"घरयौँ", "घर"}, {"घरयौं", "घर"},
			{"घरथें", "घर"}, {"घरथेँ", "घर"}, {"घरत्रै", "घरत्र"}, {"घरकाँ", "घरकाँ"},
			{"घरत्रैँ", "घरत्रैँ"}, {"घरको!", "घरको!"}, {"घरको", "घर"},
			{"सबैको", "सबै"}, {"RATS", "rats"}, {"THE", "the"}, {"CAFÉS", "cafÉs"}, {"café", "café"},
			{"नु", "नु"}, {"हरू", "हरू"},
		})
	})
	t.Run("all-category-one", func(t *testing.T) {
		var cases [][2]string
		for _, suffix := range strings.Fields("लाई लाइ ले सँगै मै सँग संग मार्फत रत मा द्वारा पछि की कै को का कि") {
			cases = append(cases, [2]string{"घर" + suffix, "घर"})
		}
		nepaliCorpus(t, cases)
	})
	t.Run("longest-repeating-category-three", func(t *testing.T) {
		nepaliCorpus(t, [][2]string{
			{"घरहुनेछ", "घर"}, {"घरहुन्छ", "घर"}, {"घरनेछन्", "घर"}, {"घरनेछु", "घर"}, {"घरनेछौ", "घर"},
			{"घरथिइस्", "घर"}, {"घरथियो", "घर"}, {"घरथियौ", "घर"}, {"घरदेखी", "घर"}, {"घरदेखि", "घर"},
			{"घरमाथि", "घर"}, {"घरनेका", "घर"}, {"घरनेकै", "घर"}, {"घरनेको", "घर"},
			{"घरहरूहरूनु", "घर"},
		})
	})
	t.Run("all-mark-contexts", func(t *testing.T) {
		var cases [][2]string
		// Category two removes the nasal mark after नौ, but नौ itself is
		// not a category-three suffix; preserve that context in the stem.
		for _, context := range []string{"यौ", "छौ", "नौ", "थे"} {
			stem := "घर"
			if context == "नौ" {
				stem = "घरनौ"
			}
			for _, mark := range []string{"ँ", "ं"} {
				cases = append(cases, [2]string{"घर" + context + mark, stem})
			}
		}
		nepaliCorpus(t, cases)
	})
}

func nepaliCorpus(t *testing.T, cases [][2]string) {
	t.Helper()
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const query = "SELECT ts_lexize('nepali_stem',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range cases {
			t.Run(tc[0], func(t *testing.T) {
				for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}} {
					dictionaryResult(t, p, b, e, query, "{"+tc[1]+"}", tc[0])
				}
			})
		}
	})
}
