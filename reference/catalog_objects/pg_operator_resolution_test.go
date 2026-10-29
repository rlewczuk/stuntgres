package reference_test

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"reflect"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func pgOperatorFact(t *testing.T, oid int64) []any {
	t.Helper()
	for _, row := range pgOperatorFacts(t) {
		if row[0] == oid {
			return row
		}
	}
	t.Fatal("missing independent admitted identity", oid)
	return nil
}

func pgOperatorSignature(t *testing.T, p *fixture.Pair, b fixture.Backend, oid int64) {
	t.Helper()
	row := pgOperatorFact(t, oid)
	q := fmt.Sprintf("SELECT oid,oprleft,oprright,oprresult,oprcode::oid AS implementation FROM pg_operator WHERE oid=%d", oid)
	s := fixture.Select(q, []string{"oid", "oprleft", "oprright", "oprresult", "implementation"}, []string{"OID", "OID", "OID", "OID", "OID"}, [][]any{{row[0], row[7], row[8], row[9], row[12]}})
	fixture.PgDescriptionRows(t, p, b, s)
	fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
}

func pgOperatorSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE operator_catalog_seed(n INT)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO operator_catalog_seed VALUES(1)", 1))
}

func pgOperatorRangeResult(t *testing.T, b fixture.Backend, query, logicalType, value string) fixture.Step {
	t.Helper()
	typeName := logicalType
	if b.PG && logicalType == "INT4MULTIRANGE" {
		typeName = ""
	}
	s := fixture.Select(query, []string{"value"}, []string{typeName}, [][]any{{value}})
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value == nil {
			t.Fatal("nonnull range witness returned NULL")
		}
		want := reflect.TypeFor[string]()
		if b.PG {
			want = reflect.TypeFor[[]byte]()
		}
		if reflect.TypeOf(value) != want {
			t.Fatalf("range raw %T, want %v", value, want)
		}
		if bytes, ok := value.([]byte); ok && bytes == nil {
			t.Fatal("typed nil range bytes")
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 {
			t.Fatal(columns, err)
		}
		want := reflect.TypeFor[string]()
		if b.PG {
			want = reflect.TypeFor[any]()
		}
		if columns[0].ScanType() != want || columns[0].DatabaseTypeName() != typeName {
			t.Fatalf("range metadata type=%q scan=%v, want %q/%v", columns[0].DatabaseTypeName(), columns[0].ScanType(), typeName, want)
		}
	}
	return s
}

func pgOperatorExecutionResult(t *testing.T, b fixture.Backend, query, typ, text string) fixture.Step {
	t.Helper()
	if typ == "INT4RANGE" || typ == "INT4MULTIRANGE" {
		return pgOperatorRangeResult(t, b, query, typ, text)
	}
	var value any = text
	var err error
	switch typ {
	case "INT4", "INT8":
		value, err = strconv.ParseInt(text, 10, 64)
	case "FLOAT8":
		value, err = strconv.ParseFloat(text, 64)
	case "BOOL":
		value, err = strconv.ParseBool(text)
	case "TIMESTAMP":
		value, err = time.Parse("2006-01-02 15:04:05", text)
	case "BYTEA":
		if len(text) < 2 || text[:2] != "\\x" {
			t.Fatal("invalid independent bytea witness", text)
		}
		var bytes []byte
		bytes, err = hex.DecodeString(text[2:])
		value = string(bytes)
	}
	if err != nil {
		t.Fatal("invalid independent execution expectation", typ, text, err)
	}
	return fixture.Select(query, []string{"value"}, []string{typ}, [][]any{{value}})
}

func pgOperatorExecutionRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	metadata := s.CheckMetadata
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		if metadata != nil {
			metadata(t, rows)
		}
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != len(s.Types) {
			t.Fatal(columns, err)
		}
		for i, column := range columns {
			if column.Name() != s.Columns[i] || column.DatabaseTypeName() != s.Types[i] {
				t.Fatalf("uncast result column %d name=%q type=%q, want %q/%q", i, column.Name(), column.DatabaseTypeName(), s.Columns[i], s.Types[i])
			}
			var scan reflect.Type
			switch s.Types[i] {
			case "BYTEA":
				scan = reflect.TypeFor[[]byte]()
			case "FLOAT8":
				scan = reflect.TypeFor[float64]()
			case "TIMESTAMP":
				scan = reflect.TypeFor[time.Time]()
			case "BIT", "VARBIT":
				scan = reflect.TypeFor[string]()
			case "JSON", "JSONB", "INET", "_INT2", "_INT4", "_TEXT":
				scan = reflect.TypeFor[string]()
				if b.PG {
					scan = reflect.TypeFor[any]()
				}
			}
			if scan != nil && column.ScanType() != scan {
				t.Fatalf("uncast %s ScanType=%v, want %v", s.Types[i], column.ScanType(), scan)
			}
		}
		fixture.ScalarAssertMetadata(t, b.PG, columns)
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
	statement, err := b.DB.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: statement, SQL: s.Query}
	for range 2 {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
}

