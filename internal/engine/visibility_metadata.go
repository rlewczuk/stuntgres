package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type collationDescriptor struct {
	OID                                    ids.OID
	ObjectName                             string
	Encoding                               int32
	ObjectNamespace                        ids.ObjectAddress
	ObjectRevision                         ids.DefinitionRevision
	Owner                                  authorization.PrincipalRef
	Provider                               string
	Deterministic                          bool
	Collate, CType, Locale, Rules, Version *string
	Semantics                              uint32
}
type conversionDescriptor struct {
	OID                 ids.OID
	ObjectName          string
	ObjectNamespace     ids.ObjectAddress
	ObjectRevision      ids.DefinitionRevision
	Owner               authorization.PrincipalRef
	Source, Destination int
	Handler             ids.ObjectAddress
	DefaultConversion   bool
}

func (d *collationDescriptor) Address() ids.ObjectAddress {
	return ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: d.OID}
}
func (d *collationDescriptor) Revision() ids.DefinitionRevision {
	if d.ObjectRevision == 0 {
		return 1
	}
	return d.ObjectRevision
}
func (d *collationDescriptor) Namespace() ids.ObjectAddress {
	if d.ObjectNamespace != (ids.ObjectAddress{}) {
		return d.ObjectNamespace
	}
	return ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
}
func (d *collationDescriptor) Name() string       { return d.ObjectName }
func (*collationDescriptor) Kind() ids.ObjectKind { return ids.CollationObject }
func (d *conversionDescriptor) Address() ids.ObjectAddress {
	return ids.ObjectAddress{ClassID: ids.ConversionClassID, ObjectID: d.OID}
}
func (d *conversionDescriptor) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (d *conversionDescriptor) Namespace() ids.ObjectAddress     { return d.ObjectNamespace }
func (d *conversionDescriptor) Name() string                     { return d.ObjectName }
func (*conversionDescriptor) Kind() ids.ObjectKind               { return ids.ConversionObject }

func collationText(s string) *string { return &s }

var builtinCollations = [...]collationDescriptor{
	{OID: 100, ObjectName: "default", Encoding: -1, Provider: "d", Deterministic: true, Semantics: 950},
	{OID: 950, ObjectName: "C", Encoding: -1, Provider: "c", Deterministic: true, Collate: collationText("C"), CType: collationText("C"), Semantics: 950},
	{OID: 951, ObjectName: "POSIX", Encoding: -1, Provider: "c", Deterministic: true, Collate: collationText("POSIX"), CType: collationText("POSIX"), Semantics: 950},
	{OID: 962, ObjectName: "ucs_basic", Encoding: 6, Provider: "b", Deterministic: true, Locale: collationText("C"), Version: collationText("1")},
	{OID: 963, ObjectName: "unicode", Encoding: -1, Provider: "i", Deterministic: true, Locale: collationText("und")},
	{OID: 811, ObjectName: "pg_c_utf8", Encoding: 6, Provider: "b", Deterministic: true, Locale: collationText("C.UTF-8"), Version: collationText("1"), Semantics: 811},
	{OID: 6411, ObjectName: "pg_unicode_fast", Encoding: 6, Provider: "b", Deterministic: true, Locale: collationText("PG_UNICODE_FAST"), Version: collationText("1"), Semantics: 6411},
}

func (d *collationDescriptor) Seeded() bool {
	for i := range builtinCollations {
		if d.OID == builtinCollations[i].OID {
			return true
		}
	}
	return false
}
func (d *collationDescriptor) Usage() (sqltype.CollationRef, error) {
	return sqltype.RegisteredCollation(uint32(d.OID), d.Semantics)
}

type collationProvider struct{}
type conversionProvider struct{}

