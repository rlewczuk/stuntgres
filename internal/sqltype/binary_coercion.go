package sqltype

import (
	"context"
	"fmt"
)

type binaryCoercionMethod uint8

const (
	binaryCoercionNone binaryCoercionMethod = iota
	binaryCoercion32
	binaryCoercion64
	binaryCoercionOctets
	binaryCoercionBits
	binaryCoercionNetwork
	binaryCoercionPoints
	binaryCoercionTriple
)

// BinaryCoercionPlan captures descriptor authority and a proven native datum
// mapping. It is immutable and catalog-free. PostgreSQL DDL permissions, physical
// attribute equality, category restrictions and duplicate edges belong to core.
type BinaryCoercionPlan struct {
	source, target *Descriptor
	method         binaryCoercionMethod
}

// PlanBinaryCoercion selects a representation mapping, never a numerical cast,
// input/output roundtrip, protocol codec or generic logical-identity retag.
func PlanBinaryCoercion(source, target *Descriptor) (BinaryCoercionPlan, error) {
	p := BinaryCoercionPlan{source: source, target: target}
	if source == nil || target == nil || !source.Concrete() || !target.Concrete() ||
		source.IsDomain() || target.IsDomain() || source.IsEnum() || target.IsEnum() ||
		source.IsArray() || target.IsArray() || source.IsComposite() || target.IsComposite() {
		return BinaryCoercionPlan{}, binaryRepresentationUnsupported()
	}
	s, t := source.id, target.id
	switch {
	case binaryWord32(s) && binaryWord32(t):
		p.method = binaryCoercion32
	case binaryWord64(s) && binaryWord64(t):
		p.method = binaryCoercion64
	case binaryOctets(source) && binaryOctets(target):
		p.method = binaryCoercionOctets
	case isBitString(s) && isBitString(t):
		p.method = binaryCoercionBits
	case IsNetwork(s) && IsNetwork(t):
		p.method = binaryCoercionNetwork
	case (s == Box || s == Lseg) && (t == Box || t == Lseg):
		p.method = binaryCoercionPoints
	case (s == Line || s == Circle) && (t == Line || t == Circle):
		p.method = binaryCoercionTriple
	default:
		return BinaryCoercionPlan{}, binaryRepresentationUnsupported()
	}
	return p, nil
}

// PlanDeclaredBinaryCoercion adapts a previously authorized binary edge to the
// ordinary immutable conversion carrier. Permission belongs to the catalog;
// this operation proves and captures only the physical datum mapping.
func PlanDeclaredBinaryCoercion(source, target Usage) (CoercionPlan, error) {
	if !source.Valid() || !target.Valid() || target.Type.IsDomain() {
		return CoercionPlan{}, fmt.Errorf("invalid declared binary coercion usage")
	}
	from := source.BaseUsage()
	binary, err := PlanBinaryCoercion(from.Type, target.Type)
	if err != nil {
		return CoercionPlan{}, err
	}
	p := CoercionPlan{source: from, target: target, context: Implicit, binary: binary}
	if source.Type.IsDomain() {
		p.logicalSource, p.logicalTarget = source.Type, target.Type
	}
	return p, nil
}

func binaryRepresentationUnsupported() error {
	return failure("0A000", "binary cast representation is not supported")
}

func binaryValueUnsupported() error {
	return failure("0A000", "binary cast value is not representable in embedded mode")
}

// Apply checks exact captured source ownership even for NULL. No caller can use
// a same-OID descriptor from another catalog generation as datum authority.
func (p BinaryCoercionPlan) Apply(ctx context.Context, value Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.method == binaryCoercionNone || p.source == nil || p.target == nil || value.typ != p.source {
		return Value{}, fmt.Errorf("invalid binary coercion value")
	}
	if value.null {
		return Null(p.target), nil
	}
	switch p.method {
	case binaryCoercion32:
		word, err := binaryEncode32(value)
		if err != nil {
			return Value{}, err
		}
		return binaryDecode32(p.target, word)
	case binaryCoercion64:
		return binaryDecode64(p.target, binaryEncode64(value))
	case binaryCoercionOctets:
		if p.source.id == Bytea && p.target.id != Bytea {
			if err := binaryTextValid(ctx, value.text); err != nil {
				return Value{}, err
			}
		}
		// JSON's internal representation is text, not a parsed JSONB tree.
		// In particular, this must not invoke JSON input or BPCHAR trimming.
		return Value{typ: p.target, text: value.text}, nil
	case binaryCoercionBits:
		return Value{typ: p.target, text: value.text}, nil
	case binaryCoercionNetwork:
		if !value.network.valid(p.target.id == Cidr) {
			return Value{}, binaryValueUnsupported()
		}
		return Value{typ: p.target, network: value.network}, nil
	case binaryCoercionPoints:
		if value.geometry == nil {
			return Value{}, binaryValueUnsupported()
		}
		// BOX input normalization is not part of a binary relabel.
		return Value{typ: p.target, geometry: value.geometry}, nil
	case binaryCoercionTriple:
		return binaryGeometryTriple(value, p.target)
	}
	return Value{}, binaryRepresentationUnsupported()
}

func binaryOctets(d *Descriptor) bool {
	return IsCharacter(d.id) || d.id == JSON || d.id == Bytea || d.IsCitext()
}
