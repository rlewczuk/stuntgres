package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func ftsInspectionMetadata(t *testing.T, b fixture.Backend, rows *sql.Rows, lateral bool) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, column := range types {
		typ := column.DatabaseTypeName()
		want := reflect.TypeFor[string]()
		switch typ {
		case "INT4":
			want = reflect.TypeFor[int64]()
			if b.PG {
				want = reflect.TypeFor[int32]()
			}
		case "INT8":
			want = reflect.TypeFor[int64]()
		case "REGDICTIONARY", "_REGDICTIONARY", "_TEXT", "RECORD":
			if b.PG {
				want = reflect.TypeFor[any]()
			}
		}
		if column.ScanType() != want {
			t.Fatalf("%s ScanType=%v want %v", typ, column.ScanType(), want)
		}
		wantLength, lengthKnown := int64(0), false
		if typ == "TEXT" {
			wantLength, lengthKnown = math.MaxInt64, true
		}
		if n, ok := column.Length(); n != wantLength || ok != lengthKnown {
			t.Fatalf("%s Length=(%d,%t), want (%d,%t)", typ, n, ok, wantLength, lengthKnown)
		}
		if p, s, ok := column.DecimalSize(); p != 0 || s != 0 || ok {
			t.Fatalf("%s DecimalSize=(%d,%d,%t)", typ, p, s, ok)
		}
		wantNullable := lateral && !b.PG && i > 0
		if nullable, known := column.Nullable(); nullable != wantNullable || known != wantNullable {
			t.Fatalf("%s Nullable=(%t,%t), want (%t,%t)", typ, nullable, known, wantNullable, wantNullable)
		}
	}
}
func ftsInspectionMetadataCases(t *testing.T, cases []struct {
	name, query    string
	columns, types []string
	rows           [][]any
}) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ftsParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := ftsInspectionConnection(t, p, b)
				s := fixture.Select(tc.query, tc.columns, tc.types, tc.rows)
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) { ftsInspectionMetadata(t, b, r, tc.name == "lateral") }
				ftsInspectionBoth(t, p, b, c, s)
				stmt, err := c.PrepareContext(p.Ctx, tc.query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.query}
				for range 2 {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			})
		})
	}
}
func TestReferenceFTSDebugMetadata(t *testing.T) {
	ftsInspectionMetadataCases(t, []struct {
		name, query    string
		columns, types []string
		rows           [][]any
	}{
		{"populated", "SELECT * FROM ts_debug('english','The Rats')", ftsDebugColumns, ftsDebugTypes, ftsDebugEnglish()},
		{"empty", "SELECT * FROM ts_debug('simple','')", ftsDebugColumns, ftsDebugTypes, nil},
		{"null", "SELECT * FROM ts_debug(NULL::regconfig,'word')", ftsDebugColumns, ftsDebugTypes, nil},
		{"null_document", "SELECT * FROM ts_debug('simple',NULL::text)", ftsDebugColumns, ftsDebugTypes, nil},
		{"wherefalse", "SELECT * FROM ts_debug('simple','WORD') WHERE FALSE", ftsDebugColumns, ftsDebugTypes, nil},
		{"partial_alias", "SELECT token,lexemes FROM ts_debug('simple','WORD') AS d(kind)", []string{"token", "lexemes"}, []string{"TEXT", "_TEXT"}, [][]any{{"WORD", "{word}"}}},
		{"ordinality", "SELECT token,ordinality FROM ts_debug('simple','One Two') WITH ORDINALITY ORDER BY ordinality", []string{"token", "ordinality"}, []string{"TEXT", "INT8"}, [][]any{{"One", int64(1)}, {" ", int64(2)}, {"Two", int64(3)}}},
		{"target", "SELECT ts_debug('simple','WORD')", []string{"ts_debug"}, []string{"RECORD"}, [][]any{{`(asciiword,"Word, all ASCII",WORD,{simple},simple,{word})`}}},
		{"rows_from", "SELECT token,word,ndoc FROM ROWS FROM(ts_debug('simple','WORD'),ts_stat('SELECT NULL::tsvector'))", []string{"token", "word", "ndoc"}, []string{"TEXT", "TEXT", "INT4"}, [][]any{{"WORD", nil, nil}}},
		{"lateral", "SELECT v.id,d.token,d.lexemes FROM (VALUES(1,'WORD'),(2,'')) v(id,txt) LEFT JOIN LATERAL ts_debug('simple',v.txt) d ON TRUE ORDER BY v.id", []string{"id", "token", "lexemes"}, []string{"INT4", "TEXT", "_TEXT"}, [][]any{{int64(1), "WORD", "{word}"}, {int64(2), nil, nil}}},
	})
}
func TestReferenceFTSStatMetadata(t *testing.T) {
	ftsInspectionMetadataCases(t, []struct {
		name, query    string
		columns, types []string
		rows           [][]any
	}{
		{"populated", "SELECT * FROM ts_stat('SELECT ''cat:1A,2B''::tsvector')", ftsStatColumns, ftsStatTypes, [][]any{{"cat", int64(1), int64(2)}}},
		{"empty", "SELECT * FROM ts_stat('SELECT ''''::tsvector')", ftsStatColumns, ftsStatTypes, nil},
		{"null", "SELECT * FROM ts_stat(NULL::text)", ftsStatColumns, ftsStatTypes, nil},
		{"wherefalse", "SELECT * FROM ts_stat('SELECT ''cat:1''::tsvector') WHERE FALSE", ftsStatColumns, ftsStatTypes, nil},
		{"partial_alias", "SELECT * FROM ts_stat('SELECT ''cat:1''::tsvector') AS s(lexeme)", []string{"lexeme", "ndoc", "nentry"}, ftsStatTypes, [][]any{{"cat", int64(1), int64(1)}}},
		{"ordinality", "SELECT * FROM ts_stat('SELECT ''cat:1''::tsvector') WITH ORDINALITY AS s(w,d,e,n)", []string{"w", "d", "e", "n"}, []string{"TEXT", "INT4", "INT4", "INT8"}, [][]any{{"cat", int64(1), int64(1), int64(1)}}},
		{"target", "SELECT ts_stat('SELECT ''cat:1''::tsvector')", []string{"ts_stat"}, []string{"RECORD"}, [][]any{{"(cat,1,1)"}}},
		{"rows_from", "SELECT * FROM ROWS FROM(ts_stat('SELECT NULL::tsvector'),ts_stat('SELECT ''cat:1''::tsvector'))", []string{"word", "ndoc", "nentry", "word", "ndoc", "nentry"}, []string{"TEXT", "INT4", "INT4", "TEXT", "INT4", "INT4"}, [][]any{{nil, nil, nil, "cat", int64(1), int64(1)}}},
		{"lateral", "SELECT v.id,s.word,s.ndoc,s.nentry FROM (VALUES(1,'SELECT ''cat:1''::tsvector'),(2,'SELECT NULL::tsvector')) v(id,q) LEFT JOIN LATERAL ts_stat(v.q) s ON TRUE ORDER BY v.id", []string{"id", "word", "ndoc", "nentry"}, []string{"INT4", "TEXT", "INT4", "INT4"}, [][]any{{int64(1), "cat", int64(1), int64(1)}, {int64(2), nil, nil, nil}}},
	})
}
