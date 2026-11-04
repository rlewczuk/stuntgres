package catalog

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InstalledTypeIO returns the actual declared text input/output procedures of a
// supported installed scalar cohort, including detached original installations.
// It is the common metadata and invocation association: no name lookup, inferred
// procedure OID, mutable side map or fabricated executable descriptor is used.
// These native scalar codecs have no binary receive/send implementation; their
// existing unsupported-binary diagnostic is not a simulated callback.
func InstalledTypeIO(view Read, d *Type) (input, output *Procedure, installed bool, err error) {
	if d == nil || d.SQLType == nil {
		return nil, nil, false, fmt.Errorf("stuntgres: missing type codec owner")
	}
	typ := d.SQLType
	if !typ.HasInstalledScalarIO() {
		return nil, nil, false, nil
	}
	installation, ok := OriginInstallation(d)
	if !ok || d.Origin.Declaration+2 >= len(installation.Members) {
		return nil, nil, false, fmt.Errorf("stuntgres: missing installed scalar role")
	}
	owner := view.Owner()
	if owner == nil || owner.Providers[ids.TypeClassID] == nil {
		return nil, nil, false, fmt.Errorf("stuntgres: missing installed scalar provider owner")
	}
	if err = owner.Providers[ids.TypeClassID].Validate(view, d); err != nil {
		return nil, nil, false, err
	}
	slot := d.Origin.Declaration
	input, ok = view.Procedure(installation.Members[slot+1])
	if !ok || !SameInstallation(input, installation) {
		return nil, nil, false, fmt.Errorf("stuntgres: missing installed type provider")
	}
	output, ok = view.Procedure(installation.Members[slot+2])
	var signature [10]byte
	expected := strconv.AppendUint(signature[:0], uint64(d.Address().ObjectID), 10)
	if !ok || !SameInstallation(output, installation) || input.Signature != "2275" || input.Result != d.Address().ObjectID || string(output.Signature) != string(expected) || output.Result != 2275 {
		return nil, nil, false, fmt.Errorf("stuntgres: invalid installed type codec relationship")
	}
	provider := owner.Providers[ids.ProcedureClassID]
	if provider == nil {
		return nil, nil, false, fmt.Errorf("stuntgres: missing registered procedure provider")
	}
	for _, procedure := range [...]*Procedure{input, output} {
		if procedure.Implementation == nil {
			return nil, nil, false, fmt.Errorf("stuntgres: missing installed codec declaration owner")
		}
		if err = provider.Validate(view, procedure); err != nil {
			return nil, nil, false, err
		}
	}
	return input, output, true, nil
}

// TypeIOCallback is a genuine captured direct codec operation, not SQL EXECUTE
// permission. The immutable source read authenticates the exact type generation
// and original declared helper; the actual borrowed owner chooses its authority.
type TypeIOCallback struct {
	Read      Read
	Type      *Type
	Helper    *Procedure
	Direction sqltype.TypeIODirection
}

func CaptureTypeIO(view Read, descriptor *sqltype.Descriptor, direction sqltype.TypeIODirection) (TypeIOCallback, error) {
	if descriptor == nil || !descriptor.HasInstalledScalarIO() {
		return TypeIOCallback{}, fmt.Errorf("stuntgres: invalid installed type I/O source")
	}
	typ, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(descriptor.ID())})
	if !ok || typ.SQLType != descriptor {
		return TypeIOCallback{}, fmt.Errorf("stuntgres: foreign type I/O generation")
	}
	input, output, installed, err := InstalledTypeIO(view, typ)
	if err != nil {
		return TypeIOCallback{}, err
	}
	if !installed {
		return TypeIOCallback{}, fmt.Errorf("stuntgres: missing installed type I/O association")
	}
	var helper *Procedure
	switch direction {
	case sqltype.TypeIOInput:
		helper = input
	case sqltype.TypeIOOutput:
		helper = output
	default:
		return TypeIOCallback{}, fmt.Errorf("stuntgres: unsupported installed type I/O direction")
	}
	return TypeIOCallback{Read: view, Type: typ, Helper: helper, Direction: direction}, nil
}