func (collationProvider) ClassID() ids.OID  { return ids.CollationClassID }
func (conversionProvider) ClassID() ids.OID { return ids.ConversionClassID }
func (collationProvider) Validate(view CatalogRead, descriptor Descriptor) error {
	d, ok := descriptor.(*collationDescriptor)
	if !ok || d == nil {
		return fmt.Errorf("stuntgres: invalid collation descriptor")
	}
	if d.Seeded() {
		found := false
		for _, fixed := range builtinCollations {
			if d.OID != fixed.OID {
				continue
			}
			invariant := *d
			invariant.ObjectName, invariant.ObjectNamespace, invariant.ObjectRevision, invariant.Owner = fixed.Name(), fixed.ObjectNamespace, fixed.ObjectRevision, fixed.Owner
			found = invariant == fixed && d.Name() != ""
			break
		}
		if !found {
			return fmt.Errorf("stuntgres: unsupported collation identity")
		}
	} else {
		if d.OID == 0 || d.Name() == "" || d.ObjectRevision == 0 || d.Encoding != 6 && d.Encoding != -1 || !d.Deterministic || d.Rules != nil {
			return fmt.Errorf("stuntgres: invalid collation properties")
		}
		if d.Provider == "c" && d.Version != nil || d.Provider == "b" && d.Version == nil {
			return fmt.Errorf("stuntgres: invalid recorded collation version")
		}
		mode, err := collationSemantics(d)
		if err != nil || mode != d.Semantics {
			return fmt.Errorf("stuntgres: invalid collation provider")
		}
		if _, err = d.Usage(); err != nil {
			return err
		}
	}
	return validateInquiryIdentity(view, d)
}
func (conversionProvider) Validate(view CatalogRead, descriptor Descriptor) error {
	d, ok := descriptor.(*conversionDescriptor)
	if !ok || d == nil {
		return fmt.Errorf("stuntgres: invalid conversion descriptor")
	}
	if d.OID == 0 || d.Name() == "" || d.Revision() == 0 || d.Source <= 0 || d.Destination <= 0 || sqltype.EncodingName(d.Source) == "" || sqltype.EncodingName(d.Destination) == "" {
		return fmt.Errorf("stuntgres: invalid conversion properties")
	}
	if fixed := conversionSeed(d.OID); fixed != nil {
		if d.Source != fixed.Source || d.Destination != fixed.Destination || d.Handler != fixed.Handler || d.DefaultConversion != fixed.DefaultConversion {
			return fmt.Errorf("stuntgres: unsupported seeded conversion capability")
		}
	}
	procedure, ok := view.Procedure(d.Handler)
	if !ok || procedure.Signature != conversionSignature || procedure.Result != 23 || procedure.Conversion.Name() == "" {
		return fmt.Errorf("stuntgres: unavailable conversion routine")
	}
	registered, ok := sqltype.RegisteredEncodingConversion(procedure.Name())
	if !ok || registered != procedure.Conversion {
		return fmt.Errorf("stuntgres: invalid conversion routine capability")
	}
	if !registered.Supports(d.Source, d.Destination) {
		return fmt.Errorf("stuntgres: conversion pair does not match real routine")
	}
	if err := validateInquiryIdentity(view, d); err != nil {
		return err
	}
	namespace := view.MustNamespace(d.Namespace())
	if namespace.Temporary == (ids.SessionToken{}) {
		handlerNamespace := view.MustNamespace(procedure.Namespace())
		if handlerNamespace.Temporary != (ids.SessionToken{}) {
			return fmt.Errorf("stuntgres: durable conversion has temporary routine")
		}
	}
	return nil
}
func validateInquiryIdentity(view CatalogRead, d Descriptor) error {
	namespace, ok := view.Descriptor(d.Namespace())
	if !ok || namespace.Kind() != ids.NamespaceObject {
		return fmt.Errorf("stuntgres: inquiry namespace is missing")
	}
	if view.NameAddress(descriptorNameKey(d)) != d.Address() {
		return fmt.Errorf("stuntgres: invalid inquiry identity")
	}
	return nil
}

// A namespace's usable exact encoding beats its fallback. Callers visit
// namespaces in path order, so an earlier fallback beats a later exact entry.
// This helper also covers synthetic multi-encoding candidates.
func selectCollation(candidates []collationCandidate, encoding int32) ids.ObjectAddress {
	var fallback ids.ObjectAddress
	for _, candidate := range candidates {
		if candidate.encoding == encoding {
			return candidate.address
		}
		if candidate.encoding == -1 {
			fallback = candidate.address
		}
	}
	return fallback
}

type collationCandidate struct {
	address  ids.ObjectAddress
	encoding int32
}

// Native fixed names are unique. Keep the encoding-bearing lookup inline,
// avoiding an index copy, catalog scan or allocation during row evaluation.
func visibleCollation(view CatalogRead, path []ids.ObjectAddress, d Descriptor) bool {
	for _, namespace := range path {
		a := view.NameAddress(ids.NameKey{ClassID: ids.CollationClassID, Namespace: namespace, Name: d.Name()})
		candidate, ok := view.Collation(a)
		if !ok {
			continue
		}
		one := [1]collationCandidate{{a, candidate.Encoding}}
		if selected := selectCollation(one[:], 6); selected != (ids.ObjectAddress{}) {
			return selected == d.Address()
		}
	}
	return false
}
