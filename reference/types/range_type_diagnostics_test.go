package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceRangeTypeDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, options, code, message, detail, hint, token string }{
		{"required", `MULTIRANGE_TYPE_NAME=range_failed_many`, "42601", `type attribute "subtype" is required`, "", "", ""},
		{"unknown", `foo=int4`, "42601", `type attribute "foo" not recognized`, "", "", ""},
		{"pseudo", `SUBTYPE=anyelement`, "42804", `range subtype cannot be anyelement`, "", "", ""},
		{"no_default", `SUBTYPE=json`, "42704", `data type json has no default operator class for access method "btree"`, "", `You must specify an operator class for the range type or define a default operator class for the subtype.`, ""},
		{"wrong_class", `SUBTYPE=int4,SUBTYPE_OPCLASS=text_ops`, "42804", `operator class "text_ops" does not accept data type integer`, "", "", ""},
		{"missing_class", `SUBTYPE=int4,SUBTYPE_OPCLASS=range_missing_ops`, "42704", `operator class "range_missing_ops" does not exist for access method "btree"`, "", "", ""},
		{"wrong_method", `SUBTYPE=int4,SUBTYPE_OPCLASS=range_hash_ops`, "42704", `operator class "range_hash_ops" does not exist for access method "btree"`, "", "", ""},
		{"missing_collation", `SUBTYPE=text,COLLATION=range_missing_collation`, "42704", `collation "range_missing_collation" for encoding "UTF8" does not exist`, "", "", ""},
		{"noncollatable", `SUBTYPE=int4,COLLATION="C"`, "42809", `range collation specified but subtype does not support collation`, "", "", ""},
		{"diff_result", `SUBTYPE=int4,SUBTYPE_DIFF=int4eq`, "42P17", `range subtype diff function int4eq(integer, integer) must return type double precision`, "", "", ""},
		{"diff_signature", `SUBTYPE=int4,SUBTYPE_DIFF=abs`, "42883", `function abs(integer, integer) does not exist`, "", "", ""},
		{"volatile_diff", `SUBTYPE=float8,SUBTYPE_DIFF=range_volatile_diff`, "42P17", `range subtype diff function range_volatile_diff(double precision, double precision) must be immutable`, "", "", ""},
		{"duplicate", `SUBTYPE=int4,SUBTYPE=int4`, "42601", `conflicting or redundant options`, "", "", "SUBTYPE=int4)"},
		{"duplicate_class", `SUBTYPE=int4,SUBTYPE_OPCLASS=int4_ops,SUBTYPE_OPCLASS=int4_ops`, "42601", `conflicting or redundant options`, "", "", "SUBTYPE_OPCLASS=int4_ops)"},
		{"duplicate_collation", `SUBTYPE=text,COLLATION="C",COLLATION="C"`, "42601", `conflicting or redundant options`, "", "", "COLLATION=\"C\")"},
		{"duplicate_diff", `SUBTYPE=float8,SUBTYPE_DIFF=float8mi,SUBTYPE_DIFF=float8mi`, "42601", `conflicting or redundant options`, "", "", "SUBTYPE_DIFF=float8mi)"},
		{"duplicate_companion", `SUBTYPE=int4,MULTIRANGE_TYPE_NAME=range_failed_many,MULTIRANGE_TYPE_NAME=range_failed_many`, "42601", `conflicting or redundant options`, "", "", "MULTIRANGE_TYPE_NAME=range_failed_many)"},
		{"subtype_resolution_first", `SUBTYPE=range_missing_type,SUBTYPE=int4`, "42704", `type "range_missing_type" does not exist`, "", "", ""},
		{"class_before_collation", `SUBTYPE=int4,SUBTYPE_OPCLASS=text_ops,COLLATION=range_missing_collation`, "42804", `operator class "text_ops" does not accept data type integer`, "", "", ""},
		{"noncollatable_before_lookup", `SUBTYPE=int4,COLLATION=range_missing_collation`, "42809", `range collation specified but subtype does not support collation`, "", "", ""},
		{"same_companion", `SUBTYPE=int4,MULTIRANGE_TYPE_NAME=range_failed`, "23505", `duplicate key value violates unique constraint "pg_type_typname_nsp_index"`, "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
				if tc.name == "volatile_diff" {
					run(fixture.Exec(`CREATE FUNCTION range_volatile_diff(double precision,double precision) RETURNS double precision LANGUAGE SQL AS $$SELECT $1-$2$$`, 0))
				}
				if tc.name == "wrong_method" {
					run(fixture.Exec(`CREATE OPERATOR CLASS public.range_hash_ops FOR TYPE integer USING hash AS OPERATOR 1 pg_catalog.=, FUNCTION 1 pg_catalog.hashint4(integer)`, 0))
				}
				q := `CREATE TYPE public.range_failed AS RANGE (` + tc.options + `)`
				want := fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail, Hint: tc.hint}
				if tc.name == "same_companion" {
					var namespaceID int64
					if err := conn.QueryRowContext(p.Ctx, `SELECT oid::bigint FROM pg_namespace WHERE nspname='public'`).Scan(&namespaceID); err != nil {
						t.Fatal(err)
					}
					want.Detail = `Key (typname, typnamespace)=(range_failed, ` + strconv.FormatInt(namespaceID, 10) + `) already exists.`
					want.Schema, want.Table, want.Constraint = "pg_catalog", "pg_type", "pg_type_typname_nsp_index"
				}
				if tc.token != "" {
					want.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1)
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, want)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					rangeTypeAbsent(run, "range_failed", "multirange_failed", "range_failed_many", "_range_failed", "_multirange_failed", "_range_failed_many")
				}
				run(fixture.Exec(`CREATE TYPE public.range_failed AS RANGE(SUBTYPE=int4)`, 0))
			})
		})
	}
	for _, mode := range []string{"range", "explicit", "automatic"} {
		t.Run("collision_"+mode, func(t *testing.T) {
			rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
				name := "range_failed"
				if mode == "explicit" {
					name = "occupied"
				}
				if mode == "automatic" {
					name = "multirange_failed"
				}
				run(fixture.Exec(`CREATE TYPE public.`+name+` AS ENUM('sentinel')`, 0))
				q := `CREATE TYPE public.range_failed AS RANGE(SUBTYPE=int4)`
				if mode == "range" {
					q = `CREATE TYPE public.range_failed AS RANGE(foo=int4)`
				}
				if mode == "explicit" {
					q = `CREATE TYPE public.range_failed AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=occupied)`
				}
				want := fixture.Diagnostic{Code: "42710", Message: `type "` + name + `" already exists`}
				if mode == "automatic" {
					want.Detail = `Failed while creating a multirange type for type "range_failed".`
					want.Hint = `You can manually specify a multirange type name using the "multirange_type_name" attribute.`
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, want)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
				}
				run(fixture.Select(`SELECT enumlabel FROM pg_enum WHERE enumtypid=$1::regtype`, []string{"enumlabel"}, []string{"NAME"}, [][]any{{"sentinel"}}, "public."+name))
				if mode != "range" {
					rangeTypeAbsent(run, "range_failed", "_range_failed")
				}
				run(fixture.Exec(`DROP TYPE public.`+name, 0))
				run(fixture.Exec(`CREATE TYPE public.range_failed AS RANGE(SUBTYPE=int4)`, 0))
			})
		})
	}
}

