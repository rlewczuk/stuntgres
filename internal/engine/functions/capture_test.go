package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

// Markers are identity-bearing, immutable and deliberately cannot be evaluated.
type captureMarker struct {
	expr.Info
	index int
}

func (*captureMarker) Eval(expr.Context) (sqltype.Value, error) { panic("capture evaluated marker") }

func captureMarkers() [7]expr.Expr {
	var args [7]expr.Expr
	types := temporalTestTypes()
	for i := range args {
		args[i] = &captureMarker{Info: expr.Info{Usage: types[sqltype.Text].Usage()}, index: i}
	}
	return args
}

func TestCaptureOperandsWrittenSlots(t *testing.T) {
	a := captureMarkers()
	slots := [4]uint8{2, 0, 3, 1}
	cases := []struct {
		name string
		node expr.Capturable
		want []expr.Expr
	}{
		{"jsonb_set", JSONBSet{Args: [5]expr.Expr{a[0], a[1], a[2], a[3], a[4]}, Count: 5, WrittenCount: 3, WrittenSlots: [5]uint8{2, 0, 1}, Labels: [5]string{"replacement", "jsonb_in", "path"}}, []expr.Expr{a[2], a[0], a[1]}},
		{"strip", JSONStripNulls{Args: [2]expr.Expr{a[0], a[1]}, WrittenCount: 1}, []expr.Expr{a[0]}},
		{"path_array", JSONBPathQueryArray{Args: [4]expr.Expr{a[0], a[1], a[2], a[3]}, WrittenCount: 3, WrittenSlots: slots}, []expr.Expr{a[2], a[0], a[3]}},
		{"path_exists", JSONBPathExists{Args: [4]expr.Expr{a[0], a[1], a[2], a[3]}, WrittenCount: 3, WrittenSlots: slots}, []expr.Expr{a[2], a[0], a[3]}},
		{"path_match", JSONBPathMatch{Args: [4]expr.Expr{a[0], a[1], a[2], a[3]}, WrittenCount: 3, WrittenSlots: slots}, []expr.Expr{a[2], a[0], a[3]}},
		{"path_first", JSONBPathQueryFirst{Args: [4]expr.Expr{a[0], a[1], a[2], a[3]}, WrittenCount: 3, WrittenSlots: slots}, []expr.Expr{a[2], a[0], a[3]}},
		{"path_query", JSONBPathQuery{Args: [4]expr.Expr{a[0], a[1], a[2], a[3]}, WrittenCount: 3, WrittenSlots: slots}, []expr.Expr{a[2], a[0], a[3]}},
		{"make", DatetimeMake{Args: a, Count: 7, WrittenCount: 2, WrittenSlots: [7]uint8{4, 1}, Labels: [7]string{"mins", "months"}}, []expr.Expr{a[4], a[1]}},
		{"regexp", RegexpCall{Args: a[:3], WrittenSlots: [7]uint8{2, 0, 1}}, []expr.Expr{a[2], a[0], a[1]}},
		{"array_written_not_formal", ArrayTransform{Args: a[:3], Slots: [3]uint8{2, 0, 1}, Labels: [3]string{"array", "element", "position"}}, a[:3]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := fmt.Sprintf("%#v", tc.node)
			got, ok, err := tc.node.Capture(expr.CaptureFunction)
			if err != nil || !ok || got.Check(tc.node, len(tc.want)) != nil {
				t.Fatal(got, ok, err)
			}
			for i, want := range tc.want {
				actual, err := got.At(tc.node, i)
				if err != nil || actual != want {
					t.Fatal(i, actual, want, err)
				}
			}
			if indexed, ok := tc.node.(expr.IndexedCapturable); ok {
				if !got.Indexed || got.Args != nil || got.Inline[0] != nil || got.Inline[1] != nil {
					t.Fatal("fixed storage leaked into descriptor", got)
				}
				for _, invalid := range []int{-1, len(tc.want)} {
					if _, err := indexed.CaptureOperand(invalid); err == nil {
						t.Fatal("indexed accessor accepted invalid ordinal", invalid)
					}
				}
			}
			if before != fmt.Sprintf("%#v", tc.node) {
				t.Fatal("capture mutated receiver")
			}
		})
	}
	for _, node := range []expr.Capturable{
		JSONBSet{Count: 6, WrittenCount: 3}, JSONBSet{Count: 4, WrittenCount: -1},
		JSONStripNulls{WrittenCount: 3}, DatetimeMake{Count: -1},
		RegexpCall{Args: make([]expr.Expr, 8)},
		JSONBPathExists{Args: [4]expr.Expr{a[0]}, WrittenCount: 1, WrittenSlots: [4]uint8{9}},
	} {
		if _, _, err := node.Capture(expr.CaptureFunction); err == nil {
			t.Fatalf("malformed %T accepted", node)
		}
	}
	for _, node := range []expr.Capturable{
		AccessInquiry{Count: -1}, AccessInquiry{Count: 5}, AccessInquiry{Count: 1},
		ACLValue{Kind: ACLKind(255)}, ACLValue{Kind: ACLMake},
		DatetimeAgeFinite{Count: 3}, DatetimeAgeFinite{Count: 1},
		DatetimeTrunc{Count: -1}, DatetimeTrunc{Count: 4},
		DatetimeZoneCalendar{Count: 4}, DatetimeZoneCalendar{Count: 1},
		DatetimeBin{}, DatetimeOverlaps{}, ObjectACL{}, PosixSubstring{}, SimilarSubstring{},
		JSONBSet{Args: [5]expr.Expr{a[0]}, Count: 1, WrittenCount: 1, WrittenSlots: [5]uint8{1}},
		JSONStripNulls{WrittenCount: 1}, RegexpCall{Args: []expr.Expr{nil}},
	} {
		operands, admitted, err := node.Capture(expr.CaptureFunction)
		if !admitted {
			t.Fatalf("malformed %T lost syntax admission", node)
		}
		if err == nil && operands.Check(node, operands.Count) == nil {
			t.Fatalf("malformed %T accepted", node)
		}
		if _, err := node.(expr.IndexedCapturable).CaptureOperand(0); err == nil {
			t.Fatalf("malformed %T indexed storage accepted", node)
		}
	}
}