func pgOperatorResolverAgreement(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	pgOperatorSeed(t, p, b)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE operator_typed(r1 INT4RANGE,r2 INT4RANGE,m1 INT4MULTIRANGE,m2 INT4MULTIRANGE,b BIT(2),v VARBIT)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO operator_typed VALUES('[1,3)','[3,5)','{[1,3)}','{[3,5)}',B'11',B'01')", 1))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE operator_network(ta TID,tb TID,a INET,b INET,n BIGINT)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO operator_network VALUES('(1,2)','(1,3)','192.0.2.3','0.0.0.255',1)", 1))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE operator_acl(a ACLITEM[],b ACLITEM,c ACLITEM)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO operator_acl VALUES(ARRAY[makeaclitem(0,0,'SELECT,UPDATE',true)],makeaclitem(0,0,'SELECT',true),makeaclitem(0,0,'DELETE',false))", 1))
	for _, tc := range []struct {
		oid                int64
		expression, result string
	}{
		{552, "1::smallint + 2::integer", "3"},
		{692, "1::integer + 2::bigint", "3"},
		{1116, "1::real + 2::double precision", "3"},
		{1099, "DATE '2024-01-03' - DATE '2024-01-01'", "2"},
		{1076, "DATE '2024-01-01' + INTERVAL '1 day'", "2024-01-02 00:00:00"},
		{3825, "'6'::money / '2'::money", "3"},
		{558, "-(2::integer)", "-2"},
		{1209, "'abc'::text LIKE 'a%'::text", "true"},
		{641, "'abc'::text ~ '^a'::text", "true"},
		{654, "'a'::text || 'b'::text", "ab"},
		{2018, "'\\x61'::bytea || '\\x62'::bytea", "\\x6162"},
		{1880, "3::integer & 1::integer", "1"},
		{3963, "'{\"a\":1}'::json ->> 'a'::text", "1"},
		{3477, "'{\"a\":1}'::jsonb ->> 'a'::text", "1"},
		{3247, "'{\"a\":1}'::jsonb ? 'a'::text", "true"},
		{3246, "'{\"a\":1}'::jsonb @> '{\"a\":1}'::jsonb", "true"},
		{3285, "'{\"a\":1}'::jsonb - 'a'::text", "{}"},
		{349, "ARRAY[1,2] || 3", "{1,2,3}"},
		{374, "0 || ARRAY[1,2]", "{0,1,2}"},
		{375, "ARRAY[1] || ARRAY[2]", "{1,2}"},
		{2751, "ARRAY[1,2] @> ARRAY[1]", "true"},
		{3898, "r1 + r2", "[1,5)"},
		{4392, "m1 + m2", "{[1,5)}"},
		{387, "ta = ta", "true"},
		{402, "ta <> tb", "true"},
		{2799, "ta < tb", "true"},
		{2800, "tb > ta", "true"},
		{2801, "ta <= tb", "true"},
		{2802, "tb >= ta", "true"},
		{2634, "~a", "63.255.253.252"},
		{2635, "a & b", "0.0.0.3"},
		{2636, "a | b", "192.0.2.255"},
		{2637, "a + n", "192.0.2.4"},
		{2638, "n + a", "192.0.2.4"},
		{2639, "a - n", "192.0.2.2"},
		{2640, "a - a", "0"},
		{968, "a @> b", "true"},
	} {
		t.Run(fmt.Sprint(tc.oid), func(t *testing.T) {
			pgOperatorSignature(t, p, b, tc.oid)
			resultNames := map[int64]string{16: "BOOL", 17: "BYTEA", 23: "INT4", 20: "INT8", 25: "TEXT", 701: "FLOAT8", 869: "INET", 1114: "TIMESTAMP", 3802: "JSONB", 5078: "_INT4", 3831: "INT4RANGE", 4537: "INT4MULTIRANGE"}
			fact := pgOperatorFact(t, tc.oid)
			source := "operator_catalog_seed"
			if tc.oid == 3898 || tc.oid == 4392 {
				source = "operator_typed"
			}
			if tc.oid == 387 || tc.oid == 402 || tc.oid >= 2634 && tc.oid <= 2640 || tc.oid >= 2799 && tc.oid <= 2802 {
				source = "operator_network"
			}
			if tc.oid == 968 {
				source = "operator_acl"
			}
			query := "SELECT (" + tc.expression + ") AS value FROM " + source
			typ, ok := resultNames[fact[9].(int64)]
			if !ok {
				t.Fatal("missing independently specified concrete witness type", fact[9])
			}
			pgOperatorExecutionRoutes(t, p, b, pgOperatorExecutionResult(t, b, query, typ, tc.result))
		})
	}
	t.Run("acl-inclusion-missing-right", func(t *testing.T) {
		pgOperatorExecutionRoutes(t, p, b, pgOperatorExecutionResult(t, b, "SELECT a @> c AS value FROM operator_acl", "BOOL", "false"))
	})
	q := "SELECT $1::smallint + $2::integer AS value FROM operator_catalog_seed"
	stmt, err := b.DB.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, args := range [][]any{{int64(1), int64(2)}, {int64(4), int64(5)}} {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"value"}, []string{"INT4"}, [][]any{{args[0].(int64) + args[1].(int64)}}, args...))
	}
	// Coercing inputs select real released providers, not synthetic overloads.
	for _, tc := range []struct{ expression, result string }{
		{"5::smallint % 2::integer", "1"},
		{"3::smallint & 1::integer", "1"},
		{"'[10,20]'::jsonb ->> 1::smallint", "20"},
		{"1::oid = 1::integer", "true"},
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT ("+tc.expression+")::text AS value FROM operator_catalog_seed", []string{"value"}, []string{"TEXT"}, [][]any{{tc.result}}))
	}
	bitStep := fixture.Select("SELECT b & v AS value FROM operator_typed", []string{"value"}, []string{"BIT"}, [][]any{{"01"}})
	pgOperatorExecutionRoutes(t, p, b, bitStep)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE operator_columns(a SMALLINT,b INTEGER,c BIGINT,f REAL,g DOUBLE PRECISION,d DATE,m MONEY,n MONEY)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO operator_columns VALUES(1,2,3,1,2,'2024-01-03','6','2')", 1))
	columnQuery := "SELECT a+b AS narrow,b+c AS wide,f+g AS floating,d-DATE '2024-01-01' AS days,m/n AS quotient,-b AS prefix FROM operator_columns WHERE b=$1"
	columnStep := fixture.Select(columnQuery, []string{"narrow", "wide", "floating", "days", "quotient", "prefix"}, []string{"INT4", "INT8", "FLOAT8", "INT4", "FLOAT8", "INT4"}, [][]any{{int64(3), int64(5), float64(3), int64(2), float64(3), int64(-2)}}, int64(2))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, columnStep)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(columnStep))
	columnStmt, err := b.DB.PrepareContext(p.Ctx, columnQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer columnStmt.Close()
	for range 2 {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: columnStmt, SQL: columnQuery}, b.Recorder, columnStep)
	}
}

