package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipSameOwner(t *testing.T) {
	ownershipCases(t, func(s *membershipScenario) {
		for _, tc := range ownershipFreshFamilies {
			s.t.Run(strings.ReplaceAll(tc.noun, " ", "_"), func(t *testing.T) {
				child := *s
				child.t = t
				s := &child
				defer s.actor("")
				if tc.create == "" {
					ownershipSeed(s, "own_fresh", "own_source")
				} else {
					ownershipDefinition(s, tc.create)
					s.exec(tc.alter + " OWNER TO own_source")
				}
				s.actor("own_other")
				switch tc.alter {
				case "ALTER TABLE own_fresh", "ALTER VIEW own_fresh_view", "ALTER MATERIALIZED VIEW own_fresh_mv", "ALTER SEQUENCE own_fresh_seq":
					// The pinned relation-family oracle checks ownership before
					// both the same-owner shortcut and target-role resolution.
					ownershipFailure(s, tc.alter+" OWNER TO own_source", fixture.Diagnostic{Code: "42501", Message: "must be owner of " + tc.noun})
					ownershipFailure(s, tc.alter+" OWNER TO own_absent", fixture.Diagnostic{Code: "42501", Message: "must be owner of " + tc.noun})
				default:
					// Type/domain/composite/collation/schema permit the unchanged
					// owner and resolve a missing target before changed-owner checks.
					s.exec(tc.alter + " OWNER TO own_source")
					ownershipFailure(s, tc.alter+" OWNER TO own_absent", fixture.Diagnostic{Code: "42704", Message: `role "own_absent" does not exist`})
				}
				ownershipFailure(s, tc.alter+" OWNER TO own_target", fixture.Diagnostic{Code: "42501", Message: "must be owner of " + tc.noun})
			})
		}
	})
}
