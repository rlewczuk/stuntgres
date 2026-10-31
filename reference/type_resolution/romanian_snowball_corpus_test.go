package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Natural pairs are published at snowballstem.org/algorithms/romanian/stemmer.html.
// Synthetic expectations are independently traced from the Romanian algorithm at
// d19326ac6c1b9a417fc872f7c2f845265a5e9ece, bundled in PostgreSQL REL_18_6.
// They are oracle-discovery expectations, not values learned from native output.
func TestReferenceRomanianSnowballMorphology(t *testing.T) {
	romanianCorpus(t, [][2]string{
		{"abruptă", "abrupt"}, {"abrutiza", "abrutiz"}, {"abrutizare", "abrutiz"},
		{"abrutizanţi", "abrutizanț"}, {"abrutizanți", "abrutizanț"}, {"absentare", "absent"},
		{"absenteism", "absenteist"}, {"absenteistă", "absenteist"}, {"absenteişti", "absenteist"},
		{"absenţa", "absenț"}, {"absența", "absenț"}, {"ocol", "ocol"}, {"ocolea", "ocol"},
		{"ocolească", "ocol"}, {"ocolesc", "ocol"}, {"ocoleşte", "ocol"}, {"ocolește", "ocol"},
		{"ocoli", "ocol"}, {"ocolind", "ocol"}, {"ocolul", "ocol"}, {"ocolului", "ocol"},
		{"ocoluri", "ocolur"}, {"ocolurile", "ocolur"}, {"ocolişuri", "ocolișur"}, {"ocolișuri", "ocolișur"},
		{"ocrotire", "ocrot"}, {"ocrotiri", "ocrotir"}, {"ocrotitoare", "ocrot"}, {"ocrotitor", "ocrot"},
		{"ocrotitori", "ocrot"}, {"octavă", "octav"}, {"octeţi", "octeț"}, {"octeţii", "octeț"},
	})
	t.Run("release-endings", romanianReleaseEndings)
}

func romanianCorpus(t *testing.T, pairs [][2]string) {
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
			dictionaryResult(t, p, b, c, q, want, "romanian_stem", pair[0])
			for range 2 {
				dictionaryResult(t, p, b, held, q, want, "romanian_stem", pair[0])
			}
		}
	})
}

// The literal endings below are test-owned transcriptions of all released
// 16/46/62/94/5 stage entries, not production-table iteration. Fixed babab puts
// each ending in R2 and its consonantal predecessor inside RV. Later stages
// delete combining replacements. Prelude and step0 can prevent a literal ending
// from reaching its named stage; the explicit exceptions below preserve those
// public whole-algorithm outcomes as well as every released ending.
func romanianReleaseEndings(t *testing.T) {
	groups := []struct {
		name, endings, prefix, result string
		count                         int
	}{
		{"step0", "ul ului aua ea ele elor ii iua iei iile iilor ilor ile atei ație ația", "babab", "babab", 16},
		{"combo", "abilitate abilitati abilităi abilități ibilitate ivitate ivitati ivităi ivități icitate icitati icităi icități icator icatori iciv iciva icive icivi icivă ical icala icale icali icală ativ ativa ative ativi ativă ațiune atoare ator atori ătoare ător ători itiv itiva itive itivi itivă ițiune itoare itor itori", "babab", "babab", 46},
		{"standard-delete", "at ata ată ati ate ut uta ută uti ute it ita ită iti ite ic ica ice ici ică abil abila abile abili abilă ibil ibila ibile ibili ibilă oasa oasă oase os osi oși ant anta ante anti antă ator atori itate itati ităi ități iv iva ive ivi ivă", "babab", "babab", 52},
		{"standard-ist", "ism isme ist ista iste isti istă iști", "babab", "bababist", 8},
		{"standard-iune", "iune iuni", "bababț", "bababt", 2},
		{"verb-conditional", "are ere ire âre ind ând indu ându eze ească ez ezi ează esc ești ește ăsc ăști ăște am ai au eam eai ea eați eau iam iai ia iați iau ui ași arăm arăți ară uși urăm urăți ură iși irăm irăți iră âi âși ârăm ârăți âră asem aseși ase aserăm aserăți aseră isem iseși ise iserăm iserăți iseră âsem âseși âse âserăm âserăți âseră usem useși use userăm userăți useră", "babab", "babab", 74},
		{"verb-unconditional", "ăm ați em eți im iți âm âți seși serăm serăți seră sei se sesem seseși sese seserăm seserăți seseră", "babab", "babab", 20},
		{"final", "a e i ie ă", "babab", "babab", 5},
	}
	for _, group := range groups {
		t.Run(group.name, func(t *testing.T) {
			endings := strings.Fields(group.endings)
			if len(endings) != group.count {
				t.Fatalf("independent %s inventory: %d != %d", group.name, len(endings), group.count)
			}
			var pairs [][2]string
			for _, ending := range endings {
				want := group.result
				switch {
				case group.name == "step0" && ending == "aua":
					// Prelude aUa prevents step0's lowercase aua match.
					// Final a removal and postlude leave au.
					want = "bababau"
				case group.name == "step0" && ending == "iua":
					// Prelude iUa similarly prevents the iua match.
					want = "bababiu"
				case group.name == "step0" && ending == "ile":
					// The preceding ab blocks step0. Longest standard abile
					// starts before fixed R2, so only final e is removed.
					want = "bababil"
				case group.name == "standard-delete" && ending == "ibile":
					// Step0 ile->i is allowed after ib (not ab). This leaves
					// bababibi, not a standard suffix; final i is removed.
					want = "bababib"
				}
				pairs = append(pairs, [2]string{group.prefix + ending, want})
			}
			romanianCorpus(t, pairs)
		})
	}
}

