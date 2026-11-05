package sqltype

import (
	"context"
	"errors"
	"fmt"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/xml"
)

// XML2Scalar converts a successful legacy operation to its exact SQL result.
// Input is TEXT, never a prevalidated SQL XML value.
func XML2Scalar(ctx context.Context, result *Descriptor, input, path string) (Value, error) {
	if result == nil {
		return Value{}, fmt.Errorf("invalid xml2 result descriptor")
	}
	var kind xml.LegacyScalarKind
	switch result.ID() {
	case Text:
		kind = xml.LegacyString
	case Float4:
		kind = xml.LegacyNumber
	case Bool:
		kind = xml.LegacyBoolean
	default:
		return Value{}, fmt.Errorf("invalid xml2 result type")
	}
	v, err := xml.XPathScalar(ctx, input, path, kind)
	if err != nil {
		var legacy *xml.LegacyXPathError
		if errors.As(err, &legacy) {
			return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "10608", Message: legacy.Error(), Detail: legacy.Detail}
		}
		return Value{}, xmlError(err)
	}
	if v.Null {
		return Null(result), nil
	}
	switch kind {
	case xml.LegacyString:
		return String(result, v.Text), nil
	case xml.LegacyNumber:
		return Value{typ: result, floating: float64(v.Number)}, nil
	default:
		return Boolean(result, v.Boolean), nil
	}
}

// XML2XSLT is operation-selected: TEXT also serves legacy xpath_string.
// Preserve the complete owned datum, including NUL and non-UTF8 output bytes.
func XML2XSLT(ctx context.Context, result *Descriptor, source, stylesheet, params string) (Value, error) {
	if result == nil || result.ID() != Text {
		return Value{}, fmt.Errorf("invalid xslt_process result descriptor")
	}
	v, err := xml.ProcessXSLT(ctx, source, stylesheet, params)
	if err != nil {
		var failure *xml.XSLTError
		if !errors.As(err, &failure) {
			return Value{}, err
		}
		code, primary := "10608", ""
		switch failure.Stage {
		case xml.XSLTSourceParse:
			code, primary = "2200M", "error parsing XML document"
		case xml.XSLTStyleParse:
			code, primary = "2200M", "error parsing stylesheet as XML document"
		case xml.XSLTCompile:
			primary = "failed to parse stylesheet"
		case xml.XSLTApply:
			primary = "failed to apply stylesheet"
		default:
			return Value{}, err
		}
		return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: primary, Detail: failure.Detail}
	}
	if v.Null {
		return Null(result), nil
	}
	return String(result, v.Text), nil
}

func XML2Valid(ctx context.Context, result *Descriptor, input string, env InputEnvironment) (Value, error) {
	if result == nil || result.ID() != Bool {
		return Value{}, fmt.Errorf("invalid xml_valid result descriptor")
	}
	valid, events, err := xml.ValidateText(ctx, input, env.XMLMode)
	xmlWarnings(events, env)
	if err != nil {
		return Value{}, xmlInputError(err, env)
	}
	return Boolean(result, valid), nil
}

// XML2Set maps the distinct set conversion and its one actual result notice.
func XML2Set(ctx context.Context, result *Descriptor, input, path, root, item, separator string, list bool, env InputEnvironment) (Value, error) {
	if result == nil || result.ID() != Text {
		return Value{}, fmt.Errorf("invalid xml2 set result descriptor")
	}
	kind := xml.LegacyNodeset
	if list {
		kind = xml.LegacyList
	}
	v, err := xml.XPathSet(ctx, input, path, root, item, separator, kind)
	if err != nil {
		var legacy *xml.LegacyXPathError
		if errors.As(err, &legacy) {
			return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "10608", Message: legacy.Error(), Detail: legacy.Detail}
		}
		return Value{}, xmlError(err)
	}
	if v.Null {
		return Null(result), nil
	}
	if v.Unsupported != 0 && env.XMLInput != nil {
		env.XMLInput.XMLInputWarning(sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf("unsupported XQuery result: %d", v.Unsupported)})
	}
	return String(result, v.Text), nil
}
