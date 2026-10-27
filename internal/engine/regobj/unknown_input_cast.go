package regobj

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// UnknownInputDeclaration names only the canonical metadata target of an
// authenticated logical IO cast. It grants no executable UNKNOWN codec/usage.
func UnknownInputDeclaration(view catalog.Read, reader catalog.DefinitionReader, target sqltype.Usage) (string, error) {
	if target.Type == nil || target.Type.ID() != sqltype.Unknown || target != target.Type.Usage() {
		return "", fmt.Errorf("stuntgres: invalid unknown input target usage")
	}
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Unknown)}
	d, ok := catalog.DescriptorAt(view, reader, address)
	if !ok {
		return "", fmt.Errorf("stuntgres: missing unknown input target")
	}
	t, ok := d.(*catalog.Type)
	if !ok || t.SQLType != target.Type {
		return "", fmt.Errorf("stuntgres: foreign unknown input target")
	}
	ns, ok := catalog.DescriptorAt(view, reader, d.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing unknown input namespace")
	}
	return catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(d.Name()), nil
}

// ValidateUnknownInputTargets authenticates retained descriptor generations;
// transparent steps remain metadata, never executable pseudo codecs.
func ValidateUnknownInputTargets(view catalog.Read, reader catalog.DefinitionReader, target *sqltype.Descriptor, steps []expr.TransparentInputCast) error {
	validate := func(target *sqltype.Descriptor) error {
		if !expr.TransparentUnknownTarget(target) {
			return fmt.Errorf("stuntgres: invalid unknown input target")
		}
		current, ok := catalog.DefinitionTypeAt(view, reader, target.ID())
		if !ok || current != target {
			return fmt.Errorf("stuntgres: foreign unknown input target generation")
		}
		return nil
	}
	if target != nil {
		if err := validate(target); err != nil {
			return err
		}
	}
	for _, step := range steps {
		if err := validate(step.Target); err != nil {
			return err
		}
	}
	return nil
}