func TestReferencePGOperatorResolverAgreement(t *testing.T) {
	pgOperatorScenario(t, pgOperatorResolverAgreement)
}

func TestReferencePGOperatorPolymorphicIdentity(t *testing.T) {
	pgOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgOperatorSeed(t, p, b)
		pgOperatorInventory(t, p, b)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TYPE operator_enum AS ENUM('a','b')", 0))
		const customRange = "CREATE TYPE operator_range AS RANGE(subtype=integer)"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(customRange, 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE operator_custom_range(r operator_range)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO operator_custom_range VALUES('[1,3)')", 1))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT r @> 2 AS value FROM operator_custom_range", []string{"value"}, []string{"BOOL"}, [][]any{{true}}))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE operator_polymorphic(e operator_enum,f operator_enum,r INT4RANGE)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO operator_polymorphic VALUES('a','b','[1,3)')", 1))
		for _, tc := range []struct{ expression, typ, want string }{
			{"ARRAY[1] || ARRAY[2]", "_INT4", "{1,2}"},
			{"ARRAY['a'::text] || ARRAY['b'::text]", "_TEXT", "{a,b}"},
			{"e < f", "BOOL", "true"},
			{"r @> 2", "BOOL", "true"},
		} {
			query := "SELECT (" + tc.expression + ") AS value FROM operator_polymorphic"
			pgOperatorExecutionRoutes(t, p, b, pgOperatorExecutionResult(t, b, query, tc.typ, tc.want))
		}
		// Generic array metadata remains admitted despite demand-driven absence
		// of an equality provider for a particular JSON element type.
		s := fixture.ImplicitError("SELECT ARRAY['{}'::json] = ARRAY['{}'::json] FROM operator_catalog_seed", fixture.Diagnostic{Code: "42883", Message: "could not identify an equality operator for type json"})
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		pgOperatorInventory(t, p, b)
	})
}

func TestReferencePGOperatorReferences(t *testing.T) {
	pgOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		q := "SELECT o.oprname,n.nspname,o.oprleft::regtype::text AS left_type,o.oprright::regtype::text AS right_type,o.oprresult::regtype::text AS result_type,o.oid::regoperator::text AS identity,o.oprcode,o.oprrest,o.oprjoin FROM pg_operator o JOIN pg_namespace n ON n.oid=o.oprnamespace WHERE o.oid=551"
		s := fixture.Select(q, []string{"oprname", "nspname", "left_type", "right_type", "result_type", "identity", "oprcode", "oprrest", "oprjoin"}, []string{"NAME", "NAME", "TEXT", "TEXT", "TEXT", "TEXT", "REGPROC", "REGPROC", "REGPROC"}, [][]any{{"+", "pg_catalog", "integer", "integer", "integer", "+(integer,integer)", "int4pl", "-", "-"}})
		fixture.PgDescriptionRows(t, p, b, s)
		fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
		q = "SELECT oprcode,oprrest,oprjoin FROM pg_operator WHERE oid=96"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(q, []string{"oprcode", "oprrest", "oprjoin"}, []string{"REGPROC", "REGPROC", "REGPROC"}, [][]any{{"int4eq", "eqsel", "eqjoinsel"}}))
		aliases, facts := pgOperatorAliasText(t), pgOperatorFacts(t)
		var targets [][]any
		for _, row := range facts {
			commutator, cok := aliases[row[10].(int64)]
			negator, nok := aliases[row[11].(int64)]
			if !cok || !nok {
				t.Fatal("unresolved released operator reference", row)
			}
			targets = append(targets, []any{row[0], commutator, negator})
		}
		q = "SELECT oid,oprcom::regoperator::text AS commutator,oprnegate::regoperator::text AS negator FROM pg_operator"
		if b.PG {
			q += " WHERE " + pgOperatorPredicate(facts)
		}
		s = fixture.Select(q+" ORDER BY oid", []string{"oid", "commutator", "negator"}, []string{"OID", "TEXT", "TEXT"}, targets)
		fixture.PgDescriptionRows(t, p, b, s)
		fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
		// The existing native operator retains its released implementation row.
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT 'int4pl'::regproc::oid AS implementation FROM pg_operator WHERE oid=551", []string{"implementation"}, []string{"OID"}, [][]any{{int64(177)}}))
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT oid FROM pg_proc WHERE oid=177", []string{"oid"}, []string{"OID"}, [][]any{{int64(177)}}))
	})
}

