package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// PgcryptoMember is one function the pgcrypto 1.4 manifest installs. The
// engine stages and validates installations from this table; bind code reads
// the selected member's result and volatility, which installed procedures
// never change.
type PgcryptoMember struct {
	Name       string
	Signature  string
	Result     ids.OID
	Volatility expr.Volatility
	Strict     bool
}

var PgcryptoMembers = [...]PgcryptoMember{
	{"digest", "25,25", 17, expr.Immutable, true},
	{"digest", "17,25", 17, expr.Immutable, true},
	{"hmac", "25,25,25", 17, expr.Immutable, true},
	{"hmac", "17,17,25", 17, expr.Immutable, true},
	{"crypt", "25,25", 25, expr.Immutable, true},
	{"gen_salt", "25", 25, expr.Volatile, true},
	{"gen_salt", "25,23", 25, expr.Volatile, true},
	{"encrypt", "17,17,25", 17, expr.Immutable, true},
	{"decrypt", "17,17,25", 17, expr.Immutable, true},
	{"encrypt_iv", "17,17,17,25", 17, expr.Immutable, true},
	{"decrypt_iv", "17,17,17,25", 17, expr.Immutable, true},
	{"gen_random_bytes", "23", 17, expr.Volatile, true},
	{"gen_random_uuid", "", 2950, expr.Volatile, false},
	{"fips_mode", "", 16, expr.Volatile, true},
}

// PgcryptoLookup returns the manifest member with the exact name and signature.
func PgcryptoLookup(name, signature string) (PgcryptoMember, bool) {
	for _, member := range PgcryptoMembers {
		if member.Name == name && member.Signature == signature {
			return member, true
		}
	}
	return PgcryptoMember{}, false
}
