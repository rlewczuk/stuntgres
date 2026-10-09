package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

// Independent expectations: PostgreSQL REL_18_0 builtin case regression data
// and Unicode 16 CaseFolding/SpecialCasing, not host Go Unicode mappings.
func stringCaseScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, backend, conn)
		})
	}
}

func stringCaseBoth(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, s referenceStep) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("%s/prepared=%v", s.query, prepared), func(t *testing.T) {
			step := s
			if prepared {
				step = referencePrepared(step)
			}
			referenceRunStep(t, p.ctx, c, b.recorder, step)
		})
	}
}

func stringCaseSetup(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE sc(id INTEGER,t TEXT,c CHAR(8),v VARCHAR(8),b BYTEA)", 0),
		referenceExec("INSERT INTO sc VALUES(1,'Straße','AbC','AbC  ',NULL),(2,'İΣ','İΣ','İΣ  ',NULL),(3,'','','',NULL),(4,NULL,NULL,NULL,NULL)", 4),
	} {
		referenceRunStep(t, p.ctx, c, b.recorder, s)
	}
}

func TestReferenceStringCaseModes(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		stringCaseSetup(t, p, b, c)
		for _, tc := range []struct {
			coll string
			rows [][]any
		}{
			{"", [][]any{{int64(1), "straße", "STRAßE", "straße", "StraßE"}, {int64(2), "İΣ", "İΣ", "İΣ", "İΣ"}, {int64(3), "", "", "", ""}, {int64(4), nil, nil, nil, nil}}},
			{` COLLATE "C"`, [][]any{{int64(1), "straße", "STRAßE", "straße", "StraßE"}, {int64(2), "İΣ", "İΣ", "İΣ", "İΣ"}, {int64(3), "", "", "", ""}, {int64(4), nil, nil, nil, nil}}},
			{` COLLATE "POSIX"`, [][]any{{int64(1), "straße", "STRAßE", "straße", "StraßE"}, {int64(2), "İΣ", "İΣ", "İΣ", "İΣ"}, {int64(3), "", "", "", ""}, {int64(4), nil, nil, nil, nil}}},
			{" COLLATE pg_catalog.pg_c_utf8", [][]any{{int64(1), "straße", "STRAßE", "straße", "Straße"}, {int64(2), "iσ", "İΣ", "İσ", "İσ"}, {int64(3), "", "", "", ""}, {int64(4), nil, nil, nil, nil}}},
			{" COLLATE pg_catalog.pg_unicode_fast", [][]any{{int64(1), "straße", "STRASSE", "strasse", "Straße"}, {int64(2), "i\u0307ς", "İΣ", "i\u0307σ", "İς"}, {int64(3), "", "", "", ""}, {int64(4), nil, nil, nil, nil}}},
		} {
			q := fmt.Sprintf("SELECT id,lower(t%s) AS lo,upper(t%s) AS up,casefold(t%s) AS fo,initcap(t%s) AS ic FROM sc ORDER BY id", tc.coll, tc.coll, tc.coll, tc.coll)
			stringCaseBoth(t, p, b, c, referenceSelect(q, []string{"id", "lo", "up", "fo", "ic"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT"}, tc.rows))
		}
		stringCaseBoth(t, p, b, c, referenceSelect("SELECT lower(c),upper(v),casefold(c),initcap(v) FROM sc WHERE id=1", []string{"lower", "upper", "casefold", "initcap"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"abc", "ABC  ", "abc", "Abc  "}}))
		stringCaseBoth(t, p, b, c, referenceSelect("SELECT pg_catalog.lower('ABC'),pg_catalog.upper('abc'),pg_catalog.casefold('AbC'),pg_catalog.initcap('hi THERE')", []string{"lower", "upper", "casefold", "initcap"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"abc", "ABC", "abc", "Hi There"}}))
	})
}

