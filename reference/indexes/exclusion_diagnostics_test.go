package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceExclusionDiagnostics(t *testing.T) {
	cases := []struct{ name, definition, code, message, detail, hint string }{
		{"gin", "USING gin(v WITH =)", "0A000", `access method "gin" does not support exclusion constraints`, "", ""},
		{"brin", "USING brin(v WITH =)", "0A000", `access method "brin" does not support exclusion constraints`, "", ""},
		{"missing_method", "USING missing(v WITH =)", "42704", `access method "missing" does not exist`, "", ""},
		{"noncommutative", "(v WITH <)", "42809", "operator <(integer,integer) is not commutative", "Only commutative operators can be used in exclusion constraints.", ""},
		{"nonmember", "(v WITH <>)", "42809", `operator <>(integer,integer) is not a member of operator family "integer_ops"`, "The exclusion operator must be related to the index operator class for the constraint.", ""},
		{"canonical_ne", "(v WITH !=)", "42809", `operator <>(integer,integer) is not a member of operator family "integer_ops"`, "The exclusion operator must be related to the index operator class for the constraint.", ""},
		{"missing_operator", "(v WITH &&)", "42883", "operator does not exist: integer && integer", "", "No operator matches the given name and argument types. You might need to add explicit type casts."},
		{"runtime_coercion", "(v WITH ^)", "42883", "operator requires run-time type coercion: integer ^ integer", "", ""},
		{"missing_class", "(v missing_ops WITH =)", "42704", `operator class "missing_ops" does not exist for access method "btree"`, "", ""},
		{"wrong_class", "(v text_ops WITH =)", "42804", `operator class "text_ops" does not accept data type integer`, "", ""},
		{"hash_multikey", "USING hash(v WITH =,w WITH =)", "0A000", `access method "hash" does not support multicolumn indexes`, "", ""},
		{"hash_include", "USING hash(v WITH =) INCLUDE(w)", "0A000", `access method "hash" does not support included columns`, "", ""},
		{"hash_direction", "USING hash(v DESC WITH =)", "0A000", `access method "hash" does not support ASC/DESC options`, "", ""},
		{"hash_nulls", "USING hash(v NULLS FIRST WITH =)", "0A000", `access method "hash" does not support NULLS FIRST/LAST options`, "", ""},
		{"volatile_expression", "((random()) WITH =)", "42P17", "functions in index expression must be marked IMMUTABLE", "", ""},
		{"volatile_predicate", "(v WITH =) WHERE(random()>0)", "42P17", "functions in index predicate must be marked IMMUTABLE", "", ""},
	}
	for _, c := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", c.name, prepared), func(t *testing.T) {
				fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					d := fixture.ControlsReferenceDiagnostic(c.code, c.message)
					d.Detail, d.Hint = c.detail, c.hint
					s := fixture.ErrorDiagnostic("CREATE TABLE t(v INT,w INT,CONSTRAINT ex EXCLUDE "+c.definition+")", d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					// Utility binding is Execute-time; syntax still belongs to Prepare.
					fixture.ExclusionReferenceRun(t, p, b, b.DB, s)
					fixture.ExclusionReferenceRun(t, p, b, b.DB, fixture.Select("SELECT relname FROM pg_class WHERE relname='t'", []string{"relname"}, []string{"NAME"}, nil))
					fixture.ExclusionReferenceRun(t, p, b, b.DB, fixture.Select("SELECT relname FROM pg_class WHERE relname='ex'", []string{"relname"}, []string{"NAME"}, nil))
				})
			})
		}
	}
}

func TestReferenceExclusionOperatorFamilies(t *testing.T) {
	for _, method := range []string{"btree", "hash"} {
		for _, c := range []struct{ typ, class, operator, family string }{
			{"INT", "oid_ops", "=(integer,integer)", "oid_ops"},
			{"TEXT", "bpchar_ops", "=(text,text)", "bpchar_ops"},
		} {
			t.Run(method+"/"+c.class, func(t *testing.T) {
				fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					d := fixture.ControlsReferenceDiagnostic("42809", fmt.Sprintf(`operator %s is not a member of operator family "%s"`, c.operator, c.family))
					d.Detail = "The exclusion operator must be related to the index operator class for the constraint."
					fixture.ExclusionReferenceRun(t, p, b, b.DB, fixture.ErrorDiagnostic("CREATE TABLE t(v "+c.typ+",EXCLUDE USING "+method+"(v "+c.class+" WITH =))", d))
				})
			})
		}
	}
	for _, method := range []string{"btree", "hash"} {
		t.Run("pattern_equality/"+method, func(t *testing.T) {
			fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, b.DB, s) }
				run(fixture.Exec("CREATE TABLE t(v TEXT,CONSTRAINT ex EXCLUDE USING "+method+"(v text_pattern_ops WITH =))", 0))
				run(fixture.Exec("INSERT INTO t VALUES('x')", 1))
				run(fixture.ErrorDiagnostic("INSERT INTO t VALUES('x')", fixture.ExclusionReferenceConflict("t", "ex", "v", "x", "x")))
			})
		})
	}
}

