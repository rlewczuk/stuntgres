package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Independently traced from REL_18_6's Indonesian stemmer, pinned Snowball
// d19326ac6c1b9a417fc872f7c2f845265a5e9ece. Oracle discovery adjudicates these
// literal expectations; neither provider output nor production tables define them.
var indonesianMorphology = [][2]string{
	{"menyapu", "sapu"}, {"membaca", "baca"}, {"memakai", "paka"},
	{"menulis", "ulis"}, {"mengambil", "ambil"}, {"penyapu", "sapu"}, {"pemakai", "pakai"},
	{"belajar", "ajar"}, {"pelajar", "ajar"}, {"pelajaran", "ajar"}, {"belajarkan", "ajar"},
	{"bekerja", "kerja"}, {"perbaikan", "baik"}, {"peledakan", "ledak"},
	{"bukulah", "buku"}, {"bukukah", "buku"}, {"bukupun", "buku"},
	{"bukuku", "buku"}, {"bukumu", "buku"}, {"bukunya", "buku"}, {"bukunyalah", "buku"},
	{"mengaaa", "aaa"}, {"pengaaa", "aaa"}, {"teraaa", "aaa"}, {"diaaa", "aaa"},
	{"keaaa", "aaa"}, {"pemaaa", "paaa"}, {"pembaaa", "baaa"}, {"penaaa", "aaa"},
	{"beraaa", "aaa"}, {"peraaa", "aaa"}, {"ailah", "ai"}, {"aiku", "ai"}, {"aailah", "aa"},
	{"buku", "buku"}, {"ai", "ai"}, {"aai", "aa"},
	{"aaakan", "aaa"}, {"aaaan", "aaa"}, {"aaai", "aaa"},
	{"memaaa", "paaa"}, {"membaaa", "baaa"}, {"menyaaa", "saaa"}, {"menybaaa", "ybaaa"},
	{"penyaaa", "saaa"}, {"penybaaa", "ybaaa"},
	{"diaaaan", "aaaan"}, {"diaaakan", "aaa"}, {"keaaakan", "aaak"},
	{"peraaakan", "aaak"}, {"beraaai", "aaai"},
	{"diberaaakan", "aaa"}, {"diberaaaan", "beraaaan"}, {"diberaaa", "beraaa"},
	{"aaakahlah", "aaakah"}, {"aaakunyalah", "aaaku"},
	{"televisi", "televisi"}, {"organisasi", "organisasi"}, {"komunikasi", "komunikasi"}, {"aaasi", "aaasi"},
	{"MENYAPU", "sapu"}, {"MÉNYAPU", "mÉnyapu"}, {"áákan", "áákan"}, {"áákan", "áá"},
}

func TestReferenceIndonesianSnowballMorphology(t *testing.T) {
	for start := 0; start < len(indonesianMorphology); start += 20 {
		group := indonesianMorphology[start:min(start+20, len(indonesianMorphology))]
		t.Run(group[0][0], func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY indonesian_rules (TEMPLATE=snowball,Language='InDoNeSiAn')", 0)
				q := "SELECT ts_lexize('indonesian_rules',$1::text) AS lexemes"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, tc := range group {
					t.Run(tc[0], func(t *testing.T) {
						for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
							dictionaryResult(t, p, b, e, q, "{"+tc[1]+"}", tc[0])
						}
					})
				}
			})
		})
	}
}