func TestReferenceRangeTypeInvalidSQL(t *testing.T) {
	for _, tc := range []struct{ q, token, message string }{
		{`CREATE TYPE public.range_failed AS RANGE(SUBTYPE=)`, ")", `syntax error at or near ")"`},
		{`CREATE TYPE public.range_failed AS RANGE(SUBTYPE=int4) nonsense`, "nonsense", `syntax error at or near "nonsense"`},
		{`CREATE TYPE public.range_failed AS RANGE(SUBTYPE=int4,)`, ")", `syntax error at or near ")"`},
	} {
		t.Run(tc.message+tc.q, func(t *testing.T) {
			rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
				position := strconv.Itoa(utf8.RuneCountInString(tc.q[:strings.LastIndex(tc.q, tc.token)]) + 1)
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42601", Message: tc.message, Position: position})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
					rangeTypeAbsent(run, "range_failed", "multirange_failed", "_range_failed", "_multirange_failed")
				}
				run(fixture.Exec(`CREATE TYPE public.range_failed AS RANGE(SUBTYPE=int4)`, 0))
			})
		})
	}
}

func TestReferenceRangeTypeAtomicity(t *testing.T) {
	for _, tc := range []struct{ input, code, message, detail string }{
		{"[4,1]", "22000", "range lower bound must be less than or equal to range upper bound", ""},
		{"broken", "22P02", `malformed range literal: "broken"`, `Missing left parenthesis or bracket.`},
		{"[x,3]", "22P02", `invalid input syntax for type integer: "x"`, ""},
		{"[1,2147483648]", "22003", `value "2147483648" is out of range for type integer`, ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
				rangeTypeSetup(run)
				rangeTypeSeed(run)
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(`INSERT INTO range_data VALUES(6,'[1,3]','{}'),(7,$1,'{}')`, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail, Context: `unnamed portal parameter $1 = '...'`}, tc.input)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					run(rangeTypeData(b, rangeTypeRows()))
					q := `INSERT INTO range_data VALUES(6,'[1,3]','{}'),(7,` + rangeLiteral(tc.input) + `,'{}')`
					want := fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail, Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.LastIndex(q, rangeLiteral(tc.input))]) + 1)}
					literal := fixture.ImplicitError(q, want)
					literal.Prepared, literal.ErrorStage = prepared, "execute"
					if prepared {
						literal.ErrorStage = "prepare"
					}
					run(literal)
					run(rangeTypeData(b, rangeTypeRows()))
				}
			})
		})
	}
}
