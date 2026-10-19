package functions

import "stuntgres/internal/engine/expr"

// UUIDOsspMembers is the bounded native uuid-ossp 1.1 inventory. Every member
// has an empty signature and a strict, parallel-safe UUID result.
var UUIDOsspMembers = [...]struct {
	Name, Selector string
	Volatility     expr.Volatility
}{
	{"uuid_generate_v4", "uuidv4", expr.Volatile},
	{"uuid_nil", "uuid_nil", expr.Immutable},
	{"uuid_ns_dns", "uuid_ns_dns", expr.Immutable},
	{"uuid_ns_url", "uuid_ns_url", expr.Immutable},
	{"uuid_ns_oid", "uuid_ns_oid", expr.Immutable},
	{"uuid_ns_x500", "uuid_ns_x500", expr.Immutable},
}

func UUIDOsspLookup(name string) (selector string, volatility expr.Volatility, ok bool) {
	for _, member := range UUIDOsspMembers {
		if member.Name == name {
			return member.Selector, member.Volatility, true
		}
	}
	return "", expr.Immutable, false
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
