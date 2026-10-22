package engine

import "testing"

func TestAlterNullabilityProviderPublicationFences(t *testing.T) {
	for _, q := range []string{
		"ALTER TABLE t DROP b, ALTER a SET NOT NULL",
		"ALTER TABLE t ADD NOT NULL a, ALTER a SET NOT NULL",
	} {
		t.Run(q, func(t *testing.T) { notNullProviderPublicationFences(t, q) })
	}
}
