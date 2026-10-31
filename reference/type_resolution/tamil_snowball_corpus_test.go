package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Literal pairs from snowball-data 35999218ecdd19c82fc87f791179d00657007814,
// tamil/{voc,output}.txt: April 2018 Tamil Wikipedia corpus, CC-BY-SA 3.0.
// https://raw.githubusercontent.com/snowballstem/snowball-data/35999218ecdd19c82fc87f791179d00657007814/tamil/COPYING
// Synthetic boundaries follow the generated Tamil algorithm in PostgreSQL
// REL_18_6 (Snowball 2.2.0), not the local development source or the Go kernel.
func TestReferenceTamilSnowballMorphology(t *testing.T) {
	for _, group := range []struct {
		name string
		pairs [][2]string
	}{
		{"historical", [][2]string{
			{"அவன்", "அவன்"}, {"அவள்", "அவள்"}, {"என்ன", "என்ன"}, {"எவ்வளவு", "வளவு"},
			{"கற்கள்", "கல்"}, {"கல்", "கல்"}, {"தமிழர்கள்", "தமிழர்"}, {"தமிழில்", "தமிழ்"},
			{"தமிழ்", "தமிழ்"}, {"நடந்தான்", "நட"}, {"நான்", "நான்"}, {"நீ", "நீ"},
			{"படித்தான்", "படி"}, {"புத்தகங்கள்", "புத்தகம்"}, {"புத்தகம்", "புத்தகம்"},
			{"மக்கள்", "மக்"}, {"மரங்கள்", "மரங்"}, {"மரத்தில்", "மரம்"}, {"மரம்", "மரம்"},
			{"வீடு", "வீடு"}, {"வீடுகள்", "வீடு"},
		}},
		{"overstripping", [][2]string{
			{"மரங்களைவிட", "மரங்களை"}, {"மரத்தினை", "மரத்தி"}, {"மரத்திற்கு", "மரத்தி"},
			{"மரத்திலிருந்து", "மரத்தில்"}, {"மரத்திலேயே", "மரத்திலேய்"}, {"மரத்திலேறி", "மரத்திலேறி"},
			{"வீடுகளில்", "வீடு"}, {"வீடுகளுக்கு", "வீடுகள்"}, {"வீடுகளுக்கும்", "வீடு"}, {"வீடுகளுடன்", "வீடு"},
		}},
		// ASCII padding isolates the release rules without guessing Tamil
		// etymology. These are fixed source-derived predictions for the
		// root's pinned oracle, never values obtained from the Go kernel.
		{"stages", [][2]string{
			{"எக்வோzzzzz", "ஓzzzzz"}, {"எக்வொzzzzz", "ஒzzzzz"},
			{"எக்வுzzzzz", "உzzzzz"}, {"எக்வூzzzzz", "ஊzzzzz"},
			{"அக்வோzzzzz", "ஓzzzzz"}, {"இக்வொzzzzz", "ஒzzzzz"},
			{"உக்வுzzzzz", "உzzzzz"}, {"அக்வூzzzzz", "ஊzzzzz"},
			{"zzzzzவ", "zzzzz"}, {"zzzzzய", "zzzzz"}, {"zzzzzவ்", "zzzzz"},
			{"zzzzzனு", "zzzzz"}, {"zzzzzங்", "zzzzzம்"},
			{"zzzzzகா", "zzzzz"}, {"zzzzzகே", "zzzzz"}, {"zzzzzகோ", "zzzzz"},
			{"zzzzzகும்", "zzzzz"}, {"zzzzzகள்", "zzzzz"},
			{"zzzzzற்கள்", "zzzzzல்"}, {"zzzzzட்கள்", "zzzzzள்"},
			{"zzzzzபி", "zzzzz"}, {"zzzzzவி", "zzzzz"},
			{"zzzzzவிடு", "zzzzz"}, {"zzzzzபற்றி", "zzzzz"},
			{"zzzzzகண்", "zzzzz"}, {"zzzzzமேல்", "zzzzz"},
			{"zzzzzமுன்", "zzzzz"}, {"zzzzzகீழ்", "zzzzz"},
		}},
	} {
		t.Run(group.name, func(t *testing.T) { tamilCorpus(t, group.pairs) })
	}
}

func tamilCorpus(t *testing.T, pairs [][2]string) {
	t.Helper()
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const q = "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, pair := range pairs {
			want := "{" + pair[1] + "}"
			if strings.ContainsAny(pair[1], " \t") {
				want = "{\"" + pair[1] + "\"}"
			}
			dictionaryResult(t, p, b, c, q, want, "tamil_stem", pair[0])
			for range 2 {
				dictionaryResult(t, p, b, held, q, want, "tamil_stem", pair[0])
			}
		}
	})
}

func TestReferenceTamilSnowballBoundaries(t *testing.T) {
	tamilCorpus(t, [][2]string{
		{"கள்", "கள்"}, {"அகள்", "அகள்"}, {"அஅகள்", "அஅ"},
		{"aக்", "aக்"}, {"abக்", "ab"},
		{"CAFÉS", "cafÉs"}, {"café", "café"}, {"cafe\u0301", "cafe\u0301"},
		{"மரங்கள்!", "மரங்கள்!"}, {"A B", "a b"},
		{"RUNNING", "running"}, {"RATS", "rats"}, {"ONLY", "only"}, {"THE", "the"},
		{strings.Repeat("A", 991) + "கள்", strings.Repeat("a", 991)},
		{strings.Repeat("A", 992) + "கள்", strings.Repeat("a", 992) + "கள்"},
	})
}

func TestReferenceTamilSnowballStopwords(t *testing.T) {
	t.Run("english127", tamilEnglishStoplist)
	tamilCorpus(t, [][2]string{{"a", "a"}, {"நான்", "நான்"}, {"THE", "the"}, {"ONLY", "only"}})
}
