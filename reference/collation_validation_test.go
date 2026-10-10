package reference_test

import "testing"

func TestReferenceCollationValidation(t *testing.T) {
	cases := []struct {
		name, sql string
		want      referenceDiagnostic
	}{
		{"provider", `CREATE COLLATION coll_invalid (PROVIDER=bogus,LOCALE='C')`, referenceDiagnostic{Code: "42P17", Message: "unrecognized collation provider: bogus"}},
		{"builtin_locale", `CREATE COLLATION coll_invalid (PROVIDER=builtin,LOCALE='en_US')`, referenceDiagnostic{Code: "42809", Message: `invalid locale name "en_US" for builtin provider`}},
		{"missing_builtin_locale", `CREATE COLLATION coll_invalid (PROVIDER=builtin)`, referenceDiagnostic{Code: "42P17", Message: `parameter "locale" must be specified`}},
		{"missing_libc_collate", `CREATE COLLATION coll_invalid (PROVIDER=libc)`, referenceDiagnostic{Code: "42P17", Message: `parameter "lc_collate" must be specified`}},
		{"missing_libc_ctype", `CREATE COLLATION coll_invalid (LC_COLLATE='C')`, referenceDiagnostic{Code: "42P17", Message: `parameter "lc_ctype" must be specified`}},
		{"nondeterministic", `CREATE COLLATION coll_invalid (LOCALE='C',DETERMINISTIC=false)`, referenceDiagnostic{Code: "0A000", Message: "nondeterministic collations not supported with this provider"}},
		{"rules", `CREATE COLLATION coll_invalid (LOCALE='C',RULES='&a<b')`, referenceDiagnostic{Code: "42P17", Message: "ICU rules cannot be specified unless locale provider is ICU"}},
		{"default_copy", `CREATE COLLATION coll_invalid FROM pg_catalog."default"`, referenceDiagnostic{Code: "42P17", Message: `collation "default" cannot be copied`}},
		{"missing_copy", `CREATE COLLATION coll_invalid FROM coll_source_absent`, referenceDiagnostic{Code: "42704", Message: `collation "coll_source_absent" for encoding "UTF8" does not exist`}},
		{"version_without_actual", `CREATE COLLATION coll_invalid (LOCALE='C',VERSION='not-semver')`, referenceDiagnostic{Code: "XX000", Message: `collation "coll_invalid" has no actual version, but a version was recorded`}},
		{"quoted_attribute", `CREATE COLLATION coll_invalid ("Lc_Collate"='C',LC_CTYPE='C')`, referenceDiagnostic{Code: "42601", Message: `collation attribute "Lc_Collate" not recognized`, Position: "32"}},
		{"duplicate_option", `CREATE COLLATION coll_invalid (LOCALE='C',LOCALE='POSIX')`, referenceDiagnostic{Code: "42601", Message: "conflicting or redundant options", Position: "43"}},
		{"locale_categories", `CREATE COLLATION coll_invalid (LOCALE='C',LC_COLLATE='C',LC_CTYPE='C')`, referenceDiagnostic{Code: "42601", Message: "conflicting or redundant options", Detail: "LOCALE cannot be specified together with LC_COLLATE or LC_CTYPE."}},
		{"source_options", `CREATE COLLATION coll_invalid (FROM=pg_catalog."C",LOCALE='C')`, referenceDiagnostic{Code: "42601", Message: "conflicting or redundant options", Detail: "FROM cannot be specified together with any other options."}},
		{"boolean", `CREATE COLLATION coll_invalid (LOCALE='C',DETERMINISTIC='o')`, referenceDiagnostic{Code: "42601", Message: "deterministic requires a Boolean value"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			collationScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				collationError(t, p, b, tc.sql, tc.want)
				collationAbsent(t, p, b, "coll_invalid")
			})
		})
	}
	t.Run("identity_conflicts", func(t *testing.T) {
		collationScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
			collationExec(t, p, b, `CREATE COLLATION coll_duplicate FROM pg_catalog."C"`, `CREATE COLLATION coll_libc_duplicate (LOCALE='C')`, `CREATE COLLATION coll_rename FROM pg_catalog."C"`)
			collationError(t, p, b, `CREATE COLLATION coll_duplicate FROM pg_catalog."C"`, referenceDiagnostic{Code: "42710", Message: `collation "coll_duplicate" already exists`})
			collationError(t, p, b, `CREATE COLLATION coll_libc_duplicate (LOCALE='C')`, referenceDiagnostic{Code: "42710", Message: `collation "coll_libc_duplicate" for encoding "UTF8" already exists`})
			collationError(t, p, b, `ALTER COLLATION coll_rename RENAME TO coll_duplicate`, referenceDiagnostic{Code: "42710", Message: `collation "coll_duplicate" already exists in schema "public"`})
			collationError(t, p, b, `DROP COLLATION coll_absent`, referenceDiagnostic{Code: "42704", Message: `collation "coll_absent" for encoding "UTF8" does not exist`})
			collationOID(t, b.db, "coll_rename")
		})
	})
}

func TestReferenceCollationExternalBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, sql  string
		native, pg referenceDiagnostic
		success    bool
	}{
		{"libc_utf8", `CREATE COLLATION coll_external (PROVIDER=libc,LOCALE='C.UTF-8')`, referenceDiagnostic{Code: "0A000", Message: "libc locale provider data is not available in this engine"}, referenceDiagnostic{}, true},
		{"libc_utf8_alias", `CREATE COLLATION coll_external (PROVIDER=libc,LOCALE='C.utf8')`, referenceDiagnostic{Code: "0A000", Message: "libc locale provider data is not available in this engine"}, referenceDiagnostic{}, true},
		{"missing_libc", `CREATE COLLATION coll_external (PROVIDER=libc,LOCALE='collation_missing_locale')`, referenceDiagnostic{Code: "0A000", Message: "libc locale provider data is not available in this engine"}, referenceDiagnostic{Code: "22023", Message: `could not create locale "collation_missing_locale": No such file or directory`, Detail: `The operating system could not find any locale data for the locale name "collation_missing_locale".`}, false},
		{"icu", `CREATE COLLATION coll_external (PROVIDER=icu,LOCALE='und')`, referenceDiagnostic{Code: "0A000", Message: "ICU is not supported in this build"}, referenceDiagnostic{}, true},
		{"icu_nondeterministic", `CREATE COLLATION coll_external (PROVIDER=icu,LOCALE='und-u-ks-level2',DETERMINISTIC=false)`, referenceDiagnostic{Code: "0A000", Message: "ICU is not supported in this build"}, referenceDiagnostic{}, true},
		{"icu_rules", `CREATE COLLATION coll_external (PROVIDER=icu,LOCALE='und',RULES='&a < b')`, referenceDiagnostic{Code: "0A000", Message: "ICU is not supported in this build"}, referenceDiagnostic{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			collationScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				if b.pg && tc.success {
					collationExec(t, p, b, tc.sql)
					collationOID(t, b.db, "coll_external")
					if tc.name == "icu_nondeterministic" {
						collationRoutes(t, p, b, referenceSelect(`SELECT 'a' COLLATE coll_external = 'A' AS equal`, []string{"equal"}, []string{"BOOL"}, [][]any{{true}}))
					}
				} else {
					want := tc.native
					if b.pg {
						want = tc.pg
					}
					collationError(t, p, b, tc.sql, want)
					collationAbsent(t, p, b, "coll_external")
				}
			})
		})
	}
}
