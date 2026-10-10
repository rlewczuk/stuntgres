package reference_test

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"testing"
)

// Independent literal witnesses validated against PostgreSQL18.0. NBSP
// witnesses use ISO/Windows mappings; Asian witnesses use ideographic space
// except JOHAB's Euro. MIC leading bytes follow REL_18_0 pg_wchar.h and
// conversion-procedure wrappers.
type binaryConversionWitness struct{ name, local, utf8 string }

var binaryConversionSpokes = []binaryConversionWitness{
	{"EUC_JP", "a1a1", "e38080"}, {"EUC_CN", "a1a1", "e38080"}, {"EUC_KR", "a1a1", "e38080"}, {"EUC_TW", "a1a1", "e38080"}, {"EUC_JIS_2004", "a1a1", "e38080"},
	{"LATIN1", "a0", "c2a0"}, {"LATIN2", "a0", "c2a0"}, {"LATIN3", "a0", "c2a0"}, {"LATIN4", "a0", "c2a0"}, {"LATIN5", "a0", "c2a0"}, {"LATIN6", "a0", "c2a0"}, {"LATIN7", "a0", "c2a0"}, {"LATIN8", "a0", "c2a0"}, {"LATIN9", "a0", "c2a0"}, {"LATIN10", "a0", "c2a0"},
	{"WIN1256", "c7", "d8a7"}, {"WIN1258", "a0", "c2a0"}, {"WIN866", "a0", "d0b0"}, {"WIN874", "a1", "e0b881"}, {"KOI8R", "c1", "d0b0"}, {"WIN1251", "e0", "d0b0"}, {"WIN1252", "a0", "c2a0"},
	{"ISO_8859_5", "d0", "d0b0"}, {"ISO_8859_6", "a0", "c2a0"}, {"ISO_8859_7", "a0", "c2a0"}, {"ISO_8859_8", "a0", "c2a0"},
	{"WIN1250", "a0", "c2a0"}, {"WIN1253", "e1", "ceb1"}, {"WIN1254", "a0", "c2a0"}, {"WIN1255", "e0", "d790"}, {"WIN1257", "a0", "c2a0"}, {"KOI8U", "c1", "d0b0"},
	{"SJIS", "8140", "e38080"}, {"BIG5", "a140", "e38080"}, {"GBK", "a1a1", "e38080"}, {"UHC", "a1a1", "e38080"}, {"GB18030", "a1a1", "e38080"}, {"JOHAB", "d9e6", "e282ac"}, {"SHIFT_JIS_2004", "8140", "e38080"},
}
var binaryConversionMule = []struct{ name, local, mic string }{
	{"KOI8R", "c1", "8bc1"}, {"ISO_8859_5", "d0", "8bc1"}, {"WIN1251", "e0", "8bc1"}, {"WIN866", "a0", "8bc1"},
	{"EUC_CN", "a1a1", "91a1a1"}, {"EUC_JP", "a1a1", "92a1a1"}, {"SJIS", "8140", "92a1a1"}, {"EUC_KR", "a1a1", "93a1a1"}, {"EUC_TW", "a1a1", "95a1a1"}, {"BIG5", "a140", "95a1a1"},
	{"LATIN2", "a0", "82a0"}, {"WIN1250", "a0", "82a0"}, {"LATIN1", "a0", "81a0"}, {"LATIN3", "a0", "83a0"}, {"LATIN4", "a0", "84a0"},
}

func binaryConversionBytes(t *testing.T, s string) string {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func TestReferenceBinaryConversionPairs(t *testing.T) {
	type pair struct{ source, destination, input, output string }
	var pairs []pair
	for _, w := range binaryConversionSpokes {
		pairs = append(pairs, pair{w.name, "UTF8", w.local, w.utf8}, pair{"UTF8", w.name, w.utf8, w.local})
	}
	for _, w := range binaryConversionMule {
		pairs = append(pairs, pair{w.name, "MULE_INTERNAL", w.local, w.mic}, pair{"MULE_INTERNAL", w.name, w.mic, w.local})
	}
	cyr := []struct{ name, raw string }{{"KOI8R", "c1"}, {"ISO_8859_5", "d0"}, {"WIN1251", "e0"}, {"WIN866", "a0"}}
	for _, s := range cyr {
		for _, d := range cyr {
			if s.name != d.name {
				pairs = append(pairs, pair{s.name, d.name, s.raw, d.raw})
			}
		}
	}
	for _, w := range []pair{{"LATIN2", "WIN1250", "a0", "a0"}, {"EUC_JP", "SJIS", "a1a1", "8140"}, {"EUC_TW", "BIG5", "a1a1", "a140"}, {"EUC_JIS_2004", "SHIFT_JIS_2004", "a1a1", "8140"}} {
		pairs = append(pairs, w, pair{w.destination, w.source, w.output, w.input})
	}
	if len(pairs) != 128 {
		t.Fatalf("pair inventory %d", len(pairs))
	}
	seen := map[string]bool{}
	for _, w := range pairs {
		key := w.source + "/" + w.destination
		if seen[key] {
			t.Fatal("duplicate", key)
		}
		seen[key] = true
	}
	binaryConversionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		q := "SELECT convert($1,$2,$3) AS v"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, w := range pairs {
			t.Run(w.source+"_to_"+w.destination, func(t *testing.T) {
				s := referenceSelect(q, []string{"v"}, []string{"BYTEA"}, [][]any{{binaryConversionBytes(t, w.output)}}, []byte(binaryConversionBytes(t, w.input)), w.source, w.destination)
				s.checkMetadata = binaryConversionMetadata(b.pg, s.columns, s.types)
				referenceRunStep(t, p.ctx, c, b.recorder, s)
				referenceRunStep(t, p.ctx, held, b.recorder, s)
				literal := fmt.Sprintf("SELECT convert('\\x%s'::bytea,'%s','%s') AS v", w.input, w.source, w.destination)
				s.query = literal
				s.args = nil
				referenceRunStep(t, p.ctx, c, b.recorder, s)
			})
		}
	})
}
func TestReferenceBinaryConversionIdentities(t *testing.T) {
	identities := []struct{ name, raw string }{{"SQL_ASCII", "ff"}, {"UTF8", "c3a9"}, {"MULE_INTERNAL", "81a0"}}
	for _, w := range binaryConversionSpokes {
		identities = append(identities, struct{ name, raw string }{w.name, w.local})
	}
	if len(identities) != 42 {
		t.Fatal(len(identities))
	}
	binaryConversionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, w := range identities {
			t.Run(w.name, func(t *testing.T) {
				for _, raw := range []string{binaryConversionBytes(t, w.raw), ""} {
					s := referenceSelect("SELECT convert($1,$2,$2) AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{raw}}, []byte(raw), w.name)
					binaryConversionBoth(t, p, b, c, s)
				}
				d := controlsReferenceDiagnostic("22021", `invalid byte sequence for encoding "`+w.name+`": 0x00`)
				stringEncodingFailure(t, p, b, c, "SELECT convert($1,$2,$2)", d, false, []byte{0}, w.name)
			})
		}
	})
}
