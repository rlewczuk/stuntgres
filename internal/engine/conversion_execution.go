package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
)

// Namespace order wins first, then the released default-index OID order within
// that namespace. Name shadowing is unrelated to directed default dispatch.
type conversionDefault struct {
	conversion *catalog.Conversion
	procedure  *catalog.Procedure
}

// A statement lazily projects its immutable snapshot exactly once, only for
// nonempty nonidentity conversion. Rows do no catalog scanning or path allocation.
func (s *StatementScope) conversionProcedure(source, destination int) (*catalog.Procedure, error) {
	if s == nil || s.inquiry.visibilityCatalog.State() == nil {
		return nil, fmt.Errorf("stuntgres: missing conversion execution snapshot")
	}
	if s.presentationOrZero().conversionDefaults == nil {
		defaults := make(map[[2]int]conversionDefault)
		for _, namespace := range s.inquiry.visibilityOtherPath {
			for _, descriptor := range s.inquiry.visibilityCatalog.Descriptors() {
				d, ok := descriptor.(*catalog.Conversion)
				if !ok || !d.DefaultConversion || d.Namespace() != namespace {
					continue
				}
				key := [2]int{d.Source, d.Destination}
				old := defaults[key].conversion
				if old == nil || old.Namespace() == namespace && d.OID < old.OID {
					procedure, ok := s.inquiry.visibilityCatalog.Procedure(d.Handler)
					if !ok || procedure.Conversion.Name() == "" || procedure.Signature != catalog.ConversionSignature || procedure.Result != 23 {
						return nil, fmt.Errorf("stuntgres: invalid conversion execution capability")
					}
					defaults[key] = conversionDefault{d, procedure}
				}
			}
		}
		s.presentation().conversionDefaults = defaults
	}
	return s.presentationOrZero().conversionDefaults[[2]int{source, destination}].procedure, nil
}