func TestReferenceExclusionPropertyDiagnostics(t *testing.T) {
	for _, c := range []struct{ properties, token, message string }{
		{"NOT DEFERRABLE INITIALLY DEFERRED", "INITIALLY", "constraint declared INITIALLY DEFERRED must be DEFERRABLE"},
		{"INITIALLY DEFERRED NOT DEFERRABLE", "NOT", "constraint declared INITIALLY DEFERRED must be DEFERRABLE"},
		{"DEFERRABLE NOT DEFERRABLE", "NOT", "conflicting constraint properties"},
		{"INITIALLY IMMEDIATE INITIALLY DEFERRED", "INITIALLY DEFERRED", "conflicting constraint properties"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", c.properties, prepared), func(t *testing.T) {
				fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					q := "/*é*/ CREATE TABLE t(v INT,CONSTRAINT \"éx\" EXCLUDE(v WITH =) " + c.properties + ")"
					d := fixture.ControlsReferenceDiagnostic("42601", c.message)
					d.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.LastIndex(q, c.token)]) + 1)
					s := fixture.ErrorDiagnostic(q, d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.ExclusionReferenceRun(t, p, b, b.DB, s)
				})
			})
		}
	}
}

func TestReferenceExclusionMethodBoundaries(t *testing.T) {
	t.Run("spgist_text", func(t *testing.T) {
		fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			q := "CREATE TABLE t(v TEXT,CONSTRAINT ex EXCLUDE USING spgist(v WITH =))"
			run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, b.DB, s) }
			if !b.PG {
				run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "non-btree indexes are unsupported")))
				return
			}
			run(fixture.Exec(q, 0))
			run(fixture.Exec("INSERT INTO t VALUES('a'),('b')", 2))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES('a')", fixture.ExclusionReferenceConflict("t", "ex", "v", "a", "a")))
		})
	})
	t.Run("gist_scalar_method_boundary", func(t *testing.T) {
		fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			q := "CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE USING gist(v WITH =))"
			d := fixture.ControlsReferenceDiagnostic("42704", `data type integer has no default operator class for access method "gist"`)
			d.Hint = "You must specify an operator class for the index or define a default operator class for the data type."
			fixture.ExclusionReferenceRun(t, p, b, b.DB, fixture.ErrorDiagnostic(q, d))
		})
	})
	t.Run("gist_point_provider", func(t *testing.T) {
		fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, b.DB, s) }
			run(fixture.Exec("CREATE TABLE t(v POINT,CONSTRAINT ex EXCLUDE USING gist(v WITH ~=))", 0))
			run(fixture.Exec("INSERT INTO t VALUES('(1,2)'),('(3,4)')", 2))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES('(1,2)')", fixture.ExclusionReferenceConflict("t", "ex", "v", "(1,2)", "(1,2)")))
		})
	})
	t.Run("range_overlap_provider", func(t *testing.T) {
		fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, b.DB, s) }
			run(fixture.Exec("CREATE TABLE t(v INT4RANGE,CONSTRAINT ex EXCLUDE USING gist(v WITH &&))", 0))
			run(fixture.Exec("INSERT INTO t VALUES('[1,3)')", 1))
			run(fixture.Exec("INSERT INTO t VALUES('[2,4)'),('[5,7)') ON CONFLICT DO NOTHING", 1))
			run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES('[2,4)') ON CONFLICT ON CONSTRAINT ex DO NOTHING", 0)))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES('[2,4)') ON CONFLICT(v) DO NOTHING", fixture.ControlsReferenceDiagnostic("42P10", "there is no unique or exclusion constraint matching the ON CONFLICT specification")))
			run(fixture.Select("SELECT count(*) AS n FROM t", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		})
	})
}
