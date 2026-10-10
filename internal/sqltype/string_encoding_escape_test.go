package sqltype

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestStringEncodingEscapeBoundaries(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{`\0001`, "\x01"}, {`\D7FF`, "\ud7ff"}, {`\E000`, "\ue000"}, {`\+10FFFF`, "\U0010ffff"},
		{`\D800\DC00`, "\U00010000"}, {`\DBFF\DFFF`, "\U0010ffff"},
		{`\uD800\+00DC00`, "\U00010000"}, {`\U0000DBFF\uDFFF`, "\U0010ffff"},
		{`a\0041b\u00e9c\\d`, "aAbéc\\d"}, {`\U00000041F`, "AF"},
	} {
		got, err := stringUnistr(context.Background(), tc.source)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, format := range []string{`\%04X`, `\u%04X`, `\+%06X`, `\U%08X`} {
		source := fmt.Sprintf(format, 0)
		_, err := stringUnistr(context.Background(), source)
		stringEncodingUnitDiagnostic(t, err, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "invalid Unicode code point: 0000"})
	}
	for _, source := range []string{`\DC00`, `\DFFF`, `\D800`, `\D800x`, `\D800é`, `\D800\\`, `\D800\0041`, `\D800\D800`} {
		_, err := stringUnistr(context.Background(), source)
		stringEncodingUnitDiagnostic(t, err, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "invalid Unicode surrogate pair"})
	}
	for _, source := range []string{`\`, `\n`, `\123`, `\uABC`, `\+12345`, `\U1234567`, `\uZZZZ`, `\D800\uZZZZ`} {
		_, err := stringUnistr(context.Background(), source)
		stringEncodingUnitDiagnostic(t, err, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "invalid Unicode escape", Hint: `Unicode escapes must be \XXXX, \+XXXXXX, \uXXXX, or \UXXXXXXXX.`})
	}
	for _, tc := range []struct{ source, code string }{{`\+110000`, "110000"}, {`\UFFFFFFFF`, "FFFFFFFF"}, {`\D800\+110000`, "110000"}, {`\D800\UFFFFFFFF`, "FFFFFFFF"}} {
		_, err := stringUnistr(context.Background(), tc.source)
		stringEncodingUnitDiagnostic(t, err, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "invalid Unicode code point: " + tc.code})
	}
}
