package xml

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

func TestXMLSerializeCallDiagnostics(t *testing.T) {
	const input = `<?xml version="1.1"?><a/>`
	const warning = "line 1: Unsupported version '1.1'\n" + input + "\n                   ^"
	value := serializeValue(t, input, Content)
	if len(value.Diagnostics()) != 0 {
		t.Fatal("CONTENT input warned")
	}
	out, events, err := Serialize(context.Background(), value, SerializeOptions{Mode: Document, Indent: true})
	if err != nil || out != "<?xml version=\"1.1\" encoding=\"UTF-8\"?>\n<a/>" || len(events) != 1 || events[0].Severity != Warning || events[0].String() != warning {
		t.Fatal(out, events, err)
	}
	events[0].Message = "mutated"
	_, again, err := Serialize(context.Background(), value, SerializeOptions{Mode: Document, Indent: true})
	if err != nil || len(again) != 1 || again[0].String() != warning || len(value.Diagnostics()) != 0 {
		t.Fatal(again, err)
	}
	raw, quiet, err := Serialize(context.Background(), value, SerializeOptions{})
	if err != nil || len(quiet) != 0 || unsafe.StringData(raw) != unsafe.StringData(value.Original()) {
		t.Fatal(raw, quiet, err)
	}
	// Failure retains real qualification events, without publishing partial text.
	fragment := serializeValue(t, `<?xml version="1.1"?>text<a/>`, Content)
	out, events, err = Serialize(context.Background(), fragment, SerializeOptions{Mode: Document, Indent: true})
	var nd *NotDocumentError
	if out != "" || !errors.As(err, &nd) || len(events) < 2 || events[0].Severity != Warning || events[0].Condition != "XML_WAR_UNKNOWN_VERSION" {
		t.Fatal(out, events, err)
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		before := value
		early := &phaseContext{Context: context.Background(), phase: "Serialize", after: 1, cause: cause}
		out, events, err = Serialize(early, value, SerializeOptions{Mode: Document, Indent: true})
		if out != "" || len(events) != 0 || !errors.Is(err, cause) {
			t.Fatal(out, events, err)
		}
		// The declaration warning precedes the real long character-data phase.
		long := serializeValue(t, `<?xml version="1.1"?><a>`+strings.Repeat("x", 20000)+`</a>`, Content)
		late := &phaseContext{Context: context.Background(), phase: "text", after: 2, cause: cause}
		out, events, err = Serialize(late, long, SerializeOptions{Mode: Document, Indent: true})
		if out != "" || !late.admitted || !errors.Is(err, cause) || len(events) != 1 || events[0].Severity != Warning {
			t.Fatal(out, events, err)
		}
		if _, ok := err.(interface{ SQLState() string }); ok {
			t.Fatal("cancellation acquired SQLSTATE")
		}
		if !reflect.DeepEqual(value, before) || len(long.Diagnostics()) != 0 {
			t.Fatal("failed serialization changed accepted value")
		}
	}
}

func TestXMLSerializeGeneratedQualification(t *testing.T) {
	ctx := context.Background()
	value, err := ConstructElement(ctx, "r", nil, []string{"\x01"})
	if err != nil || !value.Valid() || value.Original() != "<r>\x01</r>" {
		t.Fatal(value, err)
	}
	before := value
	backing := unsafe.StringData(value.Original())
	raw, events, err := Serialize(ctx, value, SerializeOptions{})
	if err != nil || len(events) != 0 || raw != value.Original() || unsafe.StringData(raw) != backing {
		t.Fatal(raw, events, err)
	}
	for _, opts := range []SerializeOptions{
		{Mode: Content, Indent: true},
		{Mode: Document},
		{Mode: Document, Indent: true},
	} {
		out, events, err := Serialize(ctx, value, opts)
		var nd *NotDocumentError
		var pe *ParseError
		if out != "" || !errors.As(err, &nd) || errors.As(err, &pe) || err.Error() != "not an XML document" {
			t.Fatal(opts, out, err)
		}
		// Qualification still reports this call's real parser events; only
		// the error classification changes, not the event collection.
		if len(events) == 0 {
			t.Fatal("qualification discarded parser events")
		}
		if !reflect.DeepEqual(before, value) || unsafe.StringData(value.Original()) != backing {
			t.Fatal("failed qualification changed accepted generated value")
		}
	}
	for _, mode := range []Mode{Content, Document} {
		rejected, err := Parse(ctx, value.Original(), Options{Mode: mode})
		var pe *ParseError
		if rejected.Valid() || !errors.As(err, &pe) || pe.RequestedMode != mode || pe.Detail() != "line 1: PCDATA invalid Char value 1\n<r>\x01</r>\n   ^" {
			t.Fatal(mode, rejected, err)
		}
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		c := &phaseContext{Context: ctx, phase: "Serialize", after: 1, cause: cause}
		out, events, err := Serialize(c, value, SerializeOptions{Indent: true})
		if out != "" || len(events) != 0 || !errors.Is(err, cause) {
			t.Fatal(out, events, err)
		}
	}
}
