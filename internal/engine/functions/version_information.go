package functions

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/sqltype/unicode16"
)

const embeddedVersionBanner = "PostgreSQL 18.0 compatible; embedded stuntgres"

// The SQL inquiry uses major.minor; Unicode execution owns the full version.
var sqlUnicodeVersion = unicode16.Version[:strings.LastIndexByte(unicode16.Version, '.')]

type VersionInformationKind uint8

const (
	VersionInformationServer VersionInformationKind = iota + 1
	VersionInformationUnicode
	VersionInformationICU
)

func (kind VersionInformationKind) Name() string {
	switch kind {
	case VersionInformationServer:
		return "version"
	case VersionInformationUnicode:
		return "unicode_version"
	case VersionInformationICU:
		return "icu_unicode_version"
	}
	return ""
}

func VersionInformationKindForName(name string) VersionInformationKind {
	switch name {
	case "version":
		return VersionInformationServer
	case "unicode_version":
		return VersionInformationUnicode
	case "icu_unicode_version":
		return VersionInformationICU
	}
	return 0
}

func (kind VersionInformationKind) IntrinsicRule() expr.IntrinsicPropertyRule {
	if kind == VersionInformationServer {
		return expr.ExactStable
	}
	return expr.ExactImmutable
}

type VersionInformation struct {
	expr.Info
	Kind VersionInformationKind
}

func (e VersionInformation) Validate(intrinsicMatch bool) error {
	if e.Kind.Name() == "" || !e.Usage.Valid() || !e.Usage.Type.Concrete() || e.Usage.Type.ID() != sqltype.Text || e.Usage.Typmod != -1 || !intrinsicMatch {
		return fmt.Errorf("stuntgres: invalid version information expression")
	}
	return nil
}

func (e VersionInformation) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if err := e.Validate(c.FunctionPropertiesMatch(e.Property, e.Kind.IntrinsicRule())); err != nil {
		return sqltype.Value{}, err
	}
	switch e.Kind {
	case VersionInformationServer:
		return sqltype.String(e.Usage.Type, embeddedVersionBanner), nil
	case VersionInformationUnicode:
		return sqltype.String(e.Usage.Type, sqlUnicodeVersion), nil
	default:
		// No ICU provider participates in native Unicode execution.
		return sqltype.Null(e.Usage.Type), nil
	}
}

// VersionInformation implements expr.Composite as a leaf: no children, and Rebuild only takes
// the new metadata.
var _ expr.Composite = VersionInformation{}

func (e VersionInformation) Children() []expr.Expr { return nil }
func (e VersionInformation) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("VersionInformation", children, 0); err != nil {
		return nil, err
	}
	e.Info = info
	return e, nil
}

// IntrinsicVolatility is the analyzed volatility: the call is volatile or
// has no children, so nothing from eliminated arms can be retained.
func (e VersionInformation) IntrinsicVolatility() expr.Volatility { return e.Property.Volatility }

func (e VersionInformation) PlanningFold() expr.Fold { return expr.FoldNever }

var _ expr.Renderable = VersionInformation{}

// Render implements expr.Renderable.
func (e VersionInformation) Render() (expr.Rendering, error) {
	if err := e.Validate(expr.PropertiesMatch(e.Property, e.Kind.IntrinsicRule())); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: fmt.Sprintf("VersionInformation kind %d", e.Kind), Form: expr.FormLeaf, Text: e.Kind.Name() + "()"}, nil
}

// SameGroup implements expr.GroupIdentity.
func (e VersionInformation) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(VersionInformation)
	return ok && e.Kind == o.Kind
}

// GroupLeaf implements expr.GroupLeaf.
func (e VersionInformation) GroupLeaf() {}

// PreflightFold implements expr.Preflighting.
func (e VersionInformation) PreflightFold() expr.Preflight { return expr.PreflightKeep }

// LooksLikeFunction implements expr.FunctionSyntax.
func (e VersionInformation) LooksLikeFunction() bool { return true }

func (e VersionInformation) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{}, true, e.Validate(expr.PropertiesMatch(e.Property, e.Kind.IntrinsicRule()))
}
