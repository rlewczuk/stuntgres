package stuntgres_test

import (
	"database/sql"
	"stuntgres"
	"testing"
)

// Expected Unicode16 and PostgreSQL18 values are specified independently of the engine.
type stringCoreCase struct {
	query string
	value any
	typ   string
}

func stringCoreCheck(t *testing.T, db *sql.DB, tc stringCoreCase, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		var err error
		if prepared {
			stmt, e := db.Prepare(tc.query)
			if e != nil {
				t.Fatal(tc.query, e)
			}
			rows, err = stmt.Query(args...)
			stmt.Close()
		} else {
			rows, err = db.Query(tc.query, args...)
		}
		queryCoreRows(t, rows, err, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}, false)
	}
}

func TestStringCoreTrim(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE string_core_seed(id INT)")
	execSQL(t, db, "SET TimeZone='UTC'")
	execSQL(t, db, "SET DateStyle='ISO, MDY'")
	for _, tc := range []stringCoreCase{
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
	} {
		t.Run(tc.query, func(t *testing.T) { stringCoreCheck(t, db, tc) })
	}
}

func TestStringCoreConcat(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE string_core_seed(id INT)")
	execSQL(t, db, "SET TimeZone='UTC'")
	execSQL(t, db, "SET DateStyle='ISO, MDY'")
	for _, tc := range []stringCoreCase{
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
	} {
		t.Run(tc.query, func(t *testing.T) { stringCoreCheck(t, db, tc) })
	}
}

func TestStringCoreNormalization(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE string_core_seed(id INT)")
	execSQL(t, db, "SET TimeZone='UTC'")
	execSQL(t, db, "SET DateStyle='ISO, MDY'")
	for _, tc := range []stringCoreCase{
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
		{"SELECT normalize('가ᆧ',NFKC) AS v", "가", "TEXT"},
		{"SELECT normalize('가ᆧ',NFD) AS v", "가ᆧ", "TEXT"},
		{"SELECT normalize('가ᆧ',NFKD) AS v", "가ᆧ", "TEXT"},
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
	} {
		t.Run(tc.query, func(t *testing.T) { stringCoreCheck(t, db, tc) })
	}
}

func TestStringCoreNormalizedPredicate(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE string_core_seed(id INT)")
	execSQL(t, db, "SET TimeZone='UTC'")
	execSQL(t, db, "SET DateStyle='ISO, MDY'")
	for _, tc := range []stringCoreCase{
		{"SELECT 'é' IS NORMALIZED AS v", false, "BOOL"},
		{"SELECT 'é' IS NFD NORMALIZED AS v", true, "BOOL"},
		{"SELECT 'é' IS NOT NORMALIZED AS v", false, "BOOL"},
		{"SELECT 'é' IS NOT NFC NORMALIZED AS v", true, "BOOL"},
		{"SELECT NULL::text IS NORMALIZED AS v", nil, "BOOL"},
		{"SELECT NULL::text IS NOT NORMALIZED AS v", nil, "BOOL"},
		{"SELECT is_normalized(NULL,'bad') AS v", nil, "BOOL"},
		{"SELECT is_normalized('x',NULL) AS v", nil, "BOOL"},
		{"SELECT '가ᆧ' IS NFC NORMALIZED AS v", true, "BOOL"},
		{"SELECT '가ᆧ' IS NFKC NORMALIZED AS v", true, "BOOL"},
		{"SELECT '가ᆧ' IS NFC NORMALIZED AS v", false, "BOOL"},
		{"SELECT '가ᆧ' IS NFKC NORMALIZED AS v", false, "BOOL"},
	} {
		t.Run(tc.query, func(t *testing.T) { stringCoreCheck(t, db, tc) })
	}
}

func TestStringCoreParameters(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, tc := range []struct {
		q    string
		args []any
		want any
	}{
		{"SELECT $1||$2 AS v", []any{"a ", "b "}, "a b "},
		{"SELECT $1||42 AS v", []any{"Value: "}, "Value: 42"},
		{"SELECT 42||$1 AS v", []any{"x"}, "42x"},
		{"SELECT $1::char(3)||$2::varchar AS v", []any{"a ", "b "}, "ab "},
		{"SELECT btrim($1,$2) AS v", []any{"é🙂X🙂é", "é🙂"}, "X"},
		{`SELECT "normalize"($1,$2) AS v`, []any{"é", "NFC"}, "é"},
	} {
		stringCoreCheck(t, db, stringCoreCase{tc.q, tc.want, "TEXT"}, tc.args...)
	}
	stmt, err := db.Prepare("SELECT $1||$2 AS v")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, args := range [][]any{{"a", "b"}, {nil, "b"}, {"c", "d"}} {
		var want any
		if args[0] != nil {
			want = args[0].(string) + args[1].(string)
		}
		rows, e := stmt.Query(args...)
		queryCoreRows(t, rows, e, []string{"v"}, []string{"TEXT"}, [][]any{{want}}, false)
	}
}
func TestStringCoreInternalChar(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE string_core_seed(id INT)")
	for _, v := range []any{"A", `\377`, `\200`, `\000`, nil} {
		for _, tc := range []struct{ q, message, position string }{
			{"SELECT '['::text||CASE WHEN false THEN relkind ELSE $1 END AS v FROM pg_catalog.pg_class WHERE relname='string_core_seed'", `operator is not unique: text || "char"`, "17"},
			{"SELECT CASE WHEN false THEN relkind ELSE $1 END||']'::text AS v FROM pg_catalog.pg_class WHERE relname='string_core_seed'", `operator is not unique: "char" || text`, "48"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				want := stuntgres.Error{Code: "42725", Message: tc.message, Position: tc.position, Hint: "Could not choose a best candidate operator. You might need to add explicit type casts."}
				_, err := db.Exec(tc.q, v)
				typeCoreDiagnostic(t, err, want)
				s, err := db.Prepare(tc.q)
				if s != nil {
					s.Close()
					t.Fatal("ambiguous operator exposed statement")
				}
				typeCoreDiagnostic(t, err, want)
			})
		}
		// CHAR/nonstring has a unique string overload, unlike CHAR/TEXT.
		var suffix, forward, reverse any
		if v != nil {
			suffix = v
			if v == `\000` {
				suffix = ""
			}
			forward = suffix.(string) + "42"
			reverse = "42" + suffix.(string)
		}
		stringCoreCheck(t, db, stringCoreCase{"SELECT CASE WHEN false THEN relkind ELSE $1 END||42 AS v FROM pg_catalog.pg_class WHERE relname='string_core_seed'", forward, "TEXT"}, v)
		stringCoreCheck(t, db, stringCoreCase{"SELECT 42||CASE WHEN false THEN relkind ELSE $1 END AS v FROM pg_catalog.pg_class WHERE relname='string_core_seed'", reverse, "TEXT"}, v)
	}
}
