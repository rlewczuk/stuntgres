package expr

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type testNode struct{ Info }

func (testNode) Eval(Context) (sqltype.Value, error) { return sqltype.Value{}, nil }

type testRoutine uint32

func (r testRoutine) RoutineOID() uint32       { return uint32(r) }
func (r testRoutine) RoutineRecipeOID() uint32 { return uint32(r) }

func TestMergeProperties(t *testing.T) {
	row := Properties{Volatility: Stable, RowDependent: true}
	param := Properties{Volatility: Volatile, ParameterDependent: true, DomainDependent: true}
	want := Properties{Volatility: Volatile, RowDependent: true, ParameterDependent: true, DomainDependent: true}
	if got := MergeProperties(row, param); got != want {
		t.Fatalf("MergeProperties(row, param) = %+v, want %+v", got, want)
	}
	if got := MergeProperties(param, row); got != want {
		t.Fatalf("MergeProperties(param, row) = %+v, want %+v", got, want)
	}
	stage := Properties{StageDependent: true, RuntimeDomainConversion: true}
	if got := MergeProperties(Properties{}, stage); got != stage {
		t.Fatalf("MergeProperties(zero, stage) = %+v, want %+v", got, stage)
	}
}

func TestIntrinsicPropertyRules(t *testing.T) {
	for _, tc := range []struct {
		rule       IntrinsicPropertyRule
		volatility Volatility
		dependent  bool // whether a dependent call still matches
	}{
		{ExactImmutable, Immutable, false},
		{ExactStable, Stable, false},
		{ExactVolatile, Volatile, false},
		{VolatileOnly, Volatile, true},
	} {
		base := IntrinsicProperties(tc.rule)
		if base != (Properties{Volatility: tc.volatility}) {
			t.Fatalf("IntrinsicProperties(%d) = %+v", tc.rule, base)
		}
		if !PropertiesMatch(base, tc.rule) {
			t.Fatalf("rule %d rejects its own properties", tc.rule)
		}
		dependent := base
		dependent.ParameterDependent = true
		if PropertiesMatch(dependent, tc.rule) != tc.dependent {
			t.Fatalf("rule %d dependent match = %v", tc.rule, !tc.dependent)
		}
	}
	if PropertiesMatch(Properties{Volatility: Stable}, ExactImmutable) || PropertiesMatch(Properties{Volatility: Stable}, VolatileOnly) {
		t.Fatal("rule accepted another volatility")
	}
	unknown := IntrinsicPropertyRule(255)
	if IntrinsicProperties(unknown) != (Properties{}) || PropertiesMatch(Properties{}, unknown) {
		t.Fatal("unknown rule is not inert")
	}
}

func TestInfoOf(t *testing.T) {
	text := sqltype.Builtins()[sqltype.Text].Usage()
	source := Info{
		Usage: text, Pos: parser.Position{Character: 7}, Property: Properties{Volatility: Stable, RowDependent: true},
		UnknownNull: true, RegclassRef: ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 42}},
		CollationConflict: true, CollationExplicit: parser.Position{Character: 3},
		CollationLeft: sqltype.CollationRef{ID: 1}, CollationRight: sqltype.CollationRef{ID: 2}, CollationConflictPos: parser.Position{Character: 5},
		RoutineReference: testRoutine(10), RoutineRecipeReference: testRoutine(11),
	}
	got := InfoOf(testNode{source})
	want := source
	want.UnknownNull, want.RegclassRef = false, ids.ObjectRef{}
	if got != want {
		t.Fatalf("InfoOf = %+v, want %+v", got, want)
	}
	left, right, pos := got.ImplicitCollationConflict()
	if got.ResultUsage() != text || got.SourcePosition() != source.Pos || got.Properties() != source.Property ||
		!got.CollationIndeterminate() || got.ExplicitCollation() != source.CollationExplicit ||
		left != source.CollationLeft || right != source.CollationRight || pos != source.CollationConflictPos ||
		got.CallRoutineReference() != testRoutine(10) || got.CallRoutineRecipeReference() != testRoutine(11) {
		t.Fatalf("Info accessors disagree with fields: %+v", got)
	}
}

type plainNode struct{ Info }

func (n plainNode) CallRoutineReference() {}
func (plainNode) Eval(Context) (sqltype.Value, error) { return sqltype.Value{}, nil }

func TestInfoOfWithoutRoutineMethods(t *testing.T) {
	// A wrapper whose CallRoutineReference has another signature exposes no
	// routine reference; the recipe still comes from the embedded Info.
	node := plainNode{Info{RoutineReference: testRoutine(1), RoutineRecipeReference: testRoutine(2)}}
	got := InfoOf(node)
	if got.RoutineReference != nil || got.RoutineRecipeReference != testRoutine(2) {
		t.Fatalf("InfoOf = %+v", got)
	}
}

func TestErrorAt(t *testing.T) {
	var diagnostic *sqlstate.Error
	err := ErrorAt("42P02", "there is no parameter $0", parser.Position{Character: 8})
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42P02" || diagnostic.Message != "there is no parameter $0" ||
		diagnostic.Severity != "ERROR" || diagnostic.SeverityUnlocalized != "ERROR" || diagnostic.Position != "8" {
		t.Fatalf("ErrorAt = %#v", err)
	}
	if !errors.As(ErrorAt("42601", "syntax error", parser.Position{}), &diagnostic) || diagnostic.Position != "" {
		t.Fatalf("zero position was recorded: %#v", diagnostic)
	}
}

func TestWithPosition(t *testing.T) {
	original := &sqlstate.Error{Severity: "ERROR", Code: "23505", Message: "duplicate key", Position: "2"}
	var diagnostic *sqlstate.Error
	if !errors.As(WithPosition(original, parser.Position{Character: 9}), &diagnostic) || diagnostic == original ||
		diagnostic.Position != "9" || diagnostic.Code != "23505" || original.Position != "2" {
		t.Fatalf("WithPosition = %#v, original %#v", diagnostic, original)
	}
	if !errors.As(WithPosition(original, parser.Position{}), &diagnostic) || diagnostic.Position != "0" {
		t.Fatalf("zero position = %#v", diagnostic)
	}
	plain := errors.New("plain")
	if WithPosition(plain, parser.Position{Character: 4}) != plain {
		t.Fatal("non-SQLSTATE error was rewritten")
	}
}
