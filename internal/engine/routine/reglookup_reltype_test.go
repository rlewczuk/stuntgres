package routine

import "testing"

func TestRegLookupRelTypeDeclarations(t *testing.T) {
	regLookupDeclarations(t, []regLookupDeclarationCase{
		{3495, 2205, "to_regclass"},
		{4086, 4089, "to_regnamespace"},
		{3493, 2206, "to_regtype"},
		{6317, 23, "to_regtypemod"},
	})
}
