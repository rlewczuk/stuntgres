package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func unaccentDictionaryText(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, want any, args ...any) {
	t.Helper()
	s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{want}}, args...)
	s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		if v != nil {
			if _, ok := v.(string); !ok {
				t.Fatalf("TEXT raw value %T, want string", v)
			}
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		cts, err := rows.ColumnTypes()
		if err != nil || len(cts) != 1 {
			t.Fatal(cts, err)
		}
		ct := cts[0]
		length, lk := ct.Length()
		precision, scale, dk := ct.DecimalSize()
		nullable, nk := ct.Nullable()
		if ct.ScanType() != reflect.TypeFor[string]() || length != 9223372036854775807 || !lk || precision != 0 || scale != 0 || dk || nullable || nk {
			t.Fatalf("computed TEXT metadata: scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t", ct.ScanType(), length, lk, precision, scale, dk, nullable, nk)
		}
	}
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func TestReferenceUnaccentDictionaryValues(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
		for _, q := range []string{"SELECT public.unaccent($1,$2) AS v", "SELECT public.unaccent($1::regdictionary,$2::text) AS v"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct{ input, want string }{
				{"Hôtel", "Hotel"}, {"Æsir Straße Œuf ﬃ", "AEsir Strasse OEuf ffi"},
				{"A\u0300", "A"}, {"1½", "1 1/2"}, {"℃℉", "°C°F"}, {"℗", "(P)"}, {"ℌ", "H"}, {"〝", "\""},
				{"ёлка", "елка"}, {"ЁЖИК", "ЕЖИК"}, {"東京🙂", "東京🙂"}, {"foobar", "foobar"}, {"", ""},
			} {
				unaccentDictionaryText(t, p, b, c, q, tc.want, "public.unaccent", tc.input)
				unaccentDictionaryText(t, p, b, held, q, tc.want, []byte("public.unaccent"), []byte(tc.input))
				unaccentDictionaryText(t, p, b, c, "SELECT public.unaccent($1::text) AS v", tc.want, tc.input)
			}
			for _, args := range [][]any{{nil, "Hôtel"}, {"public.unaccent", nil}, {nil, nil}} {
				unaccentDictionaryText(t, p, b, c, q, nil, args...)
				unaccentDictionaryText(t, p, b, held, q, nil, args...)
			}
			// A retained definition must select the supplied dictionary on every execution.
			unaccentDictionaryText(t, p, b, held, q, "word", "pg_catalog.simple", "WOrD")
			unaccentDictionaryText(t, p, b, held, q, "Hotel", "public.unaccent", "Hôtel")
			stmt.Close()
		}
		unaccentDictionaryText(t, p, b, c, "SELECT public.unaccent('public.unaccent','Hôtel') AS v", "Hotel")
		regtsSelect(t, p, b, c, "SELECT public.unaccent('public.unaccent','Hôtel') AS v WHERE false", []string{"v"}, []string{"TEXT"}, nil)
		regtsExec(t, p, b, c, "PREPARE unaccent_values(regdictionary,text) AS SELECT public.unaccent($1,$2) AS v", 0)
		unaccentDictionaryText(t, p, b, c, "EXECUTE unaccent_values('public.unaccent','Hôtel')", "Hotel")
		unaccentDictionaryText(t, p, b, c, "EXECUTE unaccent_values('pg_catalog.simple','WOrD')", "word")
		regtsExec(t, p, b, c, "DEALLOCATE unaccent_values", 0)
		ddl := "CREATE TABLE unaccent_prepare_effect(v text DEFAULT public.unaccent('public.unaccent'::regdictionary,'Hôtel'))"
		stmt, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_class WHERE relname='unaccent_prepare_effect' AND relnamespace='public'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		regtsExec(t, p, b, c, "INSERT INTO unaccent_prepare_effect DEFAULT VALUES", 1)
		regtsSelect(t, p, b, c, "SELECT v FROM unaccent_prepare_effect", []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}})
	})
}