func TestReferencePGOperatorBoundaries(t *testing.T) {
	pgOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgOperatorSeed(t, p, b)
		for _, tc := range []struct {
			oid       int64
			signature string
		}{{517, "<->(point,point)"}, {664, "<(text,text)"}, {2314, "~<~(text,text)"}, {2315, "~<=~(text,text)"}, {2317, "~>=~(text,text)"}, {2318, "~>~(text,text)"}, {2988, "=(record,record)"}, {3636, "@@(tsvector,tsquery)"}} {
			// Executable POINT, TEXT (including pattern), RECORD and FTS
			// controls publish the same released identity on both backends.
			q := fmt.Sprintf("SELECT '%s'::regoperator::oid AS identity FROM operator_catalog_seed", tc.signature)
			fixture.PgDescriptionRows(t, p, b, fixture.Select(q, []string{"identity"}, []string{"OID"}, [][]any{{tc.oid}}))
			q = fmt.Sprintf("SELECT oid FROM pg_operator WHERE oid=%d", tc.oid)
			want := [][]any{{tc.oid}}
			fixture.PgDescriptionRows(t, p, b, fixture.Select(q, []string{"oid"}, []string{"OID"}, want))
		}
		providers := fixture.Select(
			"SELECT o.oid,o.oprcode::oid AS procedure,p.proname,p.prorettype,p.proargtypes::text AS arguments,p.proisstrict,p.provolatile,p.proparallel,p.prolang,p.prosrc FROM pg_operator o JOIN pg_proc p ON p.oid=o.oprcode WHERE o.oid IN (2314,2315,2317,2318) ORDER BY o.oid",
			[]string{"oid", "procedure", "proname", "prorettype", "arguments", "proisstrict", "provolatile", "proparallel", "prolang", "prosrc"},
			[]string{"OID", "OID", "NAME", "OID", "TEXT", "BOOL", "CHAR", "CHAR", "OID", "TEXT"},
			[][]any{
				{int64(2314), int64(2160), "text_pattern_lt", int64(16), "25 25", true, "i", "s", int64(12), "text_pattern_lt"},
				{int64(2315), int64(2161), "text_pattern_le", int64(16), "25 25", true, "i", "s", int64(12), "text_pattern_le"},
				{int64(2317), int64(2163), "text_pattern_ge", int64(16), "25 25", true, "i", "s", int64(12), "text_pattern_ge"},
				{int64(2318), int64(2164), "text_pattern_gt", int64(16), "25 25", true, "i", "s", int64(12), "text_pattern_gt"},
			})
		scalars := fixture.Select(
			"SELECT 'a'::text ~<~ 'aa'::text AS prefix,'a'::text ~<=~ 'a'::text AS equal,'é'::text ~>=~ 'b'::text AS utf8,'a '::text ~>~ 'a'::text AS spaces,text_pattern_lt('A','a') AS case_order,text_pattern_le(NULL::text,'a') AS strict_left,text_pattern_ge('a',NULL::text) AS strict_right,text_pattern_gt('a ','a') AS callable",
			[]string{"prefix", "equal", "utf8", "spaces", "case_order", "strict_left", "strict_right", "callable"},
			[]string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"},
			[][]any{{true, true, true, true, true, nil, nil, true}})
		for _, step := range []fixture.Step{providers, scalars} {
			fixture.PgDescriptionRows(t, p, b, step)
			fixture.PgDescriptionRows(t, p, b, fixture.Prepared(step))
		}
	})
}
