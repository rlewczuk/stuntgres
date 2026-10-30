package functions

// Whole-token exceptions from the released Estonian 1.3 algorithm, pinned to
// PostgreSQL REL_18_6 / Snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece.
// Copyright and redistribution notice: snowball_LICENSE.txt.
func estonianException(word string) (string, bool) {
	switch word {
	case "joon", "jood", "joob", "joote", "joome", "joovad", "jõin", "jõid", "jõi", "jõime", "jõite", "joomata", "juuakse", "joodakse", "juua", "jooma":
		return "joo", true
	case "saan", "saad", "saab", "saate", "saame", "saavad", "saaksin", "saaksid", "saaks", "saaksite", "saaksime", "sain", "said", "sai", "saite", "saime", "saamata", "saadakse", "saadi", "saama", "saada":
		return "saa", true
	case "viin", "viid", "viib", "viite", "viime", "viivad", "viiksin", "viiksid", "viiks", "viiksite", "viiksime", "viisin", "viisite", "viisime", "viimata", "viiakse", "viidi", "viima", "viia":
		return "viima", true
	case "keen", "keeb", "keed", "kees", "keeme", "keete", "keevad", "keeksin", "keeks", "keeksid", "keeksime", "keeksite", "keemata", "keema", "keeta", "keedakse":
		return "keesi", true
	case "löön", "lööd", "lööb", "lööme", "lööte", "löövad", "lööksin", "lööksid", "lööks", "lööksime", "lööksite", "löömata", "lüüakse", "löödakse", "löödi", "lööma", "lüüa":
		return "löö", true
	case "lõin", "lõid", "lõi", "lõime", "lõite":
		return "lõi", true
	case "loon", "lood", "loob", "loome", "loote", "loovad", "looksin", "looksid", "looks", "looksime", "looksite", "loomata", "luuakse", "loodi", "luua", "looma":
		return "loo", true
	case "käin", "käib", "käid", "käis", "käime", "käite", "käivad", "käiksin", "käiks", "käiksid", "käiksime", "käiksite", "käimata", "käiakse", "käidi", "käia", "käima":
		return "käisi", true
	case "söön", "sööb", "sööd", "sööme", "sööte", "söövad", "sööksin", "sööks", "sööksid", "sööksime", "sööksite", "sõin", "sõi", "sõid", "sõime", "sõite", "söömata", "süüakse", "söödakse", "söödi", "sööma", "süüa":
		return "söö", true
	case "toon", "tood", "toob", "toote", "toome", "toovad", "tooksin", "tooksid", "tooks", "tooksite", "tooksime", "tõin", "tõid", "tõi", "tõime", "tõite", "toomata", "tuuakse", "toodi", "tooma", "tuua":
		return "too", true
	case "võin", "võid", "võib", "võime", "võis", "võite", "võivad", "võiksin", "võiksid", "võiks", "võiksime", "võiksite", "võimata", "võidakse", "võidi", "võida", "võima":
		return "võisi", true
	case "jään", "jääd", "jääb", "jääme", "jääte", "jäävad", "jääksin", "jääksid", "jääks", "jääksime", "jääksite", "jäime", "jäite", "jäin", "jäid", "jäi", "jäämata", "jäädakse", "jääda", "jääma", "jäädi":
		return "jääma", true
	case "müün", "müüd", "müüb", "müüs", "müüme", "müüte", "müüvad", "müüksin", "müüksid", "müüks", "müüksime", "müüksite", "müümata", "müüakse", "müüdi", "müüa", "müüma":
		return "müüsi", true
	case "loeb", "loen", "loed", "loeme", "loete", "loevad", "loeks", "loeksin", "loeksid", "loeksime", "loeksite":
		return "luge", true
	case "põen", "põeb", "põed", "põeme", "põete", "põevad", "põeksin", "põeks", "põeksid", "põeksime", "põeksite":
		return "põde", true
	case "laon", "laob", "laod", "laome", "laote", "laovad", "laoksin", "laoks", "laoksid", "laoksime", "laoksite":
		return "ladu", true
	case "teeksin", "teeks", "teeksid", "teeksime", "teeksite", "teen", "teeb", "teed", "teeme", "teete", "teevad", "tegemata", "tehakse", "tehti", "tegema", "teha":
		return "tegi", true
	case "näen", "näeb", "näed", "näeme", "näete", "näevad", "näeksin", "näeks", "näeksid", "näeksime", "näeksite", "nägemata", "nähakse", "nähti", "näha", "nägema":
		return "nägi", true
	}
	return "", false
}
