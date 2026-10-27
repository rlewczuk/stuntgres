package pgencoding18

import ("strings"; "testing")

func TestEncodingNames(t *testing.T) {
	canonical := []string{"SQL_ASCII", "EUC_JP", "EUC_CN", "EUC_KR", "EUC_TW", "EUC_JIS_2004", "UTF8", "MULE_INTERNAL", "LATIN1", "LATIN2", "LATIN3", "LATIN4", "LATIN5", "LATIN6", "LATIN7", "LATIN8", "LATIN9", "LATIN10", "WIN1256", "WIN1258", "WIN866", "WIN874", "KOI8R", "WIN1251", "WIN1252", "ISO_8859_5", "ISO_8859_6", "ISO_8859_7", "ISO_8859_8", "WIN1250", "WIN1253", "WIN1254", "WIN1255", "WIN1257", "KOI8U", "SJIS", "BIG5", "GBK", "UHC", "GB18030", "JOHAB", "SHIFT_JIS_2004"}
	if Count != len(canonical) {
		t.Fatal("encoding identity count")
	}
	for id, name := range canonical {
		if Name(id) != name || ID(name) != id {
			t.Fatal("canonical identity", id, name)
		}

	}
	for _, tc := range []struct {
		id      int
		aliases string
	}{
		{0, "sqlascii"}, {1, "eucjp"}, {2, "euccn"}, {3, "euckr"}, {4, "euctw"}, {5, "eucjis2004"}, {6, "unicode utf8"}, {7, "muleinternal"},
		{8, "iso88591 latin1"}, {9, "iso88592 latin2"}, {10, "iso88593 latin3"}, {11, "iso88594 latin4"}, {12, "iso88599 latin5"}, {13, "iso885910 latin6"}, {14, "iso885913 latin7"}, {15, "iso885914 latin8"}, {16, "iso885915 latin9"}, {17, "iso885916 latin10"},
		{18, "win1256 windows1256"}, {19, "abc tcvn tcvn5712 vscii win1258 windows1258"}, {20, "alt win866 windows866"}, {21, "win874 windows874"}, {22, "koi8 koi8r"}, {23, "win win1251 windows1251"}, {24, "win1252 windows1252"},
		{25, "iso88595"}, {26, "iso88596"}, {27, "iso88597"}, {28, "iso88598"}, {29, "win1250 windows1250"}, {30, "win1253 windows1253"}, {31, "win1254 windows1254"}, {32, "win1255 windows1255"}, {33, "win1257 windows1257"}, {34, "koi8u"},
		{35, "mskanji shiftjis sjis win932 windows932"}, {36, "big5 win950 windows950"}, {37, "gbk win936 windows936"}, {38, "uhc win949 windows949"}, {39, "gb18030"}, {40, "johab"}, {41, "shiftjis2004"},
	} {
		for _, alias := range strings.Fields(tc.aliases) {
			for _, name := range []string{alias, strings.ToUpper(alias), "-" + strings.Join(strings.Split(alias, ""), "_") + "!"} {
				if got := ID(name); got != tc.id {
					t.Fatal("alias", name, got, tc.id)
				}
			}
		}
	}
	for _, bad := range []string{"CP1250", "nope", "8", "", "é", "csISOLatin1", "utf16"} {
		if ID(bad) != -1 {
			t.Fatal("invalid alias accepted", bad)
		}
	}
	if ID("UTF8"+strings.Repeat("-",60)) != -1 || Name(-1) != "" || Name(Count) != "" { t.Fatal("raw name or ID bounds") }
}
