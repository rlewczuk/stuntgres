package extension

import (
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
)

func TestPgcryptoPGPManifest(t *testing.T) {
	wantSources := [...]string{"pg_digest", "pg_digest", "pg_hmac", "pg_hmac", "pg_crypt", "pg_gen_salt", "pg_gen_salt_rounds", "pg_encrypt", "pg_decrypt", "pg_encrypt_iv", "pg_decrypt_iv", "pg_random_bytes", "pg_random_uuid", "pg_check_fipsmode", "pg_armor", "pg_armor", "pg_dearmor", "pgp_armor_headers", "pgp_sym_encrypt_text", "pgp_sym_encrypt_text", "pgp_sym_decrypt_text", "pgp_sym_decrypt_text", "pgp_sym_encrypt_bytea", "pgp_sym_encrypt_bytea", "pgp_sym_decrypt_bytea", "pgp_sym_decrypt_bytea"}
	if len(functions.PgcryptoMembers) != len(wantSources) || pgcryptoSources != wantSources {
		t.Fatal("manifest/source alignment")
	}
	want := [...]functions.PgcryptoMember{
		{Name: "pgp_sym_encrypt", Signature: "25,25", Result: 17, Volatility: expr.Volatile, Strict: true},
		{Name: "pgp_sym_encrypt", Signature: "25,25,25", Result: 17, Volatility: expr.Volatile, Strict: true},
		{Name: "pgp_sym_decrypt", Signature: "17,25", Result: 25, Volatility: expr.Immutable, Strict: true},
		{Name: "pgp_sym_decrypt", Signature: "17,25,25", Result: 25, Volatility: expr.Immutable, Strict: true},
		{Name: "pgp_sym_encrypt_bytea", Signature: "17,25", Result: 17, Volatility: expr.Volatile, Strict: true},
		{Name: "pgp_sym_encrypt_bytea", Signature: "17,25,25", Result: 17, Volatility: expr.Volatile, Strict: true},
		{Name: "pgp_sym_decrypt_bytea", Signature: "17,25", Result: 17, Volatility: expr.Immutable, Strict: true},
		{Name: "pgp_sym_decrypt_bytea", Signature: "17,25,25", Result: 17, Volatility: expr.Immutable, Strict: true},
	}
	for i, m := range want {
		if functions.PgcryptoMembers[18+i] != m {
			t.Fatal("appended PGP slot", 18+i)
		}
		d := pgcryptoDeclaration(18 + i)
		if d.Parallel != 's' || d.ReturnsSet || d.Rows != 0 || d.Outputs != nil {
			t.Fatal("PGP scalar declaration", 18+i, d)
		}
	}
	armor := pgcryptoDeclaration(17)
	if functions.PgcryptoMembers[17].Name != "pgp_armor_headers" || !armor.ReturnsSet || armor.Rows != 1000 || armor.Outputs != pgcryptoArmorOutputs {
		t.Fatal("armor slot identity changed")
	}
}
