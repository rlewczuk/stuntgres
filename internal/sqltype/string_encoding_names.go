package sqltype

// Release18 identities/aliases from PostgreSQL REL_18_0 src/common/encnames.c
// and src/include/mb/pg_wchar.h (Copyright PostgreSQL Global Development Group).
// In particular ID7 is MULE_INTERNAL, not a gap from a later development tree.
var stringEncodingNames = [...]string{
	"SQL_ASCII", "EUC_JP", "EUC_CN", "EUC_KR", "EUC_TW", "EUC_JIS_2004", "UTF8", "MULE_INTERNAL",
	"LATIN1", "LATIN2", "LATIN3", "LATIN4", "LATIN5", "LATIN6", "LATIN7", "LATIN8", "LATIN9", "LATIN10",
	"WIN1256", "WIN1258", "WIN866", "WIN874", "KOI8R", "WIN1251", "WIN1252", "ISO_8859_5", "ISO_8859_6", "ISO_8859_7", "ISO_8859_8",
	"WIN1250", "WIN1253", "WIN1254", "WIN1255", "WIN1257", "KOI8U", "SJIS", "BIG5", "GBK", "UHC", "GB18030", "JOHAB", "SHIFT_JIS_2004",
}

func stringEncodingID(name string) int {
	// Input is the resolved NAME value (at most63 bytes), not original TEXT.
	// C-locale clean_encoding_name retains only ASCII alphanumerics.
	var clean [63]byte
	n := 0
	for i := range len(name) {
		ch := name[i]
		if ch >= 'A' && ch <= 'Z' {
			ch += 'a' - 'A'
		}
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' {
			if n == len(clean) {
				return -1
			}
			clean[n] = ch
			n++
		}
	}
	switch string(clean[:n]) {
	case "sqlascii":
		return 0
	case "eucjp":
		return 1
	case "euccn":
		return 2
	case "euckr":
		return 3
	case "euctw":
		return 4
	case "eucjis2004":
		return 5
	case "unicode", "utf8":
		return 6
	case "muleinternal":
		return 7
	case "iso88591", "latin1":
		return 8
	case "iso88592", "latin2":
		return 9
	case "iso88593", "latin3":
		return 10
	case "iso88594", "latin4":
		return 11
	case "iso88599", "latin5":
		return 12
	case "iso885910", "latin6":
		return 13
	case "iso885913", "latin7":
		return 14
	case "iso885914", "latin8":
		return 15
	case "iso885915", "latin9":
		return 16
	case "iso885916", "latin10":
		return 17
	case "win1256", "windows1256":
		return 18
	case "abc", "tcvn", "tcvn5712", "vscii", "win1258", "windows1258":
		return 19
	case "alt", "win866", "windows866":
		return 20
	case "win874", "windows874":
		return 21
	case "koi8", "koi8r":
		return 22
	case "win", "win1251", "windows1251":
		return 23
	case "win1252", "windows1252":
		return 24
	case "iso88595":
		return 25
	case "iso88596":
		return 26
	case "iso88597":
		return 27
	case "iso88598":
		return 28
	case "win1250", "windows1250":
		return 29
	case "win1253", "windows1253":
		return 30
	case "win1254", "windows1254":
		return 31
	case "win1255", "windows1255":
		return 32
	case "win1257", "windows1257":
		return 33
	case "koi8u":
		return 34
	case "mskanji", "shiftjis", "sjis", "win932", "windows932":
		return 35
	case "big5", "win950", "windows950":
		return 36
	case "gbk", "win936", "windows936":
		return 37
	case "uhc", "win949", "windows949":
		return 38
	case "gb18030":
		return 39
	case "johab":
		return 40
	case "shiftjis2004":
		return 41
	default:
		return -1
	}
}
