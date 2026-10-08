//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

// Source/manual-derived expected values; the oracle establishes observations independently.
type stringCoreReferenceCase struct {
	query string
	value any
	typ   string
}

func stringCoreTrimCases() []stringCoreReferenceCase {
	return []stringCoreReferenceCase{
		{"SELECT btrim('xyxtrimyyx','xyz') AS v", "trim", "TEXT"},
		{"SELECT ltrim('xyxtrimyyx','xyz') AS v", "trimyyx", "TEXT"},
		{"SELECT rtrim('xyxtrimyyx','xyz') AS v", "xyxtrim", "TEXT"},
		{"SELECT btrim('abaXbaa','ab') AS v", "X", "TEXT"},
		{"SELECT btrim('abaXbaa','baba') AS v", "X", "TEXT"},
		{"SELECT btrim('','ab') AS v", "", "TEXT"},
		{"SELECT btrim(' x ','') AS v", " x ", "TEXT"},
		{"SELECT trim(both 'xyz' from 'yxTomxx') AS v", "Tom", "TEXT"},
		{"SELECT trim(both from 'yxTomxx','xyz') AS v", "Tom", "TEXT"},
		{"SELECT trim('yxTomxx','xyz') AS v", "Tom", "TEXT"},
		{"SELECT trim(leading 'xyz' from 'yxTomxx') AS v", "Tomxx", "TEXT"},
		{"SELECT trim(trailing 'xyz' from 'yxTomxx') AS v", "yxTom", "TEXT"},
		{"SELECT trim(from ' x ') AS v", "x", "TEXT"},
		{"SELECT btrim(' \tX\n ') AS v", "\tX\n", "TEXT"},
		{"SELECT btrim(' X ') AS v", " X ", "TEXT"},
		{"SELECT btrim('é🙂X🙂é','é🙂') AS v", "X", "TEXT"},
		{"SELECT btrim('éXé','é') AS v", "éXé", "TEXT"},
		{"SELECT btrim(' x ',' '::char(1)) AS v", " x ", "TEXT"},
		{"SELECT ltrim(' x '::char(4)) AS v", "x", "TEXT"},
		{"SELECT ltrim(' x '::varchar) AS v", "x ", "TEXT"},
		{"SELECT btrim(NULL) AS v", nil, "TEXT"},
		{"SELECT btrim('x',NULL) AS v", nil, "TEXT"},
	}
}
func stringCoreConcatCases() []stringCoreReferenceCase {
	return []stringCoreReferenceCase{
		{"SELECT 'Post'||'greSQL' AS v", "PostgreSQL", "TEXT"},
		{"SELECT 'a '::char(3)||'b '::char(3) AS v", "ab", "TEXT"},
		{"SELECT 'a '::varchar||'b '::varchar AS v", "a b ", "TEXT"},
		{"SELECT 'a'||NULL AS v", nil, "TEXT"},
		{"SELECT NULL||'b' AS v", nil, "TEXT"},
		{"SELECT NULL||NULL AS v", nil, "TEXT"},
		{"SELECT 'x'||(1+2) AS v", "x3", "TEXT"},
		{"SELECT 'a'||'b'||'c' AS v", "abc", "TEXT"},
		{"SELECT 'a' OPERATOR(pg_catalog.||) 'b' AS v", "ab", "TEXT"},
		{"SELECT btrim(' x '||' y ') AS v", "x  y", "TEXT"},
		{"SELECT '['::text||true AS v", "[true", "TEXT"},
		{"SELECT '['::text||false AS v", "[false", "TEXT"},
		{"SELECT '['::text||42::smallint AS v", "[42", "TEXT"},
		{"SELECT '['::text||42::integer AS v", "[42", "TEXT"},
		{"SELECT '['::text||42::bigint AS v", "[42", "TEXT"},
		{"SELECT '['::text||42::oid AS v", "[42", "TEXT"},
		{"SELECT '['::text||1.5::real AS v", "[1.5", "TEXT"},
		{"SELECT '['::text||1.5::double precision AS v", "[1.5", "TEXT"},
		{"SELECT '['::text||12.340::numeric AS v", "[12.340", "TEXT"},
		{"SELECT '['::text||'abc'::name AS v", "[abc", "TEXT"},
		{"SELECT '['::text||'\\x4142'::bytea AS v", "[\\x4142", "TEXT"},
		{"SELECT '['::text||'550e8400-e29b-41d4-a716-446655440000'::uuid AS v", "[550e8400-e29b-41d4-a716-446655440000", "TEXT"},
		{"SELECT '['::text||'2001-02-03'::date AS v", "[2001-02-03", "TEXT"},
		{"SELECT '['::text||'04:05:06'::time AS v", "[04:05:06", "TEXT"},
		{"SELECT '['::text||'04:05:06+00'::timetz AS v", "[04:05:06+00", "TEXT"},
		{"SELECT '['::text||'2001-02-03 04:05:06'::timestamp AS v", "[2001-02-03 04:05:06", "TEXT"},
		{"SELECT '['::text||'2001-02-03 04:05:06+00'::timestamptz AS v", "[2001-02-03 04:05:06+00", "TEXT"},
		{"SELECT '['::text||'1 day 02:03:04'::interval AS v", "[1 day 02:03:04", "TEXT"},
		{"SELECT '['::text||'string_core_seed'::regclass AS v", "[string_core_seed", "TEXT"},
		{"SELECT '['::text||setseed(0) AS v", "[", "TEXT"},
		{"SELECT '['::text||'abc'::text AS v", "[abc", "TEXT"},
		{"SELECT '['::text||'abc '::varchar AS v", "[abc ", "TEXT"},
		{"SELECT '['::text||'abc '::char(5) AS v", "[abc", "TEXT"},
		{"SELECT true||']'::text AS v", "true]", "TEXT"},
		{"SELECT false||']'::text AS v", "false]", "TEXT"},
		{"SELECT 42::smallint||']'::text AS v", "42]", "TEXT"},
		{"SELECT 42::integer||']'::text AS v", "42]", "TEXT"},
		{"SELECT 42::bigint||']'::text AS v", "42]", "TEXT"},
		{"SELECT 42::oid||']'::text AS v", "42]", "TEXT"},
		{"SELECT 1.5::real||']'::text AS v", "1.5]", "TEXT"},
		{"SELECT 1.5::double precision||']'::text AS v", "1.5]", "TEXT"},
		{"SELECT 12.340::numeric||']'::text AS v", "12.340]", "TEXT"},
		{"SELECT 'abc'::name||']'::text AS v", "abc]", "TEXT"},
		{"SELECT '\\x4142'::bytea||']'::text AS v", "\\x4142]", "TEXT"},
		{"SELECT '550e8400-e29b-41d4-a716-446655440000'::uuid||']'::text AS v", "550e8400-e29b-41d4-a716-446655440000]", "TEXT"},
		{"SELECT '2001-02-03'::date||']'::text AS v", "2001-02-03]", "TEXT"},
		{"SELECT '04:05:06'::time||']'::text AS v", "04:05:06]", "TEXT"},
		{"SELECT '04:05:06+00'::timetz||']'::text AS v", "04:05:06+00]", "TEXT"},
		{"SELECT '2001-02-03 04:05:06'::timestamp||']'::text AS v", "2001-02-03 04:05:06]", "TEXT"},
		{"SELECT '2001-02-03 04:05:06+00'::timestamptz||']'::text AS v", "2001-02-03 04:05:06+00]", "TEXT"},
		{"SELECT '1 day 02:03:04'::interval||']'::text AS v", "1 day 02:03:04]", "TEXT"},
		{"SELECT 'string_core_seed'::regclass||']'::text AS v", "string_core_seed]", "TEXT"},
		{"SELECT setseed(0)||']'::text AS v", "]", "TEXT"},
		{"SELECT 'abc'::text||']'::text AS v", "abc]", "TEXT"},
		{"SELECT 'abc '::varchar||']'::text AS v", "abc ]", "TEXT"},
		{"SELECT 'abc '::char(5)||']'::text AS v", "abc]", "TEXT"},
	}
}
func stringCoreNormalizationCases() []stringCoreReferenceCase {
	return []stringCoreReferenceCase{
		{"SELECT normalize('äⓑc',NFC) AS v", "äⓑc", "TEXT"},
		{"SELECT normalize('äⓑc',NFD) AS v", "äⓑc", "TEXT"},
		{"SELECT normalize('äⓑc',NFKC) AS v", "äbc", "TEXT"},
		{"SELECT normalize('äⓑc',NFKD) AS v", "äbc", "TEXT"},
		{"SELECT normalize('é') AS v", "é", "TEXT"},
		{"SELECT normalize('é',NFD) AS v", "é", "TEXT"},
		{"SELECT normalize('Ḍ̇') AS v", "Ḍ̇", "TEXT"},
		{"SELECT normalize('Ḍ̇',NFD) AS v", "Ḍ̇", "TEXT"},
		{"SELECT normalize('Å') AS v", "Å", "TEXT"},
		{"SELECT normalize('Å',NFD) AS v", "Å", "TEXT"},
		{"SELECT normalize('ﬃ') AS v", "ﬃ", "TEXT"},
		{"SELECT normalize('ﬃ',NFKC) AS v", "ffi", "TEXT"},
		{"SELECT normalize(' ',NFKC) AS v", " ", "TEXT"},
		{"SELECT normalize('̈́') AS v", "̈́", "TEXT"},
		{"SELECT normalize('각') AS v", "각", "TEXT"},
		{"SELECT normalize('각',NFD) AS v", "각", "TEXT"},
		{"SELECT normalize('가ᆧ') AS v", "가", "TEXT"},
		{"SELECT normalize('힣',NFD) AS v", "힣", "TEXT"},
		{"SELECT normalize('𝔸',NFKC) AS v", "A", "TEXT"},
		{"SELECT normalize('͸') AS v", "͸", "TEXT"},
		{"SELECT normalize('a '::char(4)) AS v", "a", "TEXT"},
		{"SELECT normalize('a '::varchar) AS v", "a ", "TEXT"},
		{"SELECT normalize('a')||normalize('̈') AS v", "ä", "TEXT"},
		{"SELECT normalize('a'||'̈') AS v", "ä", "TEXT"},
		{"SELECT \"normalize\"('é','nFc') AS v", "é", "TEXT"},
		{"SELECT pg_catalog.normalize('é','NFC') AS v", "é", "TEXT"},
		{"SELECT normalize(NULL) AS v", nil, "TEXT"},
		{"SELECT \"normalize\"(NULL,'bad') AS v", nil, "TEXT"},
		{"SELECT \"normalize\"('x',NULL) AS v", nil, "TEXT"},
		{"SELECT normalize('',NFC) AS v", "", "TEXT"},
		{"SELECT normalize('ASCII🙂',NFC) AS v", "ASCII🙂", "TEXT"},
		{"SELECT normalize('',NFD) AS v", "", "TEXT"},
		{"SELECT normalize('ASCII🙂',NFD) AS v", "ASCII🙂", "TEXT"},
		{"SELECT normalize('',NFKC) AS v", "", "TEXT"},
		{"SELECT normalize('ASCII🙂',NFKC) AS v", "ASCII🙂", "TEXT"},
		{"SELECT normalize('',NFKD) AS v", "", "TEXT"},
		{"SELECT normalize('ASCII🙂',NFKD) AS v", "ASCII🙂", "TEXT"},
		{"SELECT normalize('á́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́',NFD) AS v", "á́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́́", "TEXT"},
		{"SELECT 'é' IS NORMALIZED AS v", false, "BOOL"},
		{"SELECT 'é' IS NFD NORMALIZED AS v", true, "BOOL"},
		{"SELECT 'é' IS NOT NORMALIZED AS v", false, "BOOL"},
		{"SELECT 'é' IS NOT NFC NORMALIZED AS v", true, "BOOL"},
		{"SELECT NULL::text IS NORMALIZED AS v", nil, "BOOL"},
		{"SELECT NULL::text IS NOT NORMALIZED AS v", nil, "BOOL"},
		{"SELECT is_normalized(NULL,'bad') AS v", nil, "BOOL"},
		{"SELECT is_normalized('x',NULL) AS v", nil, "BOOL"},
	}
}
func stringCoreSelect(tc stringCoreReferenceCase, args ...any) referenceStep {
	return referenceSelect(tc.query, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}, args...)
}
func stringCoreReferenceSetup(t *testing.T, p *referencePair, oracle bool) {
	for _, q := range []string{"CREATE TABLE string_core_seed(id INT)", "SET TimeZone='UTC'", "SET DateStyle='ISO, MDY'", "SET IntervalStyle='postgres'", "SET bytea_output='hex'"} {
		if oracle {
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec(q, 0))
		} else {
			p.step(t, referenceExec(q, 0))
		}
	}
}
func stringCoreReferenceValues(t *testing.T, p *referencePair, list []stringCoreReferenceCase, oracle bool) {
	for _, tc := range list {
		t.Run(tc.query, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				s := stringCoreSelect(tc)
				if prepared {
					s = referencePrepared(s)
				}
				if oracle {
					referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
				} else {
					p.step(t, s)
				}
			}
		})
	}
}
func TestReferenceStringCoreTrim(t *testing.T) {
	p := newReferencePair(t)
	stringCoreReferenceValues(t, p, stringCoreTrimCases(), false)
}
func TestReferenceStringCoreConcat(t *testing.T) {
	p := newReferencePair(t)
	stringCoreReferenceSetup(t, p, false)
	stringCoreReferenceChar(t, p, false)
	stringCoreReferenceValues(t, p, stringCoreConcatCases(), false)
	stringCoreReferenceParameters(t, p, false)
	stringCoreReferenceSettings(t, p, false)
	stringCoreReferenceVoid(t, p, false)
	stringCoreReferenceAdditional(t, p, false)
}
func TestReferenceStringCoreNormalization(t *testing.T) {
	p := newReferencePair(t)
	stringCoreReferenceValues(t, p, stringCoreNormalizationCases(), false)
	stringCoreReferenceSyntax(t, p, false)
}
func stringCoreReferenceParameters(t *testing.T, p *referencePair, oracle bool) {
	for _, tc := range []struct {
		q    string
		args []any
		want any
	}{
		{"SELECT $1||$2 AS v", []any{"a ", "b "}, "a b "},
		{"SELECT $1||$2 AS v", []any{nil, "b"}, nil},
		{"SELECT $1||42 AS v", []any{"Value: "}, "Value: 42"},
		{"SELECT 42||$1 AS v", []any{"x"}, "42x"},
		{"SELECT 'x'::text||$1 AS v", []any{"y"}, "xy"},
		{"SELECT $1::char(3)||$2::varchar AS v", []any{"a ", "b "}, "ab "},
		{`SELECT "normalize"($1,$2) AS v`, []any{"é", "NFC"}, "é"},
		{`SELECT "normalize"($1,$2) AS v`, []any{nil, "bad"}, nil},
	} {
		for _, prepared := range []bool{false, true} {
			s := referenceSelect(tc.q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.args...)
			if prepared {
				s = referencePrepared(s)
			}
			if oracle {
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
			} else {
				p.step(t, s)
			}
		}
	}
}

