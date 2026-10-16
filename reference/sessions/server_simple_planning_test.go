//go:build !noservertests

package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// These are wire-planning contracts. They do not change the deliberately
// row-owned evaluation policy of the embedded database/sql execution route.
func TestReferenceServerSimplePlanning(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			for _, c := range []struct {
				name, sql, code string
				value           []byte
				oid             uint32
				length          int16
			}{
				{
					name:  "exists_discards_unused_target",
					sql:   "SELECT EXISTS (SELECT 1/0 FROM (VALUES (1)) AS q(v)) AS v",
					value: []byte("t"), oid: 16, length: 1,
				},
				{
					name: "exists_keeps_required_predicate",
					sql:  "SELECT EXISTS (SELECT 1 FROM (VALUES (1)) AS q(v) WHERE 1/0 > 0) AS v",
					code: "22012",
				},
				{
					name: "empty_source_still_plans_direct_target",
					sql:  "SELECT 1/0 AS v FROM (VALUES (1)) AS q(v) WHERE false",
					code: "22012",
				},
				{
					name:  "case_prunes_unreachable_arm",
					sql:   "SELECT CASE WHEN true THEN 7 ELSE 1/0 END AS v",
					value: []byte("7"), oid: 23, length: 4,
				},
				{
					name:  "coalesce_prunes_unreachable_argument",
					sql:   "SELECT COALESCE(7,1/0) AS v",
					value: []byte("7"), oid: 23, length: 4,
				},
				{
					name:  "strict_null_does_not_divide",
					sql:   "SELECT NULL::int4 / 0 AS v",
					value: nil, oid: 23, length: 4,
				},
			} {
				t.Run(c.name, func(t *testing.T) {
					frames := w.query(t, c.sql)
					serverWantReady(t, frames, 'I')
					if c.code != "" {
						serverWantTags(t, frames, "EZ")
						serverNotificationError(t, frames, c.code, "division by zero", "")
						serverWantCompletions(t, frames)
					} else {
						serverWantTags(t, frames, "TDCZ")
						serverWantRaw(t, frames, [][][]byte{{c.value}})
						serverWantCompletions(t, frames, "SELECT 1")
						columns := serverColumns(t, frames)
						if len(columns) != 1 || columns[0] != (serverColumn{"v", 0, 0, c.oid, c.length, -1, 0}) {
							t.Fatalf("result metadata %+v", columns)
						}
					}
					// No reconnect or Sync hides an incorrectly failed simple cycle.
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
		})
	}
}
