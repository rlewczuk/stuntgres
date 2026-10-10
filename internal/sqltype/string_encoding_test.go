package sqltype

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestStringEncodingByteTables(t *testing.T) {
	// Independent release constants: raw unit bytes test the whole translation
	// contract without widening SQL TEXT admission to invalid UTF8 or NUL.
	for _, tc := range []struct {
		id, start int
		table     string
	}{
		{8, 160, "  cL Y  \"Ca  -R     'u .,      ?AAAAAAACEEEEIIII NOOOOOxOUUUUYTBaaaaaaaceeeeiiii nooooo/ouuuuyty"},
		{9, 160, " A L LS \"SSTZ-ZZ a,l'ls ,sstz\"zzRAAAALCCCEEEEIIDDNNOOOOxRUUUUYTBraaaalccceeeeiiddnnoooo/ruuuuyt."},
		{16, 160, "  cL YS sCa  -R     Zu .z   EeY?AAAAAAACEEEEIIII NOOOOOxOUUUUYTBaaaaaaaceeeeiiii nooooo/ouuuuyty"},
		{29, 128, "  ' \"    %S<STZZ `'\"\".--  s>stzz   L A  \"CS  -RZ  ,l'u .,as L\"lzRAAAALCCCEEEEIIDDNNOOOOxRUUUUYTBraaaalccceeeeiiddnnoooo/ruuuuyt "},
	} {
		if len(tc.table) != 256-tc.start {
			t.Fatal("independent translation width", tc.id, len(tc.table))
		}
		for code := range 256 {
			want := byte(code)
			if code >= 128 {
				want = ' '
				if code >= tc.start {
					want = tc.table[code-tc.start]
				}
			}
			got, err := stringToASCII(context.Background(), string([]byte{byte(code)}), tc.id)
			if err != nil || got != string([]byte{want}) {
				t.Fatalf("encoding%d byte%02X: %q %v want%q", tc.id, code, got, err, want)
			}
		}
	}
}

func TestStringEncodingNames(t *testing.T) {
	canonical := []string{"SQL_ASCII", "EUC_JP", "EUC_CN", "EUC_KR", "EUC_TW", "EUC_JIS_2004", "UTF8", "MULE_INTERNAL", "LATIN1", "LATIN2", "LATIN3", "LATIN4", "LATIN5", "LATIN6", "LATIN7", "LATIN8", "LATIN9", "LATIN10", "WIN1256", "WIN1258", "WIN866", "WIN874", "KOI8R", "WIN1251", "WIN1252", "ISO_8859_5", "ISO_8859_6", "ISO_8859_7", "ISO_8859_8", "WIN1250", "WIN1253", "WIN1254", "WIN1255", "WIN1257", "KOI8U", "SJIS", "BIG5", "GBK", "UHC", "GB18030", "JOHAB", "SHIFT_JIS_2004"}
	if len(stringEncodingNames) != len(canonical) {
		t.Fatal("encoding identity count")
	}
	for id, name := range canonical {
		if stringEncodingNames[id] != name || stringEncodingID(name) != id {
			t.Fatal("canonical identity", id, name)
		}
		if id != 8 && id != 9 && id != 16 && id != 29 {
			_, err := stringToASCII(context.Background(), "", id)
			d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "encoding conversion from " + name + " to ASCII not supported"}
			stringEncodingUnitDiagnostic(t, err, d)
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
				if got := stringEncodingID(name); got != tc.id {
					t.Fatal("alias", name, got, tc.id)
				}
			}
		}
	}
	for _, bad := range []string{"CP1250", "nope", "8", "", "é", "csISOLatin1", "utf16"} {
		if stringEncodingID(bad) != -1 {
			t.Fatal("invalid alias accepted", bad)
		}
	}
}

func stringEncodingUnitDiagnostic(t *testing.T, err error, want *sqlstate.Error) {
	t.Helper()
	var got *sqlstate.Error
	if !errors.As(err, &got) || *got != *want {
		t.Fatalf("diagnostic %+v (%v), want %+v", got, err, want)
	}
}

