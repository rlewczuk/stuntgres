package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDictIntValues(t *testing.T) {
	dictIntCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		dictIntExec(t, p, b, c, "CREATE EXTENSION dict_int", 0)
		held := dictIntHeld(t, p, c)
		defer held.Statement.Close()
		for _, tc := range []struct {
			name, options string
			values        [][2]string
		}{
			{"defaults", "", [][2]string{{"123456", "{123456}"}, {"1234567", "{123456}"}, {"-123456", "{-12345}"}, {"+123456", "{+12345}"}, {"0000009", "{000000}"}, {"ABCdefghi", "{ABCdef}"}, {"", `{""}`}}},
			{"three", "maxlen=3", [][2]string{{"123456", "{123}"}, {"123", "{123}"}, {"12", "{12}"}, {"-123", "{-12}"}, {"+123", "{+12}"}}},
			{"reject", "maxlen=3,rejectlong=true", [][2]string{{"123456", "{}"}, {"123", "{123}"}, {"-123", "{}"}}},
			{"signed", "maxlen=3,absval=true", [][2]string{{"-123", "{123}"}, {"+123", "{123}"}, {"-123456", "{123}"}, {"-0009", "{000}"}, {"--12", "{-12}"}, {"+", `{""}`}, {"-", `{""}`}}},
			{"signed_reject", "maxlen=3,absval=true,rejectlong=true", [][2]string{{"-123", "{123}"}, {"+123", "{123}"}, {"-1234", "{}"}}},
			{"one", "maxlen=1,absval=false", [][2]string{{"-9", "{-}"}, {"+9", "{+}"}}},
			{"one_abs", "maxlen=1,absval=true", [][2]string{{"-9", "{9}"}, {"+9", "{9}"}}},
			{"fraction", "maxlen=6.7", [][2]string{{"1234567", "{123456}"}}},
			{"bytes", "maxlen=2", [][2]string{{"éé", "{é}"}}},
		} {
			dictionary := "public.intdict"
			if tc.options != "" {
				dictionary = "public.dict_int_" + tc.name
				dictIntExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY "+dictionary+" (TEMPLATE=public.intdict_template,"+tc.options+")", 0)
			}
			for _, value := range tc.values {
				for _, e := range []fixture.Executor{c, held} {
					dictIntLexemes(t, p, b, e, value[1], dictionary, value[0])
				}
			}
		}
		for _, args := range [][2]any{{"public.intdict", nil}, {nil, "1234567"}, {nil, nil}, {[]byte("public.intdict"), []byte("1234567")}, {"public.intdict", []byte{}}} {
			var want any
			if args[0] != nil && args[1] != nil {
				want = "{123456}"
				if input, ok := args[1].([]byte); ok && len(input) == 0 {
					want = `{""}`
				}
			}
			for _, e := range []fixture.Executor{c, held} {
				dictIntLexemes(t, p, b, e, want, args[0], args[1])
			}
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT ts_lexize('public.intdict','123') AS lexemes WHERE false", []string{"lexemes"}, []string{"_TEXT"}, nil)
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				ct, err := rows.ColumnTypes()
				if err != nil || len(ct) != 1 {
					t.Fatal(ct, err)
				}
				scan := reflect.TypeFor[string]()
				if b.PG {
					scan = reflect.TypeFor[any]()
				}
				length, lk := ct[0].Length()
				precision, scale, dk := ct[0].DecimalSize()
				nullable, nk := ct[0].Nullable()
				if ct[0].ScanType() != scan || length != 0 || lk || precision != 0 || scale != 0 || dk || nullable || nk {
					t.Fatal("empty-result lexeme metadata", ct[0], length, lk, precision, scale, dk, nullable, nk)
				}
			}
			dictIntRun(t, p, b, c, s)
		}
		// This same held parameter consumer must acquire current dictionary
		// settings rather than reuse the earlier execution's result.
		dictIntExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.intdict (maxlen=3)", 0)
		dictIntLexemes(t, p, b, held, "{123}", "public.intdict", "123456")
		dictIntExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.intdict (rejectlong=true)", 0)
		dictIntLexemes(t, p, b, held, "{}", "public.intdict", "123456")
		dictIntExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.intdict (maxlen,rejectlong)", 0)
		dictIntOptionText(t, p, b, c, "public.intdict", nil)
		dictIntLexemes(t, p, b, held, "{123456}", "public.intdict", "1234567")
	})
}