func TestCaptureOperandsRawKinds(t *testing.T) {
	a := captureMarkers()
	for _, tc := range []struct {
		node     expr.Capturable
		kind     expr.CaptureKind
		want     []expr.Expr
		admitted bool
	}{
		{Timezone{Value: a[0], Zone: a[1], Function: true}, expr.CaptureFunction, []expr.Expr{a[1], a[0]}, true},
		{Timezone{Value: a[0], Zone: a[1]}, expr.CaptureTimeZone, []expr.Expr{a[0], a[1]}, true},
		{Timezone{Value: a[0], Local: true}, expr.CaptureLocal, []expr.Expr{a[0]}, true},
		{Timezone{Value: a[0], Local: true}, expr.CaptureTimeZone, nil, false},
		{Timezone{Value: a[0], Zone: a[1]}, expr.CaptureFunction, nil, false},
		{NullCount{Args: a[:2]}, expr.CaptureOperator, nil, false},
		{CurrentDateTime{}, expr.CaptureFunction, nil, false},
		{CurrentDateTime{}, expr.CaptureCurrentDateTime, nil, true},
		{SimilarEscape{Pattern: a[0], Escape: a[1], Omitted: true}, expr.CaptureFunction, []expr.Expr{a[0]}, true},
		{LikeEscape{Pattern: a[0], Escape: a[1]}, expr.CaptureFunction, a[:2], true},
	} {
		got, admitted, err := tc.node.Capture(tc.kind)
		if err != nil || admitted != tc.admitted {
			t.Fatalf("%T %d: %v %v", tc.node, tc.kind, admitted, err)
		}
		if !admitted {
			continue
		}
		if err := got.Check(tc.node, len(tc.want)); err != nil {
			t.Fatal(err)
		}
		for i, want := range tc.want {
			actual, err := got.At(tc.node, i)
			if err != nil || actual != want {
				t.Fatal(actual, want, err)
			}
		}
	}
	for _, node := range []expr.Capturable{VersionInformation{}, TransactionTime{}} {
		if _, _, err := node.Capture(expr.CaptureFunction); err == nil {
			t.Fatalf("invalid leaf %T accepted", node)
		}
	}
}

