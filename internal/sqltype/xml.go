package sqltype

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/xml"
)

type XMLMode = xml.Mode

const (
	XMLContent  = xml.Content
	XMLDocument = xml.Document
)

// XMLInputDiagnostics belongs to one input operation, never to a retained value.
type XMLInputDiagnostics interface {
	XMLInputDiagnostic(sqlstate.Error) sqlstate.Error
	XMLInputWarning(sqlstate.Error)
}
type xmlPayload struct{ result xml.Result }

func (v Value) validXML() bool {
	return v.typ != nil && v.typ.BaseType().id == XML && v.xml != nil && v.xml.result.Valid()
}
func (v Value) XML() (xml.Result, error) {
	if v.null || !v.validXML() {
		return xml.Result{}, fmt.Errorf("not an XML value")
	}
	return v.xml.result, nil
}

// XMLInput validates explicit-mode characters without retaining the environment.
func XMLInput(ctx context.Context, d *Descriptor, text string, mode XMLMode, env InputEnvironment) (Value, error) {
	return xmlInput(ctx, d, text, xml.Options{Mode: mode}, env)
}
func xmlInput(ctx context.Context, d *Descriptor, text string, opts xml.Options, env InputEnvironment) (Value, error) {
	if d == nil || d.id != XML || !d.Concrete() {
		return Value{}, fmt.Errorf("invalid XML descriptor")
	}
	result, err := xml.Parse(ctx, text, opts)
	if env.XMLInput != nil {
		events := result.Diagnostics()
		if err != nil {
			var pe *xml.ParseError
			if errors.As(err, &pe) {
				events = pe.Diagnostics
			}
		}
		xmlWarnings(events, env)
	}
	if err != nil {
		return Value{}, xmlInputError(err, env)
	}
	return Value{typ: d, xml: &xmlPayload{result: result}}, nil
}
func xmlWarnings(events []xml.Diagnostic, env InputEnvironment) {
	if env.XMLInput == nil {
		return
	}
	for _, event := range events {
		if event.Severity == xml.Warning {
			env.XMLInput.XMLInputWarning(sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: event.String()})
		}
	}
}
func xmlError(err error) error {
	if err == nil {
		return nil
	}
	var constructor *xml.ConstructError
	if errors.As(err, &constructor) {
		code := "2200S"
		if constructor.PI { code = "2200T" }
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: constructor.Message, Detail: constructor.Detail}
	}
	var xp *xml.XPathError
	if errors.As(err, &xp) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "10608", Message: xp.Error(), Detail: xp.Detail}
	}
	var xc *xml.XPathContextError
	if errors.As(err, &xc) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2200M", Message: xc.Error(), Detail: xc.Parse.Detail()}
	}
	var pe *xml.ParseError
	if errors.As(err, &pe) {
		code := "2200N"
		if pe.Category != xml.DeclarationError && pe.RequestedMode == xml.Document {
			code = "2200M"
		}
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: pe.Error(), Detail: pe.Detail()}
	}
	var nd *xml.NotDocumentError
	if errors.As(err, &nd) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2200L", Message: nd.Error()}
	}
	var enc *xml.EncodingError
	if errors.As(err, &enc) {
		code := "22023"
		switch enc.Kind {
		case xml.MissingConversion:
			code = "42883"
		case xml.InvalidSequence:
			code = "22021"
		case xml.UntranslatableCharacter:
			code = "22P05"
		}
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: enc.Message, Detail: enc.Detail}
	}
	var size *xml.SizeError
	if errors.As(err, &size) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54000", Message: size.Message, Detail: size.Detail}
	}
	return err
}
func xmlInputError(err error, env InputEnvironment) error {
	mapped := xmlError(err)
	if e, ok := mapped.(*sqlstate.Error); ok && env.XMLInput != nil {
		copy := env.XMLInput.XMLInputDiagnostic(*e)
		return &copy
	}
	return mapped
}

// XMLSerialize is character conversion, not typed transport output. Its warnings
// originate only in this call's qualification parse, never in the stored Result.
func XMLSerialize(ctx context.Context, v Value, mode XMLMode, indent bool, env InputEnvironment) (string, error) {
	result, err := v.XML()
	if err != nil {
		return "", err
	}
	text, events, err := xml.Serialize(ctx, result, xml.SerializeOptions{Mode: mode, Indent: indent})
	xmlWarnings(events, env)
	return text, xmlInputError(err, env)
}

func XMLIsDocument(ctx context.Context, v Value) (bool, error) {
	result, err := v.XML()
	if err != nil {
		return false, err
	}
	ok, err := xml.IsDocument(ctx, result)
	return ok, xmlError(err)
}
func XMLIsWellFormed(ctx context.Context, text string, mode XMLMode) (bool, error) {
	ok, err := xml.IsWellFormed(ctx, text, mode)
	return ok, xmlError(err)
}
func XMLExists(ctx context.Context, path string, v Value, env InputEnvironment) (bool, error) {
	result, err := v.XML()
	if err != nil {
		return false, err
	}
	ok, events, err := xml.XMLExists(ctx, path, result)
	xmlWarnings(events, env)
	return ok, xmlInputError(err, env)
}
func (d *Descriptor) xmlOutput(ctx context.Context, v Value, env OutputEnvironment, binary bool) (string, error) {
	result, err := v.XML()
	if err != nil {
		return "", err
	}
	var text string
	var warning xml.OutputWarning
	if binary {
		id := 6
		if env.ByteTransport {
			id = env.Transport.ID()
		}
		text, warning, err = xml.OutputBinaryCharacters(ctx, result, id)
	} else {
		text, warning, err = xml.OutputText(ctx, result)
	}
	if warning.Message != "" && env.XMLNotice != nil {
		env.XMLNotice(sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "XX001", Message: warning.Message, Detail: warning.Detail})
	}
	return text, xmlError(err)
}
func xmlLiteral(ctx context.Context, v Value) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	result, err := v.XML()
	if err != nil {
		return "", err
	}
	prefix := "XMLPARSE(CONTENT '"
	if result.RequestedMode() == xml.Document {
		prefix = "XMLPARSE(DOCUMENT '"
	}
	original := result.Original()
	if err := stringPadSize(1, int64(len(original))); err != nil {
		return "", err
	}
	size := int64(len(prefix)) + int64(len(original)) + 2
	for at := 0; at < len(original); {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		end := at + min(4096, len(original)-at)
		size += int64(strings.Count(original[at:end], "'"))
		at = end
	}
	if err := stringPadSize(1, size); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var out strings.Builder
	out.Grow(int(size))
	out.WriteString(prefix)
	for at := 0; at < len(original); {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		end := at + min(4096, len(original)-at)
		start := at
		for i := at; i < end; i++ {
			if original[i] == '\'' {
				out.WriteString(original[start:i])
				out.WriteString("''")
				start = i + 1
			}
		}
		out.WriteString(original[start:end])
		at = end
	}
	out.WriteString("')")
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return out.String(), nil
}
