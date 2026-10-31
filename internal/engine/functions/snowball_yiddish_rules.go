// Yiddish Snowball rules from PostgreSQL REL_18_6, synchronized to
// snowballstem/snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece.
// Copyright and BSD-3-Clause terms: snowball_LICENSE.txt.
package functions

const (
	yiddishKeep uint8 = iota
	yiddishDelete
	yiddishRewrite
	yiddishIES
	yiddishNested
	yiddishTes
	yiddishOS
	yiddishIST
)

type yiddishRule struct {
	ending string
	action uint8
	replacement string
}

var yiddishPrefixes = [...]string{
	"אדורכ", "דורכ", "אהינ", "אהער", "אװעק", "מיט", "אנטקעגנ", "אקעגנ",
	"אנידער", "אראפ", "ארױס", "ארױפ", "ארומ", "ארײנ", "ארונטער", "אריבער",
	"נאכ", "פארבײ", "אהײמ", "אפיר", "פארױס", "פונאנדער", "צוזאמענ", "צונױפ", "צוריק",
	"אױס", "אױפ", "אומ", "אונטער", "איבער", "אײנ", "אנ", "אפ", "בײ", "פאר", "צו",
	"אנט", "בא", "דער", "צע",
}

// Primary among: ordinary inflections, noun endings and unconditional irregulars.
// Longest-match selection is independent of this readable source-order layout.
var yiddishPrimary = [...]yiddishRule{
	{"ערס", yiddishDelete, ""}, {"ענ", yiddishDelete, ""}, {"נס", yiddishDelete, ""},
	{"ענערס", yiddishDelete, ""}, {"עס", yiddishDelete, ""}, {"ע", yiddishDelete, ""},
	{"נ", yiddishDelete, ""}, {"ס", yiddishDelete, ""}, {"עמ", yiddishDelete, ""}, {"ער", yiddishDelete, ""},
	{"יע", yiddishKeep, ""}, {"יעס", yiddishIES, ""},
	{"ענע", yiddishNested, ""}, {"ענעמ", yiddishNested, ""}, {"ענער", yiddishNested, ""}, {"ענס", yiddishNested, ""},
	{"ט", yiddishDelete, ""},
	{"טנ", yiddishTes, ""}, {"טע", yiddishTes, ""}, {"טער", yiddishTes, ""}, {"טס", yiddishTes, ""},
	{"טנס", yiddishTes, ""}, {"טענע", yiddishTes, ""}, {"טענעמ", yiddishTes, ""}, {"טענער", yiddishTes, ""},
	{"עט", yiddishDelete, ""}, {"עטנ", yiddishDelete, ""}, {"עטס", yiddishDelete, ""}, {"עטע", yiddishDelete, ""}, {"עטער", yiddishDelete, ""},
	{"גײנ", yiddishRewrite, "גײ"}, {"גאנגענ", yiddishRewrite, "גײ"}, {"נומענ", yiddishRewrite, "נעמ"},
	{"שריבנ", yiddishRewrite, "שרײב"}, {"GEמיטנ", yiddishRewrite, "מײד"},
	{"GEביטנ", yiddishRewrite, "בײט"}, {"GEביסנ", yiddishRewrite, "בײס"},
	{"װיזנ", yiddishRewrite, "װײז"}, {"טריבנ", yiddishRewrite, "טרײב"}, {"GEליטנ", yiddishRewrite, "לײט"},
	{"קליבנ", yiddishRewrite, "קלײב"}, {"ריבנ", yiddishRewrite, "רײב"}, {"GEריסנ", yiddishRewrite, "רײס"},
	{"שװיגנ", yiddishRewrite, "שװײג"}, {"שמיסנ", yiddishRewrite, "שמײס"}, {"שניטנ", yiddishRewrite, "שנײד"},
	{"בונדנ", yiddishRewrite, "בינד"}, {"װוטשנ", yiddishRewrite, "װיטש"}, {"זונגנ", yiddishRewrite, "זינג"},
	{"טרונקנ", yiddishRewrite, "טרינק"}, {"צװונגנ", yiddishRewrite, "צװינג"}, {"שלונגנ", yiddishRewrite, "שלינג"},
	{"בױגנ", yiddishRewrite, "בײג"}, {"הױבנ", yiddishRewrite, "הײב"}, {"פארלױרנ", yiddishRewrite, "פארליר"},
	{"שטאנענ", yiddishRewrite, "שטײ"}, {"שװױרנ", yiddishRewrite, "שװער"}, {"בראכט", yiddishRewrite, "ברענג"},
	{"ונג", yiddishDelete, ""}, {"הײט", yiddishDelete, ""}, {"קײט", yiddishDelete, ""}, {"יקײט", yiddishDelete, ""}, {"שאפט", yiddishDelete, ""},
	{"יזמ", yiddishDelete, ""}, {"יזמענ", yiddishDelete, ""}, {"ימ", yiddishDelete, ""}, {"ות", yiddishOS, ""},
	{"עלעכ", yiddishDelete, ""}, {"עלע", yiddishDelete, ""}, {"לעכ", yiddishDelete, ""}, {"עלעס", yiddishDelete, ""}, {"עלענ", yiddishDelete, ""},
	{"יסט", yiddishIST, ""}, {"יסטנ", yiddishDelete, ""}, {"סטו", yiddishDelete, ""},
	{"סטער", yiddishDelete, ""}, {"סטע", yiddishDelete, ""}, {"סטנ", yiddishDelete, ""}, {"סט", yiddishDelete, ""},
}

