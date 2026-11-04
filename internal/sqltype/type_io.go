package sqltype

import "context"

// TypeIODirection is a genuine source codec operation, not a generic datum
// conversion. Unsupported binary directions retain their existing diagnostics.
type TypeIODirection uint8

const (
	TypeIOInput TypeIODirection = iota + 1
	TypeIOOutput
	TypeIOReceive
	TypeIOSend
)

// TypeIOBoundary is borrowed by an actual binding/protocol/execution owner.
// Recursive array/composite/domain codecs carry this same operation environment
// to actual custom leaves; no session or callback is stored in a Descriptor.
type TypeIOBoundary interface {
	EnterTypeIO(context.Context, *Descriptor, TypeIODirection) (RoutineBodyToken, error)
}

// typeIOPurpose distinguishes a codec callback from the exact scalar leaf
// already entered by an explicit SQL I/O call. It never propagates recursively:
// child codecs use InputWithEnvironment/OutputWithEnvironment as usual.
type typeIOPurpose uint8

const (
	implicitTypeIO typeIOPurpose = iota
	enteredTypeIOLeaf
)

// HasInstalledScalarIO identifies a represented scalar kernel family, never its
// catalog ownership or permission authority. Catalog's canonical cohort owner
// authenticates the exact descriptor and original helper association.
func (d *Descriptor) HasInstalledScalarIO() bool {
	return d != nil && !d.IsDomain() && (d.IsSeg() || d.IsHstore() || d.IsQueryInt() || d.IsIntbigGkey() || d.IsLquery() || d.IsLtree() || d.IsLtxtquery() || d.IsCube() || d.IsCitext() || d.IsISN())
}

// ContainsInstalledScalarIO follows the same recursive codec graph as input and
// output. Domain and array wrappers are original core operations; only an
// actually visited represented custom scalar leaf has an installed callback.
func ContainsInstalledScalarIO(d *Descriptor) bool {
	if d == nil {
		return false
	}
	d = d.BaseType()
	if d.HasInstalledScalarIO() {
		return true
	}
	if d.IsArray() {
		return ContainsInstalledScalarIO(d.Element())
	}
	if d.IsComposite() {
		for i := range d.CompositeLen() {
			if ContainsInstalledScalarIO(d.CompositeMember(i).Usage.Type) {
				return true
			}
		}
	}
	return false
}
