package functions

import "testing"

// Independently traced release witnesses; cases→case and gosses→gos are observed PG18.6 outputs.
// gossos→goss uses the released once-only residual os deletion, not repeated s deletion.
var catalanUnitGoldens = [][2]string{
	{"gat", "gat"}, {"gats", "gat"}, {"casa", "cas"}, {"cases", "case"},
	{"gossos", "goss"}, {"gosses", "gos"}, {"homes", "hom"}, {"dones", "don"}, {"llibres", "llibr"},
	{"català", "catal"}, {"catalana", "catal"}, {"catalans", "catalan"}, {"catalanes", "catalan"},
	{"café", "caf"}, {"cafés", "caf"}, {"cafès", "caf"}, {"qüestió", "quest"}, {"qüestions", "quest"},
	{"col·laboracions", "col.labor"}, {"de", "de"}, {"i", "i"}, {"la", "la"}, {"les", "les"}, {"els", "el"},
	{"cafe\u0301", "cafe\u0301"}, {"gats!", "gats!"}, {"ç", "ç"}, {"À", "À"}, {"à", "a"}, {"l·l", "l.l"},
	{"bíbas", "bib"}, {"bìbas", "bibas"}, {"babab-la", "babab"}, {"babab'ls", "babab"},
	{"bababselas", "babab"}, {"babab'ho", "babab"}, {"babab’ho", "babab’"}, {"basela", "basel"},
	{"babab-me-me", "babab-m"}, {"bababaments", "babab"}, {"bababacions", "babab"},
	{"babablogies", "babablog"}, {"bababica", "bababic"}, {"bababquíssima", "bababc"},
	{"babada", "bab"}, {"babacions", "babacion"}, {"bababarem", "babab"}, {"bababiré", "babab"},
	{"bababàvem", "babab"}, {"bababïsquen", "babab"}, {"bababando", "babab"}, {"babando", "baband"},
	{"bababiqu", "bababic"}, {"bababaos", "bababa"}, {"bababamentament", "bababament"},
}

func TestSnowballCatalanRules(t *testing.T) {
	for _, tc := range catalanUnitGoldens {
		if got := catalanStem(tc[0]); got != tc[1] {
			t.Errorf("%q: got %q, want %q", tc[0], got, tc[1])
		}
	}
	for _, tc := range []struct {
		input  string
		p1, p2 int
	}{
		{"", 0, 0}, {"b", 1, 1}, {"babab", 3, 5}, {"bíbas", 4, 6}, {"bìbas", 6, 6},
	} {
		if p1, p2 := catalanRegions(tc.input); p1 != tc.p1 || p2 != tc.p2 {
			t.Errorf("regions %q: (%d,%d), want (%d,%d)", tc.input, p1, p2, tc.p1, tc.p2)
		}
	}
	if len(catalanPronouns) != 39 || len(catalanStandardR1)+len(catalanStandardOther) != 200 || len(catalanVerbR1)+1 != 283 || len(catalanResidual)+1 != 22 {
		t.Fatal("incomplete released suffix inventory")
	}
	// Isolate actions so final residual/cleaning cannot mask a wrong stage.
	for _, tc := range []struct {
		stage, input, want string
		p1, p2             int
		applied            bool
	}{
		{"pronoun", "babab-la", "babab", 5, 5, true},
		{"pronoun", "babab-la", "babab-la", 6, 6, false},
		{"pronoun", "basela", "basela", 3, 6, false},
		{"standard", "bababaments", "babab", 5, 5, true},
		{"standard", "bababaments", "bababaments", 6, 6, false},
		{"standard", "bababacions", "babab", 3, 5, true},
		{"standard", "bababacions", "bababacions", 3, 6, false},
		{"standard", "babablogíes", "babablog", 3, 5, true},
		{"standard", "babablogíes", "babablogíes", 3, 6, false},
		{"standard", "bababica", "bababic", 3, 5, true},
		{"standard", "bababica", "bababica", 3, 6, false},
		{"standard", "bababquíssima", "bababc", 5, 5, true},
		{"standard", "bababquíssima", "bababquíssima", 6, 6, false},
		{"standard", "babacions", "babacions", 3, 5, false},
		{"standard", "bababamentament", "bababament", 3, 5, true},
		{"verb", "bababarem", "babab", 5, 5, true},
		{"verb", "bababarem", "bababarem", 6, 6, false},
		{"verb", "bababando", "babab", 3, 5, true},
		{"verb", "bababando", "bababando", 3, 6, false},
		{"residual", "bababs", "babab", 5, 5, true},
		{"residual", "bababs", "bababs", 6, 6, false},
		{"residual", "bababiqu", "bababic", 5, 5, true},
		{"residual", "bababiqu", "bababiqu", 6, 6, false},
		{"residual", "bababaos", "bababa", 3, 5, true},
	} {
		w := catalanWord{input: tc.input}
		regions := [3]int{0, tc.p1, tc.p2}
		var applied bool
		switch tc.stage {
		case "pronoun":
			applied = w.replace(w.longest(catalanPronouns[:], 1, catalanRule{}), regions)
		case "standard":
			applied = w.standard(regions)
		case "verb":
			applied = w.verb(regions)
		case "residual":
			applied = w.residual(regions)
		}
		got := w.input
		if w.buffer != nil {
			got = string(w.buffer)
		}
		if applied != tc.applied || got != tc.want {
			t.Errorf("%s %q: %q/%v, want %q/%v", tc.stage, tc.input, got, applied, tc.want, tc.applied)
		}
	}
	if got := catalanStem("àáèéìíïòóúü·çÀ"); got != "aaeeiiioouu.çÀ" {
		t.Fatal("cleaning", got)
	}
}
