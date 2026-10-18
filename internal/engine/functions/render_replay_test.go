package functions

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Replay pins what stored scalar SQL reads from a node where it differs from
// the display form: names, forms, operator schemas, literal transport,
// snapshot demands and the faults it reports instead of replaying.

// replayFault runs the replay check stored SQL runs before replaying.
func replayFault(spec expr.Rendering) error {
	if spec.Replay.Validate == nil {
		return nil
	}
	return spec.Replay.Validate()
}

func replayText(t *testing.T, d map[sqltype.ID]*sqltype.Descriptor, s string) expr.Expr {
	t.Helper()
	return temporalTestLiteral(sqltype.String(d[sqltype.Text], s))
}

func TestRenderReplayForms(t *testing.T) {
	d := temporalTestTypes()
	word, at := expr.TextPiece, expr.ArgPiece
	a, b, c := replayText(t, d, "a"), replayText(t, d, "b"), replayText(t, d, "c")

	// POSIX SUBSTRING displays as a call but replays its SQL syntax.
	posix := PosixSubstring{Args: [2]expr.Expr{a, b}, Syntax: parser.SQLFunctionSyntax}
	if got := renderOf(t, posix).Replay.Pieces; !reflect.DeepEqual(got, []expr.Piece{word("SUBSTRING("), at(0), word(" FROM "), at(1), word(")")}) {
		t.Errorf("POSIX SUBSTRING syntax: %+v", got)
	}
	posix.Syntax = parser.OrdinaryFunctionSyntax
	if got := renderOf(t, posix).Replay; got.Pieces != nil || got.Name != "" {
		t.Errorf("POSIX substring call: %+v", got)
	}

	// EXTRACT replays its field literal, which display shows as an identifier.
	source := temporalTestLiteral(temporalTestTimestamp(t, d[sqltype.Timestamp], 0))
	extract := renderOf(t, DatetimeExtract{Args: [2]expr.Expr{a, source}, Field: "a", Syntax: parser.SQLFunctionSyntax}).Replay
	if !reflect.DeepEqual(extract.Args, []expr.RenderArg{{Expr: a}, {Expr: source}}) ||
		!reflect.DeepEqual(extract.Pieces, []expr.Piece{word("EXTRACT("), at(0), word(" FROM "), at(1), word(")")}) {
		t.Errorf("EXTRACT: %+v", extract)
	}

	// SIMILAR TO replays over the normalizer's inputs; the normalizer itself
	// is absorbed, so its routine is recorded but not rendered.
	normalizer := SimilarEscape{Pattern: b, Escape: c}
	similar := renderOf(t, Similar{Left: a, Right: normalizer, Operator: "!~"}).Replay
	if !reflect.DeepEqual(similar.Args, []expr.RenderArg{{Expr: a}, {Expr: normalizer, Absorbed: true}, {Expr: b}, {Expr: c}}) ||
		!reflect.DeepEqual(similar.Pieces, []expr.Piece{word("("), at(0), word(" NOT SIMILAR TO "), at(2), word(" ESCAPE "), at(3), word(")")}) {
		t.Errorf("NOT SIMILAR TO ESCAPE: %+v", similar)
	}
	normalizer.Omitted = true
	similar = renderOf(t, Similar{Left: a, Right: normalizer, Operator: "~"}).Replay
	if !reflect.DeepEqual(similar.Args, []expr.RenderArg{{Expr: a}, {Expr: normalizer, Absorbed: true}, {Expr: b}}) ||
		!reflect.DeepEqual(similar.Pieces, []expr.Piece{word("("), at(0), word(" SIMILAR TO "), at(2), word(")")}) {
		t.Errorf("SIMILAR TO: %+v", similar)
	}
	if fault := replayFault(renderOf(t, Similar{Left: a, Right: b, Operator: "~"})); fault == nil || fault.Error() != "stuntgres: invalid stored SIMILAR normalizer" {
		t.Errorf("SIMILAR without normalizer: %v", fault)
	}

	// The field-notation fallback replays as field notation.
	count := NullCount{Info: expr.Info{Usage: d[sqltype.Int4].Usage()}, Kind: CountNonnulls, Args: []expr.Expr{a}, DotBuiltin: true}
	if got := renderOf(t, count).Replay.Pieces; !reflect.DeepEqual(got, []expr.Piece{word("("), at(0), word(")."), expr.IdentifierPiece("num_nonnulls")}) {
		t.Errorf("dot builtin: %+v", got)
	}

	// SQL value functions replay in lower case.
	raw := parser.CurrentDateTimeExpr{Kind: parser.LocalTimestamp, HasPrecision: true, Precision: 3}
	usage, _, err := ResolveCurrentDateTimeUsage(raw, d[CurrentDateTimeType(raw.Kind)].Usage())
	if err != nil {
		t.Fatal(err)
	}
	clock := CurrentDateTime{Info: expr.Info{Usage: usage}, Kind: raw.Kind, HasPrecision: true, Precision: usage.Typmod}
	clock.Property = expr.IntrinsicProperties(CurrentDateTimeIntrinsicRule())
	if spec := renderOf(t, clock); spec.Text != "LOCALTIMESTAMP(3)" || spec.Replay.Text != "localtimestamp(3)" {
		t.Errorf("current datetime: %q %q", spec.Text, spec.Replay.Text)
	}
}