func TestReferenceRomanianSnowballBoundaries(t *testing.T) {
	romanianCorpus(t, [][2]string{
		{"bababului", "babab"}, {"bababaua", "bababau"}, {"bababea", "babab"}, {"bababii", "babab"},
		{"bababatei", "babab"}, {"bababația", "babab"}, {"tabile", "tabil"}, {"copile", "cop"},
		{"ababilitate", "ababil"}, {"abilitate", "abil"}, {"bababativitate", "babab"},
		{"bababisme", "bababist"}, {"bababistă", "bababist"}, {"bababțiune", "bababt"},
		{"abețiune", "abet"}, {"bababiune", "bababiun"}, {"bababare", "babab"}, {"cabare", "cabar"},
		{"bababaare", "bababaar"}, {"bababuare", "bababu"}, {"cabați", "cab"}, {"bababareic", "bababar"},
		{"aia", "aia"}, {"baia", "bai"}, {"baiua", "baiu"},
		{"abie", "abie"}, {"cabase", "cabas"},
		{"ABRUPTă", "abrupt"}, {"ABRUPTĂ", "abruptĂ"}, {"ş", "ș"}, {"ţ", "ț"}, {"Ş", "Ş"}, {"Ţ", "Ţ"},
		{"abruptă!", "abruptă!"}, {"abrupta\u0306", "abrupta\u0306"}, {"café", "café"}, {"A B", "a b"},
		{"THE", "the"}, {"ONLY", "only"}, {"RUNNING", "running"},
		{strings.Repeat("ş", 500), strings.Repeat("ș", 500)},
		{"A" + strings.Repeat("ş", 500), "a" + strings.Repeat("ş", 500)},
		{"babab" + strings.Repeat("b", 992) + "are", "babab" + strings.Repeat("b", 992)},
		{"BABAB" + strings.Repeat("B", 993) + "are", "babab" + strings.Repeat("b", 993) + "are"},
		{"babab" + strings.Repeat("ș", 496) + "are", "babab" + strings.Repeat("ș", 496)},
		{"BABAB" + strings.Repeat("ș", 496) + "aare", "babab" + strings.Repeat("ș", 496) + "aare"},
	})
}

func TestReferenceRomanianSnowballStopwords(t *testing.T) {
	t.Run("english127", romanianEnglishStoplist)
	romanianCorpus(t, [][2]string{{"a", "a"}, {"și", "și"}, {"şi", "și"}, {"în", "în"}, {"la", "la"}, {"cu", "cu"}, {"este", "este"}, {"sunt", "sunt"}, {"THE", "the"}, {"ONLY", "only"}, {"being", "being"}})
}
