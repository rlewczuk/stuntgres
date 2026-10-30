package functions

// Literal suffixes and actions from PostgreSQL REL_18_6's Catalan Snowball
// algorithm (Snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece).
// See snowball_LICENSE.txt. Region checks follow longest-match selection.
type catalanRule struct {
	suffix      string
	region      int
	replacement string
}

var catalanPronouns = [...]string{
	"'s", "'hi", "'ho", "'l", "'ls", "-ls", "-la", "-les", "-li",
	"vos", "se", "nos", "-nos", "-us", "us", "'n", "'ns", "-n", "-ns",
	"'m", "-me", "-m", "-te", "'t", "li", "lo", "los", "me", "sela",
	"selo", "selas", "selos", "le", "la", "las", "les", "ens", "ho", "hi",
}

var catalanStandardR1 = [...]string{
	"ar", "atge", "formes", "icte", "ictes", "ell", "ells", "ella", "és", "ès",
	"esc", "essa", "et", "ets", "eta", "eres", "eries", "ers", "ina", "ines", "able", "ls",
	"ió", "itat", "itats", "itzar", "iva", "ives", "ivisme", "ius", "fer", "ment", "amen",
	"ament", "aments", "ments", "ot", "sfera", "al", "als", "era", "ana", "iste", "aire",
	"eria", "esa", "eses", "esos", "or", "ícia", "ícies", "icis", "ici", "íci", "ícis",
	"ària", "àries", "alla", "ció", "cions", "nça", "nces", "ó", "dor", "all", "il",
	"ístic", "enc", "enca", "ís", "issa", "issos", "íssem", "íssiu", "issem", "isseu", "ísseu",
	"ós", "osa", "dora", "dores", "dors", "adura", "ble", "bles", "ívol", "ívola", "dís",
	"egar", "ejar", "ificar", "itar", "ables", "adors", "idores", "idors", "adora", "ació",
	"doras", "dur", "dures", "allengües", "ant", "ants", "ancia", "ancies", "atòria", "atòries",
	"tori", "toris", "ats", "ions", "ota", "isam", "ors", "ora", "ores", "isament", "bilitat",
	"bilitats", "ivitat", "ivitats", "ari", "aris", "ionisme", "ionista", "ionistes", "ialista",
	"ialistes", "ialisme", "ialismes", "ud", "uts", "uds", "encia", "encies", "ència", "ències",
	"ïtat", "ïtats", "atiu", "atius", "atives", "ativa", "ativitat", "ativitats", "ible", "ibles",
	"assa", "asses", "assos", "ent", "ents", "íssim", "íssima", "íssims", "íssimes", "ìssem",
	"ìsseu", "ìssin", "ims", "ima", "imes", "isme", "ista", "ismes", "istes", "inia", "inies",
	"íinia", "ínies", "ita", "ites", "triu", "trius", "oses", "osos", "ient", "otes", "ots",
}

var catalanStandardOther = [...]catalanRule{
	{"acions", 2, ""}, {"ada", 2, ""}, {"ades", 2, ""},
	{"logía", 2, "log"}, {"logíes", 2, "log"}, {"logia", 2, "log"}, {"logies", 2, "log"},
	{"logi", 2, "log"}, {"logis", 2, "log"}, {"lógica", 2, "log"}, {"lógics", 2, "log"}, {"lógiques", 2, "log"},
	{"ic", 2, "ic"}, {"ica", 2, "ic"}, {"ics", 2, "ic"}, {"iques", 2, "ic"},
	{"quíssim", 1, "c"}, {"quíssims", 1, "c"}, {"quíssimes", 1, "c"}, {"quíssima", 1, "c"},
}

var catalanVerbR1 = [...]string{
	"ador", "adora", "adors", "adores", "re", "ie", "ent", "ents", "udes", "arà", "eren",
	"ará", "arían", "arías", "arán", "arás", "aríais", "aria", "arian", "arien", "aries", "aràs",
	"aría", "aréis", "aríamos", "aremos", "ara", "aré", "arés", "erían", "erías", "erán", "erás",
	"eríais", "ería", "eréis", "eríamos", "eremos", "erá", "eré", "er", "erau", "erass",
	"irían", "irías", "irán", "irás", "iríais", "iría", "iréis", "iríamos", "iremos", "irá", "iré",
	"írem", "íreu", "íeu", "ia", "ies", "íem", "ìeu", "ien", "at", "ut", "uda", "ava", "aves",
	"avem", "ávem", "àvem", "àveu", "áveu", "aven", "au", "ats", "asseu", "esseu", "eresseu",
	"àsseu", "àssem", "àssim", "àssiu", "essen", "esses", "assen", "asses", "assim", "assiu",
	"éssen", "ésseu", "éssim", "éssiu", "éssem", "í", "ares", "àrem", "àreu", "àren", "aríem",
	"aríeu", "areu", "aren", "ant", "ïm", "ïu", "és", "ïen", "en", "es", "em", "am", "ams",
	"ïa", "ïes", "dre", "eix", "eixer", "tzar", "eixes", "ides", "ïdes", "it", "ït", "ïda",
	"aba", "ada", "ades", "ida", "ía", "iera", "ad", "ed", "its", "id", "ids", "ase", "iese",
	"aste", "iste", "an", "aban", "ían", "aran", "ieran", "asen", "iesen", "aron", "ieron", "ado",
	"ido", "iendo", "ió", "ar", "ir", "as", "ieu", "ii", "io", "ià", "ess", "essin", "essis",
	"ass", "assin", "assis", "essim", "èssim", "èssiu", "abas", "adas", "idas", "ías", "aras",
	"ieras", "ases", "ieses", "ís", "áis", "abais", "íais", "arais", "ierais", "aseis", "ieseis",
	"asteis", "isteis", "ados", "idos", "amos", "ábamos", "íamos", "imos", "ques", "áramos",
	"iéramos", "iésemos", "ásemos", "ira", "iran", "irem", "iren", "ires", "ireu", "iria", "irien",
	"iries", "irà", "iràs", "irè", "irìem", "irìeu", "isquen", "iguem", "igueu", "esqui", "esquin",
	"esquis", "eixi", "eixin", "eixis", "eixen", "eixo", "isin", "isis", "esques", "sis", "sin",
	"int", "iríem", "iríeu", "isc", "atges", "esca", "esquen", "issen", "isses", "issin", "issis",
	"isca", "issiu", "issim", "ïsc", "ïsca", "ïssin", "íssiu", "íssim", "ïssis", "ïguem", "ïgueu",
	"ïra", "ïren", "ïres", "ïsquen", "ïsques", "ïssen", "ïsses", "ïxo", "ïxen", "ïxes", "ïx",
	"ixo", "ixen", "ixes", "ix", "ixa", "inin", "inis", "ini", "ineu", "itza", "itzi", "itzeu", "itzis",
	"itzo", "itz", "itzà", "arem", "in", "às", "iï", "iïn", "iïs",
}

var catalanResidual = [...]string{
	"os", "a", "o", "á", "à", "í", "ó", "e", "é", "eu", "iu", "is", "i", "ir", "s", "ì", "itz", "ï", "ïn", "ïs", "it",
}