// Nested among after ene/enem/ener/ens. A preceding GE is removed only in the
// final cleanup pass; these are exact suffix rewrites, not another stemmer run.
var yiddishIrregular = [...]yiddishRule{
	{"גאנג", yiddishRewrite, "גײ"}, {"נומ", yiddishRewrite, "נעמ"},
	{"מיט", yiddishRewrite, "מײד"}, {"ביט", yiddishRewrite, "בײט"}, {"ביס", yiddishRewrite, "בײס"},
	{"װיז", yiddishRewrite, "װײז"}, {"טריב", yiddishRewrite, "טרײב"}, {"ליט", yiddishRewrite, "לײט"},
	{"קליב", yiddishRewrite, "קלײב"}, {"ריב", yiddishRewrite, "רײב"}, {"ריס", yiddishRewrite, "רײס"},
	{"שװיג", yiddishRewrite, "שװײג"}, {"שמיס", yiddishRewrite, "שמײס"}, {"שניט", yiddishRewrite, "שנײד"},
	{"שריב", yiddishRewrite, "שרײב"}, {"בונד", yiddishRewrite, "בינד"}, {"װוטש", yiddishRewrite, "װיטש"},
	{"זונג", yiddishRewrite, "זינג"}, {"טרונק", yiddishRewrite, "טרינק"}, {"צװונג", yiddishRewrite, "צװינג"},
	{"שלונג", yiddishRewrite, "שלינג"}, {"בױג", yiddishRewrite, "בײג"}, {"הױב", yiddishRewrite, "הײב"},
	{"פארלױר", yiddishRewrite, "פארליר"}, {"שטאנ", yiddishRewrite, "שטײ"}, {"שװױר", yiddishRewrite, "שװער"},
}

var yiddishSecondary = [...]yiddishRule{
	{"ונג", yiddishDelete, ""}, {"הײט", yiddishDelete, ""}, {"קײט", yiddishDelete, ""},
	{"יקײט", yiddishDelete, ""}, {"שאפט", yiddishDelete, ""}, {"ל", yiddishDelete, ""},
}

var yiddishTertiary = [...]yiddishRule{
	{"יג", yiddishDelete, ""}, {"יק", yiddishDelete, ""}, {"יש", yiddishDelete, ""},
	{"ניק", yiddishDelete, ""}, {"דיק", yiddishDelete, ""}, {"בליק", yiddishKeep, ""}, {"גליק", yiddishKeep, ""},
	{"נדיק", yiddishDelete, ""}, {"ענדיק", yiddishDelete, ""},
}