func TestCaptureOperandsSpecialAnnotations(t *testing.T) {
	a := captureMarkers()
	for _, node := range []expr.Capturable{ArrayTransform{Args: a[:2]}, ArraySearch{Args: a[:2]}, ArrayInspect{Args: a[:1]}, ArrayRandom{Args: a[:1]}} {
		got, ok, err := node.Capture(expr.CaptureFunction)
		if err != nil || !ok || !got.SelectedImplicit || got.CallImplicit {
			t.Fatal(node, got, err)
		}
	}
	sleep, _, _ := (DatetimeSleep{Arg: a[0]}).Capture(expr.CaptureFunction)
	if !sleep.CallImplicit || sleep.SelectedImplicit || sleep.Count != 1 {
		t.Fatal(sleep)
	}
	posix, _, _ := (PosixSubstring{}).Capture(expr.CaptureFunction)
	similar, _, _ := (SimilarSubstring{}).Capture(expr.CaptureFunction)
	if !posix.PosixSubstring || posix.SimilarSubstring || !similar.SimilarSubstring || similar.PosixSubstring {
		t.Fatal(posix, similar)
	}
	json, _, _ := (SQLJSON{Args: a[:2]}).Capture(expr.CaptureSQLJSON)
	if !json.JSONSources {
		t.Fatal(json)
	}
	dot, ok, err := (NullCount{Kind: CountNonnulls, Args: a[:1]}).Capture(expr.CaptureField)
	if err != nil || !ok || dot.DotBuiltin != "num_nonnulls" || dot.Count != 1 {
		t.Fatal(dot, ok, err)
	}
}

type capturePromotedJSONBSet struct{ JSONBSet }

var captureOperandsSink expr.CaptureOperands

// Keep the real dynamic interface boundary: concrete devirtualization could hide
// receiver-array escapes. Fixtures and interface boxes are built before measuring.
//
//go:noinline
func exerciseCaptureOperands(source expr.Capturable, kind expr.CaptureKind, want []expr.Expr) {
	operands, admitted, err := source.Capture(kind)
	if err != nil {
		panic(err)
	}
	if !admitted {
		panic("capture rejected fixture")
	}
	if err := operands.Check(source, len(want)); err != nil {
		panic(err)
	}
	for written, expected := range want {
		actual, err := operands.At(source, written)
		if err != nil {
			panic(err)
		}
		if actual != expected {
			panic("capture changed written operand identity")
		}
	}
	captureOperandsSink = operands
}

