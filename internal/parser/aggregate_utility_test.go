package parser

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unicode/utf8"
)

func TestAggregateLifecycleSignature(t *testing.T) {
	for _, tc := range []struct {
		sig   string
		count int
	}{
		{"*", 0}, {"integer", 1}, {"IN ignored int,VARIADIC args text[]", 2}, {"ORDER BY integer", 1}, {"float8 ORDER BY integer", 2}, {"float8,integer", 2}, {`VARIADIC "any" ORDER BY VARIADIC "any"`, 1},
		{"VARIADIC integer[] ORDER BY VARIADIC pg_catalog.int4[]", 1},
		{"VARIADIC pg_catalog.int4[] ORDER BY VARIADIC integer[]", 1},
		{"VARIADIC int[] ORDER BY VARIADIC pg_catalog.int4[]", 1},
		{"VARIADIC pg_catalog.int4[] ORDER BY VARIADIC int[]", 1},
	} {
		statements, err := Parse("ALTER AGGREGATE public.a(" + tc.sig + ") RENAME TO b")
		if err != nil {
			t.Fatal(tc.sig, err)
		}
		alter := statements[0].(*AlterRoutine)
		if alter.Kind != "aggregate" || alter.Action != "rename" || alter.Target.Text != "b" || !alter.Routine.SignatureSpecified || len(alter.Routine.Arguments) != tc.count {
			t.Fatalf("%s: %#v", tc.sig, alter)
		}
		statements, err = Parse("DROP AGGREGATE IF EXISTS public.a(" + tc.sig + "),b(*) CASCADE")
		if err != nil {
			t.Fatal(tc.sig, err)
		}
		drop := statements[0].(*DropRoutine)
		if drop.Kind != "aggregate" || !drop.IfExists || len(drop.Targets) != 2 || !drop.Targets[0].SignatureSpecified || len(drop.Targets[0].Arguments) != tc.count {
			t.Fatalf("%s: %#v", tc.sig, drop)
		}
	}
	for _, q := range []string{"DROP AGGREGATE a", "DROP AGGREGATE a()", "ALTER AGGREGATE a RENAME TO b", "ALTER AGGREGATE a(*) RENAME TO b junk", "GRANT EXECUTE ON AGGREGATE a(integer) TO PUBLIC", "REVOKE EXECUTE ON AGGREGATE a(integer) FROM PUBLIC"} {
		statements, err := Parse("SELECT 1;" + q)
		if err == nil || len(statements) != 0 {
			t.Fatal("accepted invalid whole input", q, statements, err)
		}
	}
	for _, mode := range []string{"OUT", "INOUT", "IN OUT"} {
		q := "/*é*/ DROP AGGREGATE a(" + mode + " ignored integer)"
		statements, err := Parse(q)
		var d *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "aggregates cannot have output arguments", Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, mode)]) + 1)}
		if len(statements) != 0 || !errors.As(err, &d) || *d != want {
			t.Fatalf("%s: %#v %v want %#v", q, d, err, want)
		}
	}
	for _, pair := range []string{"integer[] ORDER BY VARIADIC bigint[]", "integer[] ORDER BY VARIADIC int4[]"} {
		// Parsed identity, not resolved type-OID equivalence: unqualified
		// int4 differs from the pg_catalog-qualified INTEGER grammar alias.
		q := "DROP AGGREGATE a(VARIADIC " + pair + ")"
		_, err := Parse(q)
		var d *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "an ordered-set aggregate with a VARIADIC direct argument must have one VARIADIC aggregated argument of the same data type", Position: strconv.Itoa(strings.LastIndex(q, "VARIADIC") + 1)}
		if !errors.As(err, &d) || *d != want {
			t.Fatalf("%s: %#v %v want %#v", q, d, err, want)
		}
	}
	// Declaration canonicalization is not PostgreSQL parsed name equality.
	generic := TypeName{Name: "integer", Parts: []Identifier{{Text: "int4"}}}
	keyword := TypeName{Name: "integer"}
	qualified := TypeName{Name: "int4", Schema: &Identifier{Text: "pg_catalog"}, Parts: []Identifier{{Text: "pg_catalog"}, {Text: "int4"}}}
	if aggregateUtilitySameType(generic, keyword) || aggregateUtilitySameType(keyword, generic) || !aggregateUtilitySameType(keyword, qualified) || !aggregateUtilitySameType(qualified, keyword) {
		t.Fatal("generic declaration canonicalization erased parsed qualification")
	}
	for _, q := range []string{"ALTER AGGREGATE a(int4[]) RENAME TO b", "DROP AGGREGATE a(int4[])"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		var target GrantRoutineTarget
		switch s := statements[0].(type) {
		case *AlterRoutine:
			target = s.Routine
		case *DropRoutine:
			target = s.Targets[0]
		}
		if parts := target.Arguments[0].Type.Parts; len(parts) != 1 || parts[0].Text != "int4" {
			t.Fatal("aggregate head discarded original generic type component", q, target)
		}
	}
}
