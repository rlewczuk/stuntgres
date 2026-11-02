package settings

const PgcryptoBuiltinCrypto = "pgcrypto.builtin_crypto_enabled"

// PgcryptoDefinition returns an owned module recipe. It is installed only in a
// physical session that initializes the authenticated stock provider, never in
// the shared registry or the public registration namespace.
func PgcryptoDefinition() Definition {
	d := Definition{
		Name: PgcryptoBuiltinCrypto, Type: Enum, Access: Privileged,
		BootDefault: "on", EnumValues: []string{"on", "off", "fips"},
		Category: "Customized Options",
		Description: "Sets if builtin crypto functions are enabled.",
		ExtraDescription: `"on" enables builtin crypto, "off" unconditionally disables and "fips" will disable builtin crypto if OpenSSL is in FIPS mode`,
	}
	var err error
	d.bootValue, err = d.Parse(d.BootDefault)
	if err != nil { panic(err) } // The private source-owned recipe is constant.
	return d
}
