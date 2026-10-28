package core

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Namespace order wins first, then the released default-index OID order within
// that namespace. Name shadowing is unrelated to directed default dispatch.
type conversionDefault struct {
	conversion *catalog.Conversion
	procedure  *catalog.Procedure
}

// A statement lazily resolves each demanded pair from its immutable snapshot.
// Later rows reuse the result (including absence) without catalog/path work.
func (s *StatementScope) conversionProcedure(source, destination int) (*catalog.Procedure, error) {
	if s == nil || s.Inquiry.visibilityCatalog.State() == nil {
		return nil, fmt.Errorf("stuntgres: missing conversion execution snapshot")
	}
	key := [2]int{source, destination}
	if selected, ok := s.presentationOrZero().conversionDefaults[key]; ok {
		return selected.procedure, nil
	}
	selected, err := resolveConversionDefault(s.Inquiry.visibilityCatalog, s.Inquiry.visibilityOtherPath, source, destination)
	if err != nil {
		return nil, err
	}
	if s.presentationOrZero().conversionDefaults == nil {
		s.presentation().conversionDefaults = make(map[[2]int]conversionDefault)
	}
	s.presentation().conversionDefaults[key] = selected
	return selected.procedure, nil
}

// Namespace priority, then the lowest conversion OID in that namespace, is the
// single default authority for both SQL conversion and client/COPY admission.
func resolveConversionDefault(view catalog.Read, path []ids.ObjectAddress, source, destination int) (conversionDefault, error) {
	for _, namespace := range path {
		var selected *catalog.Conversion
		for _, descriptor := range view.Descriptors() {
			d, ok := descriptor.(*catalog.Conversion)
			if ok && d.DefaultConversion && d.Namespace() == namespace && d.Source == source && d.Destination == destination && (selected == nil || d.OID < selected.OID) {
				selected = d
			}
		}
		if selected != nil {
			procedure, ok := view.Procedure(selected.Handler)
			if !ok || procedure.Conversion.Name() == "" || procedure.Signature != catalog.ConversionSignature || procedure.Result != 23 {
				return conversionDefault{}, fmt.Errorf("stuntgres: invalid conversion execution capability")
			}
			return conversionDefault{selected, procedure}, nil
		}
	}
	return conversionDefault{}, nil
}