func TestCaptureOperandsNoAllocations(t *testing.T) {
	a := captureMarkers()
	set := JSONBSet{Args: [5]expr.Expr{a[0], a[1], a[2], a[3], a[4]}, Count: 5, WrittenCount: 3, WrittenSlots: [5]uint8{2, 0, 1}, Labels: [5]string{"replacement", "jsonb_in", "path"}}
	regexp := RegexpCall{Args: a[:3], WrittenSlots: [7]uint8{2, 0, 1}, Labels: [7]string{"pattern", "string", "replacement"}}
	promoted := capturePromotedJSONBSet{set}
	// Every affected family is exercised through a preboxed Capturable. Default
	// operands deliberately remain outside want; distinct markers expose reordering.
	cases := []struct {
		name string
		node expr.Capturable
		kind expr.CaptureKind
		want []expr.Expr
	}{
		{"jsonb_set", set, expr.CaptureFunction, []expr.Expr{a[2], a[0], a[1]}},
		{"jsonb_set_pointer", &set, expr.CaptureFunction, []expr.Expr{a[2], a[0], a[1]}},
		{"promoted_value", promoted, expr.CaptureFunction, []expr.Expr{a[2], a[0], a[1]}},
		{"promoted_pointer", &promoted, expr.CaptureFunction, []expr.Expr{a[2], a[0], a[1]}},
		{"strip", JSONStripNulls{Args: [2]expr.Expr{a[0], a[1]}, WrittenCount: 1}, expr.CaptureFunction, a[:1]},
		{"path_array", JSONBPathQueryArray{Args: [4]expr.Expr{a[0], a[1], a[2], a[3]}, WrittenCount: 2, WrittenSlots: [4]uint8{1, 0}}, expr.CaptureFunction, []expr.Expr{a[1], a[0]}},
		{"path_exists", JSONBPathExists{Args: [4]expr.Expr{a[0], a[1], a[2], a[3]}, WrittenCount: 2, WrittenSlots: [4]uint8{1, 0}}, expr.CaptureFunction, []expr.Expr{a[1], a[0]}},
		{"path_match", JSONBPathMatch{Args: [4]expr.Expr{a[0], a[1], a[2], a[3]}, WrittenCount: 2, WrittenSlots: [4]uint8{1, 0}}, expr.CaptureFunction, []expr.Expr{a[1], a[0]}},
		{"path_first", JSONBPathQueryFirst{Args: [4]expr.Expr{a[0], a[1], a[2], a[3]}, WrittenCount: 2, WrittenSlots: [4]uint8{1, 0}}, expr.CaptureFunction, []expr.Expr{a[1], a[0]}},
		{"path_query", JSONBPathQuery{Args: [4]expr.Expr{a[0], a[1], a[2], a[3]}, WrittenCount: 2, WrittenSlots: [4]uint8{1, 0}}, expr.CaptureFunction, []expr.Expr{a[1], a[0]}},
		{"make", DatetimeMake{Args: a, Count: 7, WrittenCount: 2, WrittenSlots: [7]uint8{4, 1}, Labels: [7]string{"mins", "months"}}, expr.CaptureFunction, []expr.Expr{a[4], a[1]}},
		{"regexp_call", regexp, expr.CaptureFunction, []expr.Expr{a[2], a[0], a[1]}},
		{"regexp", Regexp{regexp}, expr.CaptureFunction, []expr.Expr{a[2], a[0], a[1]}},
		{"regexp_set", RegexpSet{regexp}, expr.CaptureFunction, []expr.Expr{a[2], a[0], a[1]}},
		{"access", AccessInquiry{Args: [4]expr.Expr{a[0], a[1], a[2]}, Count: 3}, expr.CaptureFunction, a[:3]},
		{"acl", ACLValue{Kind: ACLMake, Args: [4]expr.Expr{a[0], a[1], a[2], a[3]}}, expr.CaptureFunction, a[:4]},
		{"acl_operator", ACLValue{Kind: ACLContains, Args: [4]expr.Expr{a[0], a[1]}}, expr.CaptureOperator, a[:2]},
		{"age", DatetimeAgeFinite{Args: [2]expr.Expr{a[0], a[1]}, Count: 2}, expr.CaptureFunction, a[:2]},
		{"trunc", DatetimeTrunc{Args: [3]expr.Expr{a[0], a[1], a[2]}, Count: 3}, expr.CaptureFunction, a[:3]},
		{"zone", DatetimeZoneCalendar{Args: [3]expr.Expr{a[0], a[1], a[2]}, Count: 3}, expr.CaptureFunction, a[:3]},
		{"bin", DatetimeBin{Args: [3]expr.Expr{a[0], a[1], a[2]}}, expr.CaptureFunction, a[:3]},
		{"overlaps", DatetimeOverlaps{Args: [4]expr.Expr{a[0], a[1], a[2], a[3]}}, expr.CaptureFunction, a[:4]},
		{"object_acl_written_not_slots", ObjectACL{Args: [3]expr.Expr{a[0], a[1], a[2]}, Slots: [3]uint8{2, 0, 1}}, expr.CaptureFunction, a[:3]},
		{"posix", PosixSubstring{Args: [2]expr.Expr{a[0], a[1]}}, expr.CaptureFunction, a[:2]},
		{"similar", SimilarSubstring{Args: [3]expr.Expr{a[0], a[1], a[2]}}, expr.CaptureFunction, a[:3]},
		{"safe_slice", ArrayTransform{Args: a[:3], Slots: [3]uint8{2, 0, 1}}, expr.CaptureFunction, a[:3]},
		{"inline_timezone", Timezone{Zone: a[1], Value: a[0], Function: true}, expr.CaptureFunction, []expr.Expr{a[1], a[0]}},
		{"zero_leaf", CurrentDateTime{}, expr.CaptureCurrentDateTime, nil},
		{"advisory", Advisory{Args: a[:2]}, expr.CaptureFunction, a[:2]},
		{"blocking", BlockingInquiry{Arg: a[0]}, expr.CaptureFunction, a[:1]},
		{"sequence", SequenceCall{Name: "setval", Args: a[:3]}, expr.CaptureFunction, a[:3]},
		{"sequence lastval", SequenceCall{Name: "lastval"}, expr.CaptureFunction, nil},
	}
	defer func() { captureOperandsSink = expr.CaptureOperands{} }()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := fmt.Sprintf("%#v", tc.node)
			if allocations := testing.AllocsPerRun(100, func() {
				exerciseCaptureOperands(tc.node, tc.kind, tc.want)
			}); allocations != 0 {
				t.Fatalf("capture allocated %g times per operation; want zero", allocations)
			}
			if before != fmt.Sprintf("%#v", tc.node) {
				t.Fatal("capture mutated receiver storage or provenance")
			}
		})
	}
}