func TestReferenceStringCaseUnicode(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ op, input, simple, full string }{
			{"lower", "İ", "i", "i\u0307"}, {"casefold", "İ", "İ", "i\u0307"},
			{"upper", "ß", "ß", "SS"}, {"casefold", "ẞ", "ß", "ss"},
			{"upper", "ﬃ", "ﬃ", "FFI"}, {"casefold", "ﬃ", "ﬃ", "ffi"},
			{"lower", "ȺȺȺ", "ⱥⱥⱥ", "ⱥⱥⱥ"}, {"upper", "ⱥⱥⱥ", "ȺȺȺ", "ȺȺȺ"},
			{"casefold", "Σσς", "σσσ", "σσσ"}, {"casefold", "\uAB70", "\u13A0", "\u13A0"},
			{"casefold", "ǰ", "ǰ", "j\u030C"},
			// Unicode16 assigned Latin capital/lowercase RAMS HORN.
			{"lower", "\uA7CB", "ɤ", "ɤ"}, {"upper", "ɤ", "\uA7CB", "\uA7CB"},
			{"initcap", "42FOO_bar-bAZ O'NEILL", "42foo_Bar-Baz O'Neill", "42foo_Bar-Baz O'Neill"},
			{"initcap", "ǆxxǅ", "Ǆxxǆ", "ǅxxǆ"}, {"initcap", "ßSS", "ßss", "Ssss"},
			{"initcap", "ﬃOO", "ﬃoo", "Ffioo"}, {"initcap", "Λλ 1a １a", "Λλ 1a １A", "Λλ 1a １a"},
			{"initcap", "ȺȺȺ", "Ⱥⱥⱥ", "Ⱥⱥⱥ"}, {"initcap", "a\u0301BC", "A\u0301Bc", "A\u0301Bc"},
			{"initcap", "a\u0345BC", "A\u0345bc", "A\u0345bc"},
			{"lower", "ΑΣ", "ασ", "ας"}, {"lower", "ΑΣΑ", "ασα", "ασα"}, {"lower", "Σ", "σ", "σ"},
			{"casefold", "ΑΣ", "ασ", "ασ"}, {"lower", "Α\u0345Σ\u0345", "α\u0345σ\u0345", "α\u0345ς\u0345"},
			{"lower", "ΑΣ" + strings.Repeat("\u0345", 1024) + "Α", "ασ" + strings.Repeat("\u0345", 1024) + "α", "ασ" + strings.Repeat("\u0345", 1024) + "α"},
		} {
			t.Run(tc.op+"/"+tc.input, func(t *testing.T) {
				q := fmt.Sprintf("SELECT %s($1 COLLATE pg_c_utf8) AS simple,%s($1 COLLATE pg_unicode_fast) AS full", tc.op, tc.op)
				stringCaseBoth(t, p, b, c, referenceSelect(q, []string{"simple", "full"}, []string{"TEXT", "TEXT"}, [][]any{{tc.simple, tc.full}}, tc.input))
			})
		}
		stringCaseBoth(t, p, b, c, referenceSelect(`SELECT initcap('élan ÉCOLE' COLLATE "C") AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{"éLan ÉCole"}}))
		stringCaseBoth(t, p, b, c, referenceSelect("SELECT upper('ß'::varchar(1) COLLATE pg_unicode_fast) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"SS"}}))
	})
}

func TestReferenceStringCaseHeldMetadata(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		stringCaseSetup(t, p, b, c)
		q := "SELECT lower($1) AS lo,upper($1) AS up,casefold($1) AS fo,initcap($1) AS ic,lower($1 COLLATE pg_c_utf8) AS simple,casefold($1 COLLATE pg_c_utf8) AS sf,lower($1 COLLATE pg_unicode_fast) AS full,upper($1 COLLATE pg_unicode_fast) AS fu,casefold($1 COLLATE pg_unicode_fast) AS ff,initcap($1 COLLATE pg_unicode_fast) AS fi FROM sc WHERE id=1"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, tc := range []struct {
			arg any
			row []any
		}{
			{"İ", []any{"İ", "İ", "İ", "İ", "i", "İ", "i\u0307", "İ", "i\u0307", "İ"}},
			{"ß", []any{"ß", "ß", "ß", "ß", "ß", "ß", "ß", "SS", "ss", "Ss"}},
			{"ΑΣ", []any{"ΑΣ", "ΑΣ", "ΑΣ", "ΑΣ", "ασ", "ασ", "ας", "ΑΣ", "ασ", "Ας"}},
			{"", []any{"", "", "", "", "", "", "", "", "", ""}},
			{nil, []any{nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}},
			{"Straße", []any{"straße", "STRAßE", "straße", "StraßE", "straße", "straße", "straße", "STRASSE", "strasse", "Straße"}},
		} {
			referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"lo", "up", "fo", "ic", "simple", "sf", "full", "fu", "ff", "fi"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{tc.row}, tc.arg))
		}
		buf := []byte("AbC")
		rows, err := stmt.QueryContext(p.ctx, buf)
		if err != nil {
			t.Fatal(err)
		}
		copy(buf, "zzz")
		defer rows.Close()
		meta, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal("missing byte-backed row", rows.Err())
		}
		values := make([]any, 10)
		targets := make([]any, 10)
		for i := range targets {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		if referenceNormalize(values[0]) != "abc" {
			t.Fatalf("borrowed caller bytes: %v", values)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		for _, col := range meta {
			if col.DatabaseTypeName() != "TEXT" {
				t.Fatal(col.DatabaseTypeName())
			}
			if !b.pg {
				if col.ScanType() != reflect.TypeOf("") {
					t.Fatal(col.ScanType())
				}
				if n, ok := col.Length(); !ok || n != math.MaxInt64 {
					t.Fatalf("TEXT length %d/%v", n, ok)
				}
				if _, ok := col.Nullable(); ok {
					t.Fatal("expression inherited nullability")
				}
				if _, _, ok := col.DecimalSize(); ok {
					t.Fatal("TEXT decimal size")
				}
			} else {
				// Independently observed on pinned PostgreSQL18.0 via pq.
				if col.ScanType() != reflect.TypeOf("") {
					t.Fatal(col.ScanType())
				}
				if n, ok := col.Length(); !ok || n != math.MaxInt64 {
					t.Fatalf("pq TEXT length %d/%v", n, ok)
				}
				if nullable, known := col.Nullable(); nullable || known {
					t.Fatalf("pq expression nullability %v/%v", nullable, known)
				}
			}
		}
		stringCaseBoth(t, p, b, c, referenceSelect("SELECT lower(t),upper(t),casefold(t),initcap(t) FROM sc WHERE id=99", []string{"lower", "upper", "casefold", "initcap"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, nil))
	})
}
