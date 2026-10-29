package fixture

import (
	"database/sql"
	"reflect"
	"testing"
)

const TSTemplateSimpleMethods = "INIT=pg_catalog.dsimple_init, LEXIZE=pg_catalog.dsimple_lexize"

func TSTemplateRestrict(t *testing.T, p *Pair, c Executor, r *Recorder, name, dictionary string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := ImplicitError("DROP TEXT SEARCH TEMPLATE "+name+" RESTRICT", Diagnostic{Code: "2BP01", Message: "cannot drop text search template " + name + " because other objects depend on it", Detail: "text search dictionary " + dictionary + " depends on text search template " + name, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		s.Prepared, s.ErrorStage = prepared, "execute"
		RunStep(t, p.Ctx, c, r, s)
	}
}

func TSTemplateLexemes(t *testing.T, p *Pair, c Executor, r *Recorder, pg bool, q string, want any, args ...any) {
	t.Helper()
	s := Select(q, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{want}}, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 {
			t.Fatal(columns, err)
		}
		ct := columns[0]
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		length, lk := ct.Length()
		precision, scale, dk := ct.DecimalSize()
		nullable, nk := ct.Nullable()
		if ct.ScanType() != scan || length != 0 || lk || precision != 0 || scale != 0 || dk || nullable || nk {
			t.Fatal("lexeme metadata", ct.ScanType(), length, lk, precision, scale, dk, nullable, nk)
		}
	}
	s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		if v != nil {
			if pg {
				if _, ok := v.([]byte); !ok {
					t.Fatalf("pq lexemes raw %T", v)
				}
			} else if _, ok := v.(string); !ok {
				t.Fatalf("native lexemes raw %T", v)
			}
		}
		return true
	}
	RunStep(t, p.Ctx, c, r, s)
}
