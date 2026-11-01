package functions

// PostgreSQL REL_18_6 / Snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece
// French suffix stages; BSD license in snowball_LICENSE.txt. Longest selection
// precedes action predicates; a failed predicate cannot try a shorter ending.
var frenchStandardSuffixes = [...]string{
	"ance", "iqUe", "isme", "able", "iste", "eux", "ances", "iqUes", "ismes", "ables", "istes",
	"atrice", "ateur", "ation", "atrices", "ateurs", "ations", "logie", "logies",
	"usion", "ution", "usions", "utions", "ence", "ences", "ement", "ements", "ité", "ités",
	"if", "ive", "ifs", "ives", "eaux", "aux", "euse", "euses", "issement", "issements",
	"amment", "emment", "ment", "ments",
}

// A false result may still carry a changed word: amment/emment/ment/ments
// deliberately continue into the verb stages after their successful mutation.
func frenchStandard(w []rune, rv, r1, r2 int) ([]rune, bool) {
	suffix, start := frenchLongest(w, frenchStandardSuffixes[:], 0)
	if suffix == "" {
		return w, false
	}
	switch suffix {
	case "eaux":
		return w[:len(w)-1], true
	case "aux":
		if start >= r1 {
			w[start+1] = 'l'
			return w[:start+2], true
		}
	case "euse", "euses":
		if start >= r2 {
			return w[:start], true
		}
		if start >= r1 {
			w[start+2] = 'x'
			return w[:start+3], true
		}
	case "issement", "issements":
		if start >= r1 && start > 0 && !frenchVowel(w[start-1]) {
			return w[:start], true
		}
	case "amment", "emment":
		if start >= rv {
			w[start], w[start+1], w[start+2] = 'a', 'n', 't'
			if suffix == "emment" {
				w[start] = 'e'
			}
			return w[:start+3], false
		}
	case "ment", "ments":
		if start > rv && frenchVowel(w[start-1]) {
			return w[:start], false
		}
	case "ement", "ements":
		if start < rv {
			break
		}
		w = w[:start]
		secondary, p := frenchLongest(w, []string{"iv", "eus", "abl", "iqU", "ièr", "Ièr"}, 0)
		switch secondary {
		case "iv":
			if p >= r2 {
				w = w[:p]
				if a := frenchSuffix(w, "at"); a >= r2 {
					w = w[:a]
				}
			}
		case "eus":
			if p >= r2 {
				w = w[:p]
			} else if p >= r1 {
				w[p+2] = 'x'
			}
		case "abl", "iqU":
			if p >= r2 {
				w = w[:p]
			}
		case "ièr", "Ièr":
			if p >= rv {
				w[p] = 'i'
				w = w[:p+1]
			}
		}
		return w, true
	default:
		if start < r2 {
			break
		}
		switch suffix {
		case "atrice", "ateur", "ation", "atrices", "ateurs", "ations":
			w = frenchIC(w[:start], r2)
		case "logie", "logies":
			w = w[:start+3]
		case "usion", "ution", "usions", "utions":
			w = w[:start+1]
		case "ence", "ences":
			w[start+2] = 't'
			w = w[:start+3]
		case "ité", "ités":
			w = w[:start]
			secondary, p := frenchLongest(w, []string{"abil", "ic", "iv"}, 0)
			switch secondary {
			case "abil":
				if p >= r2 {
					w = w[:p]
				} else {
					w[p+2] = 'l'
					w = w[:p+3]
				}
			case "ic":
				w = frenchIC(w, r2)
			case "iv":
				if p >= r2 {
					w = w[:p]
				}
			}
		case "if", "ive", "ifs", "ives":
			w = w[:start]
			if p := frenchSuffix(w, "at"); p >= r2 {
				w = frenchIC(w[:p], r2)
			}
		default:
			w = w[:start]
		}
		return w, true
	}
	return w, false
}

func frenchIC(w []rune, r2 int) []rune {
	p := frenchSuffix(w, "ic")
	if p < 0 {
		return w
	}
	if p >= r2 {
		return w[:p]
	}
	// Every caller has already removed a longer suffix, so capacity for the
	// one-character expansion is retained in the same invocation buffer.
	w[p+1] = 'q'
	return append(w, 'U')
}

var frenchIVerbSuffixes = [...]string{
	"îmes", "ît", "îtes", "i", "ie", "ies", "ir", "ira", "irai", "iraIent", "irais", "irait", "iras", "irent", "irez", "iriez",
	"irions", "irons", "iront", "is", "issaIent", "issais", "issait", "issant", "issante", "issantes", "issants", "isse", "issent", "isses",
	"issez", "issiez", "issions", "issons", "it",
}

func frenchIVerb(w []rune, rv int) ([]rune, bool) {
	suffix, p := frenchLongest(w, frenchIVerbSuffixes[:], rv)
	if suffix != "" && p > rv && w[p-1] != 'H' && !frenchVowel(w[p-1]) {
		return w[:p], true
	}
	return w, false
}

var frenchVerbSuffixes = [...]string{
	"ions", "é", "ée", "ées", "és", "èrent", "er", "era", "erai", "eraIent", "erais", "erait", "eras", "erez", "eriez", "erions", "erons", "eront", "ez", "iez",
	"âmes", "ât", "âtes", "a", "ai", "aIent", "ais", "ait", "ant", "ante", "antes", "ants", "as", "asse", "assent", "asses", "assiez", "assions",
}

func frenchVerb(w []rune, rv, r2 int) ([]rune, bool) {
	suffix, p := frenchLongest(w, frenchVerbSuffixes[:], rv)
	if suffix == "" || suffix == "ions" && p < r2 {
		return w, false
	}
	w = w[:p]
	switch suffix {
	case "âmes", "ât", "âtes", "a", "ai", "aIent", "ais", "ait", "ant", "ante", "antes", "ants", "as", "asse", "assent", "asses", "assiez", "assions":
		if p > rv && w[p-1] == 'e' {
			w = w[:p-1]
		}
	}
	return w, true
}

func frenchResidual(w []rune, rv, r2 int) []rune {
	if n := len(w); n > 1 && w[n-1] == 's' {
		keep := false
		switch w[n-2] {
		case 'a', 'i', 'o', 'u', 'è', 's':
			keep = true
		}
		if !keep || n >= 3 && w[n-3] == 'H' && w[n-2] == 'i' {
			w = w[:n-1]
		}
	}
	suffix, p := frenchLongest(w, []string{"ion", "ier", "ière", "Ier", "Ière", "e"}, rv)
	switch suffix {
	case "ion":
		if p >= r2 && p > rv && (w[p-1] == 's' || w[p-1] == 't') {
			return w[:p]
		}
	case "ier", "ière", "Ier", "Ière":
		w[p] = 'i'
		return w[:p+1]
	case "e":
		return w[:p]
	}
	return w
}
