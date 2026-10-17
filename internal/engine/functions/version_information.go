package functions

import (
	"fmt"
	"strings"

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

func (kind VersionInformationKind) IntrinsicRule() IntrinsicPropertyRule {
	if kind == VersionInformationServer {
		return ExactStable
	}
	return ExactImmutable
}

type VersionInformationContext[P any] interface {
	Context
	FunctionPropertiesMatch(P, IntrinsicPropertyRule) bool
}

type VersionInformation[C VersionInformationContext[P], P any] struct {
	Info[P]
	Kind VersionInformationKind
}

func (e VersionInformation[C, P]) Validate(intrinsicMatch bool) error {
	if e.Kind.Name() == "" || !e.Usage.Valid() || !e.Usage.Type.Concrete() || e.Usage.Type.ID() != sqltype.Text || e.Usage.Typmod != -1 || !intrinsicMatch {
		return fmt.Errorf("stuntgres: invalid version information expression")
	}
	return nil
}

func (e VersionInformation[C, P]) Eval(c C) (sqltype.Value, error) {
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
