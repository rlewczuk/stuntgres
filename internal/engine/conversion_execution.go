package engine

import (
	"fmt"
)

// Namespace order wins first, then the released default-index OID order within
// that namespace. Name shadowing is unrelated to directed default dispatch.
type conversionDefault struct {
	conversion *conversionDescriptor
	procedure  *procedureDescriptor
}

// A statement lazily projects its immutable snapshot exactly once, only for
// nonempty nonidentity conversion. Rows do no catalog scanning or path allocation.
func (s *StatementScope) conversionProcedure(source, destination int) (*procedureDescriptor, error) {
	if s == nil || s.visibilityCatalog.state == nil {
		return nil, fmt.Errorf("stuntgres: missing conversion execution snapshot")
	}
	if s.conversionDefaults == nil {
		defaults := make(map[[2]int]conversionDefault)
		for _, namespace := range s.visibilityOtherPath {
			for _, descriptor := range s.visibilityCatalog.Descriptors() {
				d, ok := descriptor.(*conversionDescriptor)
				if !ok || !d.defaultConversion || d.Namespace() != namespace {
					continue
				}
				key := [2]int{d.source, d.destination}
				old := defaults[key].conversion
				if old == nil || old.Namespace() == namespace && d.oid < old.oid {
					procedure, ok := s.visibilityCatalog.Procedure(d.handler)
					if !ok || procedure.conversion.Name() == "" || procedure.signature != conversionSignature || procedure.result != 23 {
						return nil, fmt.Errorf("stuntgres: invalid conversion execution capability")
					}
					defaults[key] = conversionDefault{d, procedure}
				}
			}
		}
		s.conversionDefaults = defaults
	}
	return s.conversionDefaults[[2]int{source, destination}].procedure, nil
}