// Held statements and a physical connection prove Stable output is not frozen at Prepare.
func stringCoreReferenceSettings(t *testing.T, p *referencePair, oracle bool) {
	dbs := []*sql.DB{p.memory, p.postgres}
	if oracle {
		dbs = []*sql.DB{p.postgres}
	}
	for _, db := range dbs {
		if db == nil {
			continue
		}
		t.Run("settings", func(t *testing.T) {
			c, err := db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			b, err := c.PrepareContext(p.ctx, `SELECT 'x'::text||$1::bytea AS v`)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			d, err := c.PrepareContext(p.ctx, `SELECT 'x'::text||$1::date AS v`)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			z, err := c.PrepareContext(p.ctx, `SELECT 'x'::text||$1::timestamptz AS v`)
			if err != nil {
				t.Fatal(err)
			}
			defer z.Close()
			i, err := c.PrepareContext(p.ctx, `SELECT 'x'::text||$1::interval AS v`)
			if err != nil {
				t.Fatal(err)
			}
			defer i.Close()
			check := func(stmt *sql.Stmt, arg any, want string) {
				t.Helper()
				var got string
				if err := stmt.QueryRowContext(p.ctx, arg).Scan(&got); err != nil || got != want {
					t.Fatalf("got %q want %q error %v", got, want, err)
				}
			}
			exec := func(q string) {
				t.Helper()
				if _, err := c.ExecContext(p.ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			exec("SET bytea_output='hex'")
			check(b, []byte{0x41}, `x\x41`)
			exec("SET bytea_output='escape'")
			check(b, []byte{0x41}, "xA")
			exec("SET DateStyle='ISO, MDY'")
			check(d, "2001-02-03", "x2001-02-03")
			exec("SET DateStyle='SQL, DMY'")
			check(d, "2001-02-03", "x03/02/2001")
			exec("SET DateStyle='ISO, MDY'")
			exec("SET TimeZone='UTC'")
			check(z, "2001-02-03 04:05:06+00", "x2001-02-03 04:05:06+00")
			exec("SET TimeZone='Europe/Helsinki'")
			check(z, "2001-02-03 04:05:06+00", "x2001-02-03 06:05:06+02")
			exec("SET IntervalStyle='postgres'")
			check(i, "1 day 02:03:04", "x1 day 02:03:04")
			exec("SET IntervalStyle='iso_8601'")
			check(i, "1 day 02:03:04", "xP1DT2H3M4S")
			exec("SET bytea_output='hex'")
			exec("SET TimeZone='UTC'")
			exec("SET IntervalStyle='postgres'")
		})
	}
}
func stringCoreReferenceVoid(t *testing.T, p *referencePair, oracle bool) {
	dbs := []*sql.DB{p.memory, p.postgres}
	if oracle {
		dbs = []*sql.DB{p.postgres}
	}
	for _, db := range dbs {
		if db == nil {
			continue
		}
		t.Run("void_effect", func(t *testing.T) {
			c, err := db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			seed := func() {
				t.Helper()
				if _, err := c.ExecContext(p.ctx, "SELECT setseed(0)"); err != nil {
					t.Fatal(err)
				}
			}
			random := func() float64 {
				t.Helper()
				var v float64
				if err := c.QueryRowContext(p.ctx, "SELECT random()").Scan(&v); err != nil {
					t.Fatal(err)
				}
				return v
			}
			seed()
			first := random()
			for _, q := range []string{"SELECT 'x'::text||setseed(0)", "SELECT setseed(0)||'x'::text"} {
				for _, prepared := range []bool{false, true} {
					random()
					var got string
					if prepared {
						s, e := c.PrepareContext(p.ctx, q)
						if e != nil {
							t.Fatal(e)
						}
						err = s.QueryRowContext(p.ctx).Scan(&got)
						s.Close()
					} else {
						err = c.QueryRowContext(p.ctx, q).Scan(&got)
					}
					if err != nil || got != "x" {
						t.Fatal(got, err)
					}
					if next := random(); next != first {
						t.Fatal("VOID child did not reseed", next, first)
					}
				}
			}
			seed()
			argument := random()
			if _, err := c.ExecContext(p.ctx, "SELECT setseed($1)", argument); err != nil {
				t.Fatal(err)
			}
			wantNext := random()
			for _, q := range []string{"SELECT 'x'::text||setseed(random())", "SELECT setseed(random())||'x'::text"} {
				for _, prepared := range []bool{false, true} {
					seed()
					var got string
					if prepared {
						s, e := c.PrepareContext(p.ctx, q)
						if e != nil {
							t.Fatal(e)
						}
						err = s.QueryRowContext(p.ctx).Scan(&got)
						s.Close()
					} else {
						err = c.QueryRowContext(p.ctx, q).Scan(&got)
					}
					if err != nil || got != "x" {
						t.Fatal(got, err)
					}
					if next := random(); next != wantNext {
						t.Fatal("volatile VOID child did not execute exactly once", next, wantNext)
					}
				}
			}
		})
	}
}
