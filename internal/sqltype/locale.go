package sqltype

// Locale is an immutable, bounded formatting policy. Its zero value is C.
// Categories are captured separately by the statement; no process-global locale
// state, installed libc locales, translations or ICU provider is consulted.
type Locale struct{ data *localeData }

type localeData struct {
	decimal, grouping, positive, negative, currency string
	months                                          *[12]string
	days                                            *[7]string
}

var cLocale = localeData{
	decimal: ".", grouping: ",", positive: "+", negative: "-", currency: " ",
	months: &datetimeMonths, days: &datetimeDays,
}

// ParseCLocale binds empty environment selection deliberately to native C.
// lc_messages excludes explicit empty selection at its settings validator.
func ParseCLocale(name string) (Locale, bool) {
	if name == "C" || name == "POSIX" || name == "" {
		return Locale{&cLocale}, true
	}
	return Locale{}, false
}

func (l Locale) policy() *localeData {
	if l.data == nil {
		return &cLocale
	}
	return l.data
}
