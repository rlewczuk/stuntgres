package engine

import (
	"fmt"

	"stuntgres/internal/sqltype"
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
			for _, descriptor := range s.visibilityCatalog.state.descriptors {
				d, ok := descriptor.(*conversionDescriptor)
				if !ok || !d.defaultConversion || d.Namespace() != namespace {
					continue
				}
				key := [2]int{d.source, d.destination}
				old := defaults[key].conversion
				if old == nil || old.Namespace() == namespace && d.oid < old.oid {
					procedure, ok := s.visibilityCatalog.state.descriptors[d.handler].(*procedureDescriptor)
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

func (e binaryExpression) evalConversion(c *EvalContext, values []sqltype.Value) (sqltype.Value, error) {
	raw, sourceName, destinationName, null, err := e.plan.EncodingInput(c.Context, values...)
	if err != nil {
		return sqltype.Value{}, err
	}
	if null {
		return sqltype.Null(e.usage.Type), nil
	}
	source, destination, unchanged, err := sqltype.PrepareBinaryEncoding(c.Context, raw, sourceName, destinationName)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !unchanged {
		procedure, err := c.liveScope().conversionProcedure(source, destination)
		if err != nil {
			return sqltype.Value{}, err
		}
		if procedure == nil {
			return sqltype.Value{}, sqltype.MissingBinaryConversion(source, destination)
		}
		var consumed int
		output, consumed, err := procedure.conversion.ConvertValidated(c.Context, raw, source, destination)
		if err != nil {
			return sqltype.Value{}, err
		}
		if consumed != len(raw) {
			return sqltype.Value{}, sqlError("XX000", "encoding conversion function returned incorrect result")
		}
		raw = output
	}
	return e.plan.EncodingResult(raw)
}
