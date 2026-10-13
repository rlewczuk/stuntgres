package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// This is a new-behavior smoke, sharing the actual feature assertions rather
// than aliasing a prerequisite smoke or comparing backend outputs alone.
func TestReferenceJSONTypeFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{jsonTypeReader})
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonTypeKinds(t, p, b, c)
		jsonTypeLengths(t, p, b, c)
		jsonTypeKeys(t, p, b, c)
		jsonTypeParameters(t, p, b, c)
		jsonTypeMetadata(t, p, b, c)
		jsonTypeSRF(t, p, b, c)
		jsonTypeConsumers(t, p, b, c)
		// Consumers updated row 2: the retained scalar cursor must preserve that frame.
		check := fixture.Select("SELECT id,jk,bt,jl,bn FROM jt_stored ORDER BY id", []string{"id", "jk", "bt", "jl", "bn"}, []string{"INT4", "TEXT", "TEXT", "INT4", "INT4"}, [][]any{{int64(1), "array", "array", int64(1), int64(2)}, {int64(2), "array", "array", int64(3), int64(0)}})
		jsonTypeControlPhase(t, p, b, c, check)
		// Roles are introduced after the dump/replay boundary, avoiding cluster-wide
		// role creation being confused with the fixture's public-schema reset.
		jsonTypeACL(t, p, b, c)
	})
}
