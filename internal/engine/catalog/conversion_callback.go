package catalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// EncodingConversionCallback is inert catalog selection, not permission or a
// physical-session borrower. Mutable presentation does not select a new helper.
type EncodingConversionCallback struct {
	Ref        ids.ObjectRef
	Conversion *Conversion
	Helper     *Procedure
}

func (c EncodingConversionCallback) EncodingConversionHandler() sqltype.EncodingConversionHandler {
	return c.Helper.Conversion
}
func CaptureEncodingConversion(view Read, conversion *Conversion, helper *Procedure) (EncodingConversionCallback, error) {
	if view.Owner() == nil || conversion == nil || helper == nil {
		return EncodingConversionCallback{}, fmt.Errorf("stuntgres: missing selected encoding conversion")
	}
	current, ok := view.Conversion(conversion.Address())
	if !ok || current != conversion {
		return EncodingConversionCallback{}, ids.ErrCatalogReplaced
	}
	selected, ok := view.Procedure(conversion.Handler)
	if !ok || selected != helper || helper.Signature != ConversionSignature || helper.Result != 23 || !helper.Conversion.Supports(conversion.Source, conversion.Destination) {
		return EncodingConversionCallback{}, fmt.Errorf("stuntgres: invalid selected encoding conversion")
	}
	return EncodingConversionCallback{Ref: view.Owner().Domain.Ref(view.Epoch(), helper.Address()), Conversion: conversion, Helper: helper}, nil
}
