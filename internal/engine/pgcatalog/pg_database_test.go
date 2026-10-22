package pgcatalog

import (
	"stuntgres/internal/engine/catalog"
	"testing"
)

func TestCollationActualVersionProviders(t *testing.T) {
	c, posix := "C", "POSIX"
	utf8, utf8Alias, fast, root := "C.UTF-8", "C.UTF8", "PG_UNICODE_FAST", "und"
	stale, empty, external, invalid := "arbitrary-recorded-version", "", "en_US.UTF-8", "not-a-builtin-locale"
	for _, tc := range []struct {
		name string
		d    catalog.Collation
		want *string
	}{
		{"default", catalog.Collation{Provider: "d", Version: &stale}, nil},
		{"libc-C", catalog.Collation{Provider: "c", Collate: &c, CType: &c, Version: &stale}, nil},
		{"libc-POSIX", catalog.Collation{Provider: "c", Collate: &posix, CType: &posix, Version: &empty}, nil},
		{"builtin-C", catalog.Collation{Provider: "b", Locale: &c, Version: &stale}, &builtinCollationVersion},
		{"builtin-UTF8", catalog.Collation{Provider: "b", Locale: &utf8, Version: &empty}, &builtinCollationVersion},
		{"builtin-UTF8-alias", catalog.Collation{Provider: "b", Locale: &utf8Alias}, &builtinCollationVersion},
		{"builtin-fast", catalog.Collation{Provider: "b", Locale: &fast, Version: &stale}, &builtinCollationVersion},
		{"unavailable-ICU", catalog.Collation{Provider: "i", Locale: &root}, nil},
		{"unavailable-ICU-recorded", catalog.Collation{Provider: "i", Locale: &root, Version: &stale}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.d
			got, err := CollationActualVersion(&tc.d)
			if err != nil {
				t.Fatal(err)
			}
			if (got == nil) != (tc.want == nil) || got != nil && *got != "1" {
				t.Fatalf("actual version = %v; want %v", got, tc.want)
			}
			if tc.d != before {
				t.Fatal("version inquiry changed the descriptor")
			}
		})
	}
	if got, err := DatabaseCollationActualVersion(); err != nil || got != nil {
		t.Fatal("native database must remain versionless", got, err)
	}
	for _, tc := range []struct {
		name string
		d    catalog.Collation
	}{
		{"external-libc", catalog.Collation{Provider: "c", Collate: &external, CType: &external}},
		{"invalid-libc-properties", catalog.Collation{Provider: "c"}},
		{"invalid-builtin-locale", catalog.Collation{Provider: "b", Locale: &invalid}},
		{"invalid-builtin-properties", catalog.Collation{Provider: "b", Locale: &c, Collate: &c}},
		{"unknown-provider", catalog.Collation{Provider: "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := CollationActualVersion(&tc.d); err == nil || got != nil {
				t.Fatal("invalid or unavailable executable definition lost its error", got, err)
			}
		})
	}
}