func TestStringEncodingPlans(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		result ID
		inputs []ID
		values [4]Value
		flags  [4]bool
		want   any
	}{
		{"ascii", Int4, []ID{Text}, [4]Value{String(types[Text], "é")}, [4]bool{true}, int64(233)},
		{"chr", Text, []ID{Int4}, [4]Value{mustStringEncodingInteger(t, types[Int4], 128578)}, [4]bool{}, "🙂"},
		{"to_ascii", Text, []ID{Text, Name}, [4]Value{String(types[Text], "é"), String(types[Name], "LATIN1")}, [4]bool{true}, "AC"},
		{"to_ascii", Text, []ID{Text, Int4}, [4]Value{String(types[Text], "é"), mustStringEncodingInteger(t, types[Int4], 9)}, [4]bool{true}, "AS"},
		{"unistr", Text, []ID{Text}, [4]Value{String(types[Text], `\D83D\uDE42`)}, [4]bool{true}, "🙂"},
		{"unicode_assigned", Bool, []ID{Text}, [4]Value{String(types[Text], "\U0001fae9")}, [4]bool{true}, true},
	} {
		t.Run(tc.name+fmt.Sprint(tc.inputs), func(t *testing.T) {
			usages := make([]Usage, len(tc.inputs))
			for i, id := range tc.inputs {
				usages[i] = types[id].Usage()
			}
			plan, err := BindString(tc.name, types[tc.result].Usage(), types[Text].Usage(), usages, tc.flags, true)
			if err != nil {
				t.Fatal(err)
			}
			same, err := BindString(tc.name, types[tc.result].Usage(), types[Text].Usage(), usages, tc.flags, true)
			if err != nil || same != plan || plan.SessionDependent() {
				t.Fatal("immutable comparable plan", err)
			}
			got, err := plan.Evaluate(ctx, tc.values, InputEnvironment{}, OutputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			switch want := tc.want.(type) {
			case int64:
				n, e := got.Integer()
				if e != nil || n != want {
					t.Fatal(got, e)
				}
			case string:
				s, e := got.Text()
				if e != nil || s != want {
					t.Fatal(got, e)
				}
			case bool:
				v, e := got.Boolean()
				if e != nil || v != want {
					t.Fatal(got, e)
				}
			}
			for i := range usages {
				nulls := tc.values
				nulls[i] = Null(usages[i].Type)
				got, err := plan.Evaluate(ctx, nulls, InputEnvironment{}, OutputEnvironment{})
				if err != nil || !got.IsNull() || got.Type() != types[tc.result] {
					t.Fatal("strict NULL", got, err)
				}
			}
			foreign := tc.values
			other := mathTypes()[tc.inputs[0]]
			foreign[0] = Null(other)
			if _, err := plan.Evaluate(ctx, foreign, InputEnvironment{}, OutputEnvironment{}); err == nil {
				t.Fatal("foreign owner accepted")
			}
			if plan.ArgumentUsage(0).Type != types[tc.inputs[0]] {
				t.Fatal("formal owner")
			}
		})
	}
	for _, id := range []ID{Text, Int8, Bool} {
		if _, err := BindString("to_ascii", types[Text].Usage(), types[Text].Usage(), []Usage{types[Text].Usage(), types[id].Usage()}, [4]bool{true}, false); err == nil {
			t.Fatal("invalid formal encoding owner accepted", id)
		}
	}
	for _, n := range []int64{-1, 0, 0xD800, 0xDFFF, 0x110000} {
		plan, err := BindString("chr", types[Text].Usage(), types[Text].Usage(), []Usage{types[Int4].Usage()}, [4]bool{}, false)
		if err != nil {
			t.Fatal(err)
		}
		_, err = plan.Evaluate(ctx, [4]Value{mustStringEncodingInteger(t, types[Int4], n)}, InputEnvironment{}, OutputEnvironment{})
		code, message := "54000", "null character not permitted"
		if n < 0 {
			code, message = "22023", "character number must be positive"
		} else if n > 0x10ffff {
			message = fmt.Sprintf("requested character too large for encoding: %d", n)
		} else if n >= 0xd800 {
			message = fmt.Sprintf("requested character not valid for encoding: %d", n)
		}
		stringEncodingUnitDiagnostic(t, err, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message})
	}
}

func mustStringEncodingInteger(t *testing.T, d *Descriptor, n int64) Value {
	t.Helper()
	v, err := Integer(d, n)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestStringEncodingCancellationAndIdentity(t *testing.T) {
	for _, f := range []func(context.Context) (string, error){
		func(ctx context.Context) (string, error) { return stringToASCII(ctx, strings.Repeat("é", 4096), 8) },
		func(ctx context.Context) (string, error) { return stringUnistr(ctx, strings.Repeat(`\0041`, 4096)) },
		func(ctx context.Context) (string, error) { return stringUnistr(ctx, strings.Repeat("A", 32768)) },
	} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if got, err := f(ctx); got != "" || !errors.Is(err, context.Canceled) {
			t.Fatal("precancel", got, err)
		}
		base, stop := context.WithCancel(context.Background())
		probe := &stringTrimCancellationContext{Context: base, cancel: stop, cancelAt: 3}
		got, err := f(probe)
		stop()
		if got != "" || !errors.Is(err, context.Canceled) || probe.calls < 3 {
			t.Fatal("bounded checkpoint", got, err, probe.calls)
		}
	}
	source := strings.Repeat("Aé🙂", 4096)
	ascii := strings.Repeat("ABC", 4096)
	for _, f := range []func() (string, error){func() (string, error) { return stringUnistr(context.Background(), source) }, func() (string, error) { return stringToASCII(context.Background(), ascii, 8) }} {
		if n := testing.AllocsPerRun(10, func() {
			if _, err := f(); err != nil {
				panic(err)
			}
		}); n != 0 {
			t.Fatal("unchanged string allocated", n)
		}
	}
}