func TestRenderReplayNames(t *testing.T) {
	d := temporalTestTypes()
	a, b := replayText(t, d, "a"), replayText(t, d, "b")
	if got := renderOf(t, LikeEscape{Pattern: a, Escape: b}).Replay.Name; got != "like_escape" {
		t.Errorf("like_escape: %q", got)
	}
	stamp := temporalTestLiteral(temporalTestTimestamp(t, d[sqltype.Timestamp], 0))
	overlaps := DatetimeOverlaps{Args: [4]expr.Expr{stamp, stamp, stamp, stamp}}
	if got := renderOf(t, overlaps).Replay.Name; got != "overlaps" {
		t.Errorf("overlaps call: %q", got)
	}
	overlaps.Syntax = parser.SQLFunctionSyntax
	if got := renderOf(t, overlaps).Replay.Name; got != "" {
		t.Errorf("OVERLAPS syntax: %q", got)
	}
	text := d[sqltype.Text]
	arg := stringUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.Null(text)}
	integer := stringUnitArgument{Info: expr.Info{Usage: d[sqltype.Int4].Usage()}, value: sqltype.Null(d[sqltype.Int4])}
	for _, c := range []struct {
		name   string
		args   []expr.Expr
		replay string
	}{
		{"normalize", []expr.Expr{arg}, "normalize"},
		{"overlay", []expr.Expr{arg, arg, integer}, "overlay"},
		{"left", []expr.Expr{arg, integer}, ""},
		{"lower", []expr.Expr{arg}, ""},
	} {
		var textual [4]bool
		for i, arg := range c.args {
			textual[i] = arg.ResultUsage().Type == text
		}
		n, err := NewString(expr.Info{Usage: text.Usage()}, c.name, text.Usage(), c.args, textual, parser.OrdinaryStringSyntax, false)
		if err != nil {
			t.Fatal(c.name, err)
		}
		if got := renderOf(t, n).Replay.Name; got != c.replay {
			t.Errorf("%s: %q", c.name, got)
		}
	}
}

func TestRenderReplayOperatorSchemas(t *testing.T) {
	types := formatUnitTypes()
	bytea := types[sqltype.Bytea]
	plan, err := sqltype.BindBinary("||", bytea.Usage(), bytea.Usage(), bytea.Usage())
	if err != nil {
		t.Fatal(err)
	}
	operand := binaryUnitArgument{Info: expr.Info{Usage: bytea.Usage()}}
	// Binary and string operators always replay OPERATOR(pg_catalog.op);
	// display shows pg_catalog operators bare.
	if spec := renderOf(t, Binary{Info: expr.Info{Usage: bytea.Usage()}, Plan: plan, Args: []expr.Expr{operand, operand}}); spec.Namespace != "pg_catalog" || spec.Replay.QuoteNamespace {
		t.Errorf("binary operator: %+v", spec)
	}
	text := types[sqltype.Text]
	arg := stringUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.Null(text)}
	for _, c := range []struct {
		name   string
		syntax parser.StringSyntax
	}{{"||", parser.OrdinaryStringSyntax}, {"starts_with", parser.PrefixStringSyntax}} {
		result := text.Usage()
		if c.name == "starts_with" {
			result = types[sqltype.Bool].Usage()
		}
		n, err := NewString(expr.Info{Usage: result}, c.name, text.Usage(), []expr.Expr{arg, arg}, [4]bool{true, true}, c.syntax, false)
		if err != nil {
			t.Fatal(err)
		}
		if spec := renderOf(t, n); spec.Form != expr.FormInfix || spec.Namespace != "pg_catalog" || spec.Replay.QuoteNamespace {
			t.Errorf("%s: %+v", c.name, spec)
		}
	}
	// Written schemas replay as written: always quoted for array and range
	// operators, quoted when needed for POSIX operators.
	if spec := renderOf(t, Posix{Left: arg, Right: arg, Operator: "~", Namespace: "pg_catalog"}); spec.Namespace != "pg_catalog" || spec.Replay.QuoteNamespace {
		t.Errorf("POSIX: %+v", spec)
	}
	if spec := renderOf(t, RangeOperator{Args: [2]expr.Expr{arg, arg}, Namespace: "pg_catalog"}); !spec.Replay.QuoteNamespace {
		t.Errorf("range operator: %+v", spec)
	}
}

