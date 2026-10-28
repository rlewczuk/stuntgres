package sqltype

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/xml"
	"testing"
	"unsafe"
)

func TestXMLSQLValueOwnership(t *testing.T) {
	ctx := context.Background()
	types, foreign := testTypes(), testTypes()
	d := types[XML]
	if !d.Concrete() || d.Usage().Collation.ID != 0 || IsCharacter(XML) || foreign[143].Concrete() {
		t.Fatal("XML identity grants unrelated capabilities")
	}
	empty, err := d.Input(ctx, "", -1)
	if err != nil || !empty.ValidResult(d.Usage()) {
		t.Fatal(empty, err)
	}
	invalid := Value{typ: d}
	zero := Value{typ: d, xml: &xmlPayload{}}
	for _, v := range []Value{invalid, zero} {
		if v.ValidResult(d.Usage()) || SameDatum(v, v) {
			t.Fatal("unaccepted XML admitted")
		}
		if _, err := v.Retag(foreign[XML]); err == nil {
			t.Fatal("unaccepted XML retagged")
		}
		if _, err := v.XML(); err == nil {
			t.Fatal("unaccepted XML exposed")
		}
	}
	input := strings.Clone("<a/>")
	v, err := d.Input(ctx, input, -1)
	if err != nil {
		t.Fatal(err)
	}
	saved := v.xml
	copied := v
	if !SameDatum(v, copied) || copied.xml != saved || unsafe.StringData(saved.result.Original()) != unsafe.StringData(input) {
		t.Fatal("copy lost accepted backing")
	}
	retagged, err := v.Retag(foreign[XML])
	if err != nil || retagged.xml != saved || !retagged.ValidResult(foreign[XML].Usage()) {
		t.Fatal(retagged, err)
	}
	same, err := v.SamePayload(ctx, retagged)
	if err != nil || !same {
		t.Fatal(same, err)
	}
	fresh, err := d.Input(ctx, input, -1)
	if err != nil || SameDatum(v, fresh) {
		t.Fatal("fresh acceptance reused identity", err)
	}
	same, err = v.SamePayload(ctx, fresh)
	if err != nil || !same {
		t.Fatal("payload semantics use allocation identity", err)
	}
	document, err := XMLInput(ctx, d, input, XMLDocument, InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	same, err = v.SamePayload(ctx, document)
	if err != nil || same {
		t.Fatal("requested mode was lost", err)
	}
	domain, err := Domain(99000, "xml_domain", "xml_domain", d.Usage(), false)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := domain.Wrap(v)
	if err != nil || wrapped.xml != saved || !wrapped.ValidResult(domain.Usage()) {
		t.Fatal(wrapped, err)
	}
	base, err := wrapped.BaseValue()
	if err != nil || !SameDatum(v, base) {
		t.Fatal(base, err)
	}
	if _, err := domain.Wrap(invalid); err == nil {
		t.Fatal("invalid domain payload accepted")
	}
	longOriginal := "<a>" + strings.Repeat("x", 40000) + "</a>"
	left, err := d.Input(ctx, strings.Clone(longOriginal), -1)
	if err != nil {
		t.Fatal(err)
	}
	right, err := d.Input(ctx, strings.Clone(longOriginal), -1)
	if err != nil {
		t.Fatal(err)
	}
	leftBacking, rightBacking := left.xml, right.xml
	for _, cancelAt := range []int{2, 4} {
		base, cancel := context.WithCancel(ctx)
		interrupted := &stringTrimCancellationContext{Context: base, cancel: cancel, cancelAt: cancelAt}
		same, err := left.SamePayload(interrupted, right)
		cancel()
		if same || !errors.Is(err, context.Canceled) || left.xml != leftBacking || right.xml != rightBacking {
			t.Fatal("payload comparison cancellation changed accepted state", same, err)
		}
	}
	same, err = left.SamePayload(ctx, right)
	if err != nil || !same {
		t.Fatal("payload comparison reuse", same, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Input(canceled, "<broken>", -1); !errors.Is(err, context.Canceled) || v.xml != saved {
		t.Fatal("canceled attempt changed accepted backing", err)
	}
	if _, err := d.Input(ctx, "<broken>", -1); err == nil || v.xml != saved {
		t.Fatal("rejected attempt changed backing", err)
	}
	for _, mode := range []XMLMode{XMLContent, XMLDocument} {
		value, err := XMLInput(ctx, d, `<?xml version="1.0"?><a/>`, mode, InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		literal, err := d.SQLLiteral(ctx, value)
		want := "CONTENT"
		if mode == XMLDocument {
			want = "DOCUMENT"
		}
		if err != nil || literal != "XMLPARSE("+want+` '<?xml version="1.0"?><a/>')` {
			t.Fatal(literal, err)
		}
	}
	const adjacent = `<?xml version="1.0" encoding="UTF8"standalone="yes"?><a/>`
	accepted, err := XMLInput(ctx, d, adjacent, XMLDocument, InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	literal, err := d.SQLLiteral(ctx, accepted)
	if err != nil || literal != "XMLPARSE(DOCUMENT '"+adjacent+"')" {
		t.Fatal(literal, err)
	}
}

func TestXMLSQLLiteral(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[XML]
	original := "<a x=\"'\\\\\">" + strings.Repeat("é'\\\\", 20000) + "</a>"
	for _, mode := range []XMLMode{XMLContent, XMLDocument} {
		value, err := XMLInput(ctx, d, original, mode, InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		backing := value.xml
		literal, err := d.SQLLiteral(ctx, value)
		prefix := "XMLPARSE(CONTENT '"
		if mode == XMLDocument {
			prefix = "XMLPARSE(DOCUMENT '"
		}
		want := prefix + strings.ReplaceAll(original, "'", "''") + "')"
		if err != nil || literal != want {
			t.Fatal("long quoted literal mismatch", err)
		}
		// Decode the ordinary SQL doubled-quote spelling, without changing literal
		// backslashes; both the original and accepted parse mode must survive replay.
		decoded := strings.ReplaceAll(literal[len(prefix):len(literal)-2], "''", "'")
		replayed, err := XMLInput(ctx, d, decoded, mode, InputEnvironment{})
		if err != nil || replayed.xml.result.Original() != original || replayed.xml.result.RequestedMode() != mode {
			t.Fatal("literal roundtrip lost spelling or mode", err)
		}
		chunks := (len(original) + 4095) / 4096
		for _, cancelAt := range []int{1, 4, chunks + 5} {
			base, cancel := context.WithCancel(ctx)
			interrupted := &stringTrimCancellationContext{Context: base, cancel: cancel, cancelAt: cancelAt}
			failed, err := d.SQLLiteral(interrupted, value)
			cancel()
			if failed != "" || !errors.Is(err, context.Canceled) || interrupted.calls < cancelAt {
				t.Fatal("literal count/emission cancellation", len(failed), err)
			}
			if value.xml != backing || value.xml.result.Original() != original {
				t.Fatal("literal cancellation mutated accepted value")
			}
		}
	}
}

type xmlUnitDiagnostics struct{ warnings []sqlstate.Error }

func (n *xmlUnitDiagnostics) XMLInputDiagnostic(e sqlstate.Error) sqlstate.Error {
	e.Context = "unit input"
	return e
}
func (n *xmlUnitDiagnostics) XMLInputWarning(e sqlstate.Error) { n.warnings = append(n.warnings, e) }

func TestXMLSQLCodec(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	d := types[XML]
	const source = `<?xml version="1.0" encoding="LATIN1"?><a>é</a>`
	v, err := d.Input(ctx, source, -1)
	if err != nil {
		t.Fatal(err)
	}
	text, err := d.Output(ctx, v)
	if err != nil || text != "<a>é</a>" {
		t.Fatal(text, err)
	}
	raw, err := XMLSerialize(ctx, v, XMLContent, false, InputEnvironment{})
	if err != nil || raw != source {
		t.Fatal(raw, err)
	}
	for _, target := range []ID{Text, VarChar, BPChar, Name} {
		plan, err := PlanCoercion(d.Usage(), types[target].Usage(), Explicit)
		if err != nil {
			t.Fatal(target, err)
		}
		converted, err := plan.Apply(ctx, v, InputEnvironment{XMLMode: XMLDocument}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		want := source
		if target == Name {
			want = "<a>é</a>"
		}
		got, err := types[target].Output(ctx, converted)
		if err != nil || got != want {
			t.Fatal(target, got, err)
		}
	}
	binarySource := []byte("<?xml version=\"1.0\" encoding=\"LATIN1\"?><a>\xe9</a>")
	received, err := d.BinaryInputWithEnvironment(ctx, binarySource, -1, InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	binarySource[len(binarySource)-5] = 'x'
	got, err := d.Output(ctx, received)
	if err != nil || got != "<a>é</a>" {
		t.Fatal("binary input borrowed source", got, err)
	}
	for _, tc := range []struct{ encoding, want string }{{"UTF8", "<a>é</a>"}, {"SQL_ASCII", "<a>é</a>"}, {"LATIN1", "<?xml version=\"1.0\" encoding=\"LATIN1\"?><a>\xe9</a>"}} {
		sent, err := d.AppendBinaryWithEnvironment(ctx, []byte{7}, received, OutputEnvironment{ByteTransport: true, Transport: clientPolicy(t, tc.encoding)})
		if err != nil || string(sent) != "\x07"+tc.want {
			t.Fatal(tc.encoding, sent, err)
		}
	}
	notices := &xmlUnitDiagnostics{}
	env := InputEnvironment{XMLMode: XMLDocument, XMLInput: notices}
	if _, err := d.InputWithEnvironment(ctx, `<?xml version="1.1"?><a>`, -1, env); err == nil {
		t.Fatal("malformed XML accepted")
	} else {
		var e *sqlstate.Error
		if !errors.As(err, &e) || e.Code != "2200M" || e.Context != "unit input" {
			t.Fatal(err)
		}
	}
	if len(notices.warnings) != 1 || notices.warnings[0].Code != "01000" || notices.warnings[0].Severity != "WARNING" || notices.warnings[0].SeverityUnlocalized != "WARNING" {
		t.Fatal(notices.warnings)
	}
	const adjacent = `<?xml version="1.0" encoding="UTF8"standalone="yes"?><a/>`
	value, err := XMLInput(ctx, d, adjacent, XMLDocument, InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	var output []sqlstate.Error
	sink := OutputEnvironment{XMLNotice: func(e sqlstate.Error) { output = append(output, e) }}
	text, err = d.OutputWithEnvironment(ctx, value, sink)
	if err != nil || text != adjacent || len(output) != 1 || output[0].Code != "XX001" || output[0].Detail != "Space required." || output[0].Severity != "WARNING" || output[0].SeverityUnlocalized != "WARNING" {
		t.Fatal(text, output, err)
	}
	if _, err := XMLSerialize(ctx, value, XMLContent, false, InputEnvironment{}); err != nil || len(output) != 1 {
		t.Fatal(output, err)
	}
	// NAME is an ordinary typed-output I/O conversion, unlike the three
	// released raw XML character casts or XMLSERIALIZE's TEXT-to-NAME target.
	namePlan, err := PlanCoercion(d.Usage(), types[Name].Usage(), Explicit)
	if err != nil {
		t.Fatal(err)
	}
	var castWarnings []sqlstate.Error
	nameOutput := OutputEnvironment{XMLNotice: func(e sqlstate.Error) { castWarnings = append(castWarnings, e) }}
	named, err := namePlan.Apply(ctx, value, InputEnvironment{}, nameOutput)
	if err != nil {
		t.Fatal(err)
	}
	nameText, err := types[Name].Output(ctx, named)
	if err != nil || nameText != adjacent || len(castWarnings) != 1 || castWarnings[0].Code != "XX001" || castWarnings[0].Detail != "Space required." || castWarnings[0].Position != "" || castWarnings[0].Context != "" {
		t.Fatal(nameText, castWarnings, err)
	}
	// Typed-output warning remains committed when later encoding conversion fails.
	untranslatable := strings.Replace(adjacent, "<a/>", "<a>€</a>", 1)
	badOutput, err := XMLInput(ctx, d, untranslatable, XMLDocument, InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	failedSink := sink
	failedSink.ByteTransport = true
	failedSink.Transport = clientPolicy(t, "LATIN1")
	sent, err := d.AppendBinaryWithEnvironment(ctx, []byte{9}, badOutput, failedSink)
	var conversion *sqlstate.Error
	if string(sent) != "\x09" || !errors.As(err, &conversion) || conversion.Code != "22P05" || len(output) != 2 || output[1].Code != "XX001" {
		t.Fatal(sent, output, err)
	}
	fragment, err := d.Input(ctx, "text<a/>", -1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = XMLSerialize(ctx, fragment, XMLDocument, false, InputEnvironment{})
	var e *sqlstate.Error
	if !errors.As(err, &e) || e.Code != "2200L" || e.Detail != "" {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		err  error
		code string
	}{
		{&xml.EncodingError{Kind: xml.InvalidEncodingName, Message: "encoding"}, "22023"},
		{&xml.EncodingError{Kind: xml.MissingConversion, Message: "conversion"}, "42883"},
		{&xml.EncodingError{Kind: xml.InvalidSequence, Message: "sequence"}, "22021"},
		{&xml.EncodingError{Kind: xml.UntranslatableCharacter, Message: "character"}, "22P05"},
		{&xml.SizeError{Kind: xml.ConversionSize, Message: "size", Detail: "detail"}, "54000"},
		{&xml.SizeError{Kind: xml.AppendSize, Message: "size", Detail: "detail"}, "54000"},
	} {
		if !errors.As(xmlError(tc.err), &e) || e.Code != tc.code || e.Message != tc.err.Error() || e.Severity != "ERROR" || e.SeverityUnlocalized != "ERROR" {
			t.Fatal(tc, e)
		}
	}
	if !d.InputSessionDependent() || d.OutputSessionDependent() {
		t.Fatal("wrong XML codec volatility")
	}
	compose := &xmlUnitDiagnostics{}
	parsed, err := XMLInput(ctx, d, `<?xml version="1.1"?><a/>`, XMLDocument, InputEnvironment{XMLInput: compose})
	if err != nil || len(compose.warnings) != 1 {
		t.Fatal(compose.warnings, err)
	}
	_, err = XMLSerialize(ctx, parsed, XMLDocument, true, InputEnvironment{XMLInput: compose})
	if err != nil || len(compose.warnings) != 2 || compose.warnings[0].Message != compose.warnings[1].Message {
		t.Fatal(compose.warnings, err)
	}
	_, err = XMLSerialize(ctx, parsed, XMLContent, false, InputEnvironment{XMLInput: compose})
	if err != nil || len(compose.warnings) != 2 {
		t.Fatal("old warnings replayed", compose.warnings, err)
	}
	facts, ok := d.PGTypeMetadata()
	if !ok || facts.Input != 2893 || facts.Output != 2894 || facts.Receive != 2898 || facts.Send != 2899 || facts.Category != "U" {
		t.Fatal(facts)
	}
}

func TestXMLSQLTransportAuthority(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[XML]
	value, err := d.Input(ctx, "<a>é</a>", -1)
	if err != nil {
		t.Fatal(err)
	}
	policy := clientPolicy(t, "LATIN1")
	// Display spelling is not transport authority. Native values remain Unicode
	// even when the same presentation carries a nonUTF8 admitted capability.
	env := OutputEnvironment{ClientEncoding: "UTF8", Transport: policy}
	native, err := d.AppendBinaryWithEnvironment(ctx, nil, value, env)
	if err != nil || string(native) != "<a>é</a>" {
		t.Fatal(string(native), err)
	}
	env.ByteTransport = true
	sent, err := d.AppendBinaryWithEnvironment(ctx, []byte{7}, value, env)
	if err != nil || string(sent) != "\x07<?xml version=\"1.0\" encoding=\"LATIN1\"?><a>\xe9</a>" {
		t.Fatal(string(sent), err)
	}
	if env.Transport != policy {
		t.Fatal("send changed admitted authority")
	}
	// A known encoding ID without a directional provider cannot reach the
	// provider's built-in conversion kernel through the XML binary codec.
	env.Transport = ClientEncodingPolicy{id: 8, admitted: true, outgoing: NewEncodingConversion(6, 8, EncodingConversionHandler{})}
	sent, err = d.AppendBinaryWithEnvironment(ctx, []byte{7}, value, env)
	var diagnostic *sqlstate.Error
	if string(sent) != "\x07" || !errors.As(err, &diagnostic) || diagnostic.Code != "42883" {
		t.Fatal(string(sent), err)
	}
}
