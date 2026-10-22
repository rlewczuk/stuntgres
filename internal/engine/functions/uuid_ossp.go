package functions

import "stuntgres/internal/engine/expr"

// UUIDOsspMember describes one strict, parallel-safe UUID-returning overload.
type UUIDOsspMember struct {
	Name, Selector, Signature string
	Volatility                expr.Volatility
	ArgumentNames             [2]string
}

// UUIDOsspMembers preserves installation order and identities of the original
// eight members; namespace/name generation adds exactly two documented overloads.
var UUIDOsspMembers = [...]UUIDOsspMember{
	{Name: "uuid_generate_v4", Selector: "uuidv4", Volatility: expr.Volatile},
	{Name: "uuid_nil", Selector: "uuid_nil", Volatility: expr.Immutable},
	{Name: "uuid_ns_dns", Selector: "uuid_ns_dns", Volatility: expr.Immutable},
	{Name: "uuid_ns_url", Selector: "uuid_ns_url", Volatility: expr.Immutable},
	{Name: "uuid_ns_oid", Selector: "uuid_ns_oid", Volatility: expr.Immutable},
	{Name: "uuid_ns_x500", Selector: "uuid_ns_x500", Volatility: expr.Immutable},
	{Name: "uuid_generate_v1", Selector: "uuid_generate_v1", Volatility: expr.Volatile},
	{Name: "uuid_generate_v1mc", Selector: "uuid_generate_v1mc", Volatility: expr.Volatile},
	{Name: "uuid_generate_v3", Selector: "uuid_generate_v3", Signature: "2950,25", Volatility: expr.Immutable, ArgumentNames: [2]string{"namespace", "name"}},
	{Name: "uuid_generate_v5", Selector: "uuid_generate_v5", Signature: "2950,25", Volatility: expr.Immutable, ArgumentNames: [2]string{"namespace", "name"}},
}

func UUIDOsspLookup(name string) (UUIDOsspMember, bool) {
	for _, member := range UUIDOsspMembers {
		if member.Name == name {
			return member, true
		}
	}
	return UUIDOsspMember{}, false
}

func uuidOsspConstant(name string) ([16]byte, bool) {
	var discriminator byte
	switch name {
	case "uuid_nil":
		return [16]byte{}, true
	case "uuid_ns_dns":
		discriminator = 0x10
	case "uuid_ns_url":
		discriminator = 0x11
	case "uuid_ns_oid":
		discriminator = 0x12
	case "uuid_ns_x500":
		discriminator = 0x14
	default:
		return [16]byte{}, false
	}
	return [16]byte{0x6b, 0xa7, 0xb8, discriminator, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}, true
}
