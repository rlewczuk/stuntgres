package catalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type Collation struct {
	OID                                    ids.OID
	Extension                              ids.ObjectAddress
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

type Conversion struct {
	OID                 ids.OID
	Extension           ids.ObjectAddress
	ObjectName          string
	ObjectNamespace     ids.ObjectAddress
	ObjectRevision      ids.DefinitionRevision
	Owner               authorization.PrincipalRef
	Source, Destination int
	Handler             ids.ObjectAddress
	DefaultConversion   bool
}

func (d *Collation) Address() ids.ObjectAddress {
	return ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: d.OID}
}

func (d *Collation) Revision() ids.DefinitionRevision {
	if d.ObjectRevision == 0 {
		return 1
	}
	return d.ObjectRevision
}

func (d *Collation) Namespace() ids.ObjectAddress {
	if d.ObjectNamespace != (ids.ObjectAddress{}) {
		return d.ObjectNamespace
	}
	return ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
}

func (d *Collation) Name() string { return d.ObjectName }

func (*Collation) Kind() ids.ObjectKind { return ids.CollationObject }

func (d *Conversion) Address() ids.ObjectAddress {
	return ids.ObjectAddress{ClassID: ids.ConversionClassID, ObjectID: d.OID}
}

func (d *Conversion) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (d *Conversion) Namespace() ids.ObjectAddress { return d.ObjectNamespace }

func (d *Conversion) Name() string { return d.ObjectName }

func (*Conversion) Kind() ids.ObjectKind { return ids.ConversionObject }

func CollationText(s string) *string { return &s }

var BuiltinCollations = [...]Collation{
	{OID: 100, ObjectName: "default", Encoding: -1, Provider: "d", Deterministic: true, Semantics: 950},
	{OID: 950, ObjectName: "C", Encoding: -1, Provider: "c", Deterministic: true, Collate: CollationText("C"), CType: CollationText("C"), Semantics: 950},
	{OID: 951, ObjectName: "POSIX", Encoding: -1, Provider: "c", Deterministic: true, Collate: CollationText("POSIX"), CType: CollationText("POSIX"), Semantics: 950},
	{OID: 962, ObjectName: "ucs_basic", Encoding: 6, Provider: "b", Deterministic: true, Locale: CollationText("C"), Version: CollationText("1")},
	{OID: 963, ObjectName: "unicode", Encoding: -1, Provider: "i", Deterministic: true, Locale: CollationText("und")},
	{OID: 811, ObjectName: "pg_c_utf8", Encoding: 6, Provider: "b", Deterministic: true, Locale: CollationText("C.UTF-8"), Version: CollationText("1"), Semantics: 811},
	{OID: 6411, ObjectName: "pg_unicode_fast", Encoding: 6, Provider: "b", Deterministic: true, Locale: CollationText("PG_UNICODE_FAST"), Version: CollationText("1"), Semantics: 6411},
}

func (d *Collation) Seeded() bool {
	for i := range BuiltinCollations {
		if d.OID == BuiltinCollations[i].OID {
			return true
		}
	}
	return false
}

func (d *Collation) Usage() (sqltype.CollationRef, error) {
	return sqltype.RegisteredCollation(uint32(d.OID), d.Semantics)
}

type CollationProvider struct{}

type ConversionProvider struct{}

func (CollationProvider) ClassID() ids.OID { return ids.CollationClassID }

func (ConversionProvider) ClassID() ids.OID { return ids.ConversionClassID }

func (CollationProvider) Validate(view Read, descriptor Descriptor) error {
	d, ok := descriptor.(*Collation)
	if !ok || d == nil {
		return fmt.Errorf("stuntgres: invalid collation descriptor")
	}
	if d.Seeded() {
		found := false
		for _, fixed := range BuiltinCollations {
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
		mode, err := CollationSemantics(d)
		if err != nil || mode != d.Semantics {
			return fmt.Errorf("stuntgres: invalid collation provider")
		}
		if _, err = d.Usage(); err != nil {
			return err
		}
	}
	return validateInquiryIdentity(view, d)
}

func (ConversionProvider) Validate(view Read, descriptor Descriptor) error {
	d, ok := descriptor.(*Conversion)
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
	if !ok || procedure.Signature != ConversionSignature || procedure.Result != 23 || procedure.Conversion.Name() == "" {
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

func validateInquiryIdentity(view Read, d Descriptor) error {
	namespace, ok := view.Descriptor(d.Namespace())
	if !ok || namespace.Kind() != ids.NamespaceObject {
		return fmt.Errorf("stuntgres: inquiry namespace is missing")
	}
	if view.NameAddress(DescriptorNameKey(d)) != d.Address() {
		return fmt.Errorf("stuntgres: invalid inquiry identity")
	}
	return nil
}