func TestRenderReplayTransport(t *testing.T) {
	d := temporalTestTypes()
	a, b, c := replayText(t, d, "a"), replayText(t, d, "b"), replayText(t, d, "c")
	transports := func(args []expr.RenderArg) []expr.Transport {
		out := make([]expr.Transport, len(args))
		for i, arg := range args {
			out[i] = arg.Transport
		}
		return out
	}
	// format: the format string and raw unknown literals select their type by
	// spelling; other text literals need it.
	format := Format{Info: expr.Info{Usage: d[sqltype.Text].Usage()}, Args: []expr.Expr{a, b, c}, Int4: d[sqltype.Int4], RawUnknown: [2]uint64{4}}
	if got := transports(renderOf(t, format).Args); !reflect.DeepEqual(got, []expr.Transport{expr.TransportAmbiguous, expr.TransportAmbiguousText, expr.TransportAmbiguous}) {
		t.Errorf("format: %v", got)
	}
	if got := transports(renderOf(t, StringVariadic{Name: "concat", Args: []expr.Expr{a, b}}).Args); !reflect.DeepEqual(got, []expr.Transport{expr.TransportAmbiguous, expr.TransportAmbiguous}) {
		t.Errorf("concat: %v", got)
	}
	create := JSONCreate{Kind: sqltype.JSONBuildObject, Args: []expr.Expr{a, b}, Unknown: [2]uint64{1}}
	if got := transports(renderOf(t, create).Args); !reflect.DeepEqual(got, []expr.Transport{expr.TransportNone, expr.TransportLiteral}) {
		t.Errorf("JSON creation: %v", got)
	}

	// String arguments transport NAME literals; base conversions always
	// select their integer overload.
	text, integer := d[sqltype.Text], d[sqltype.Int8]
	lower, err := NewString(expr.Info{Usage: text.Usage()}, "lower", text.Usage(), []expr.Expr{a}, [4]bool{true}, parser.OrdinaryStringSyntax, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := transports(renderOf(t, lower).Args); !reflect.DeepEqual(got, []expr.Transport{expr.TransportName}) {
		t.Errorf("lower: %v", got)
	}
	value := stringUnitArgument{Info: expr.Info{Usage: integer.Usage()}, value: sqltype.Null(integer)}
	hex, err := NewString(expr.Info{Usage: text.Usage()}, "to_hex", text.Usage(), []expr.Expr{value}, [4]bool{}, parser.OrdinaryStringSyntax, false)
	if err != nil {
		t.Fatal(err)
	}
	if arg := renderOf(t, hex).Args[0]; arg.Transport != expr.TransportCast || arg.TransportType != hex.Plan.ArgumentUsage(0) {
		t.Errorf("to_hex: %+v", arg)
	}

	// The uuidv7 shift replays NULL intervals as typed NULLs.
	shift := temporalTestLiteral(sqltype.Null(d[sqltype.Interval]))
	uuid := UUIDGeneration{Info: expr.Info{Usage: d[sqltype.UUID].Usage()}, Name: "uuidv7", Args: []expr.Expr{shift}}
	if got := transports(renderOf(t, uuid).Args); !reflect.DeepEqual(got, []expr.Transport{expr.TransportIntervalNulls}) {
		t.Errorf("uuidv7 shift: %v", got)
	}
}

func TestRenderReplaySnapshots(t *testing.T) {
	d := temporalTestTypes()
	text := d[sqltype.Text]
	relation := temporalTestLiteral(sqltype.Null(d[sqltype.Regclass]))
	plain := replayText(t, d, "a")
	if !renderOf(t, StringVariadic{Name: "concat", Args: []expr.Expr{plain, relation}}).Replay.CatalogSnapshot {
		t.Error("concat over regclass reads no catalog names")
	}
	if renderOf(t, StringVariadic{Name: "concat", Args: []expr.Expr{plain}}).Replay.CatalogSnapshot {
		t.Error("concat over text reads catalog names")
	}
	// String calls read catalog names only through non-textual arguments.
	n, err := NewString(expr.Info{Usage: text.Usage()}, "lower", text.Usage(), []expr.Expr{plain}, [4]bool{true}, parser.OrdinaryStringSyntax, false)
	if err != nil {
		t.Fatal(err)
	}
	if renderOf(t, n).Replay.CatalogSnapshot {
		t.Error("textual string argument reads catalog names")
	}

	bytea := d[sqltype.Bytea]
	operand := binaryUnitArgument{Info: expr.Info{Usage: bytea.Usage()}}
	name := binaryUnitArgument{Info: expr.Info{Usage: d[sqltype.Name].Usage()}}
	for _, c := range []struct {
		name       string
		args       []expr.Expr
		result     sqltype.Usage
		visibility bool
	}{
		{"convert_from", []expr.Expr{operand, name}, text.Usage(), true},
		{"md5", []expr.Expr{operand}, text.Usage(), false},
	} {
		usages := make([]sqltype.Usage, len(c.args))
		for i, arg := range c.args {
			usages[i] = arg.ResultUsage()
		}
		plan, err := sqltype.BindBinary(c.name, c.result, usages...)
		if err != nil {
			t.Fatal(c.name, err)
		}
		if got := renderOf(t, Binary{Info: expr.Info{Usage: c.result}, Plan: plan, Args: c.args}).Replay.VisibilitySnapshot; got != c.visibility {
			t.Errorf("%s visibility: %t", c.name, got)
		}
	}
}

func TestRenderReplayFaults(t *testing.T) {
	d := temporalTestTypes()
	a := replayText(t, d, "a")
	unsupported := errors.New("stuntgres: unsupported stored default node")
	// Display never runs the replay check, so it renders nodes the check rejects.
	for _, c := range []struct {
		name  string
		node  expr.Renderable
		fault string
	}{
		{"standalone normalizer", SimilarEscape{Pattern: a, Escape: a}, "stuntgres: standalone stored SIMILAR normalizer"},
		{"regexp set", RegexpSet{RegexpCall{Args: []expr.Expr{a, a}, WrittenSlots: [7]uint8{0, 1}}}, unsupported.Error()},
		{"path query", JSONBPathQuery{Args: [4]expr.Expr{a, a}, WrittenCount: 2, WrittenSlots: [4]uint8{0, 1}}, unsupported.Error()},
		{"object keys", JSONKeys{Args: []expr.Expr{a}}, unsupported.Error()},
		{"random", Random{Info: expr.Info{Usage: d[sqltype.Text].Usage()}, Name: "random"}, "stuntgres: invalid stored random signature"},
		{"uuid", UUIDGeneration{Info: expr.Info{Usage: d[sqltype.Text].Usage()}, Name: "uuidv4"}, "stuntgres: invalid stored UUID generation signature"},
		{"null count", NullCount{Info: expr.Info{Usage: d[sqltype.Text].Usage()}, Kind: CountNulls, Args: []expr.Expr{a}}, "stuntgres: invalid stored null count signature"},
	} {
		spec, err := c.node.Render()
		if err != nil {
			t.Errorf("%s: display failed: %v", c.name, err)
			continue
		}
		if fault := replayFault(spec); fault == nil || fault.Error() != c.fault {
			t.Errorf("%s: fault %v", c.name, fault)
		}
	}
	// random() replays only in relation-owned expressions.
	random := Random{Info: expr.Info{Usage: d[sqltype.Float8].Usage()}, Name: "random"}
	if spec := renderOf(t, random); replayFault(spec) != nil || !spec.Replay.RelationOnly {
		t.Errorf("random: %+v", spec.Replay)
	}
	// pg_sleep carries its clock type for the catalog check.
	clock := d[sqltype.Timestamptz]
	sleep := DatetimeSleep{Info: expr.Info{Usage: d[sqltype.Void].Usage()}, Kind: DatetimeSleepSeconds, Arg: a, ClockUsage: clock.Usage()}
	sleep.Property = expr.IntrinsicProperties(DatetimeSleepIntrinsicRule())
	if spec, err := sleep.Render(); err == nil && !reflect.DeepEqual(spec.Replay.Types, []*sqltype.Descriptor{clock}) {
		t.Errorf("pg_sleep types: %+v", spec.Replay.Types)
	}
}
