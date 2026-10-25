package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// A pending retirement must not publish over a later committed cast edge. The
// ordinary public routes lock these objects; this tests the independent final
// precondition against an authentic captured retirement and real descriptors.
func TestTransactionalDefinitionPublication(t *testing.T) {
	for _, target := range []string{"source", "function", "adapter"} {
		t.Run(target, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			setup := []string{
				"CREATE TYPE retirement_src AS ENUM ('red')",
				"CREATE FUNCTION retirement_score(retirement_src) RETURNS integer LANGUAGE SQL AS $$ SELECT 41 $$",
			}
			drop := "DROP TYPE retirement_src CASCADE"
			create := "CREATE CAST (retirement_src AS integer) WITH FUNCTION retirement_score(retirement_src)"
			if target == "function" {
				drop = "DROP FUNCTION retirement_score(retirement_src) RESTRICT"
			}
			if target == "adapter" {
				setup = []string{
					"CREATE CAST (text AS bytea) WITHOUT FUNCTION AS IMPLICIT",
					"CREATE FUNCTION retirement_bytes(xid) RETURNS text LANGUAGE SQL AS $$ SELECT 'abc'::text $$",
				}
				drop = "DROP CAST (text AS bytea) RESTRICT"
				create = "CREATE CAST (xid AS bytea) WITH FUNCTION retirement_bytes(xid)"
			}
			for _, query := range setup {
				domainUnitPublish(t, e, domainUnitStage(t, e, op, query))
			}
			base := e.CatalogRead()
			retire := domainUnitStage(t, e, op, drop)
			defer e.DiscardCatalogStage(retire)
			mode := parser.DropRestrict
			if target == "source" {
				mode = parser.DropCascade
			}
			if err := core.StageExpressionDependencies(context.Background(), retire, retire.DomainTargets, mode, nil, nil, nil, ids.ObjectAddress{}); err != nil {
				t.Fatal(err)
			}
			journal := session.NewJournal()
			for address := range retire.Removals() {
				journal.RecordDescriptor(base.State(), address, nil)
			}
			journal.Dependencies = append(journal.Dependencies, retire.Sequence.IdentityDependencyList()...)
			addition := domainUnitStage(t, e, op, create)
			var added *catalog.Cast
			for _, descriptor := range addition.Descriptors() {
				added, _ = descriptor.(*catalog.Cast)
				if added != nil {
					break
				}
			}
			if added == nil {
				t.Fatal("missing genuine added cast")
			}
			domainUnitPublish(t, e, addition)
			committed := e.CatalogRead()
			e.Mu.Lock()
			err := e.CatalogPreflightLocked(journal)
			e.Mu.Unlock()
			if _, ok := err.(catalog.Conflict); !ok {
				t.Fatal("captured retirement admitted a new surviving cast edge", err)
			}
			if e.CatalogRead().State() != committed.State() || committed.Get(added.Address()) != added {
				t.Fatal("failed retirement changed committed graph")
			}
			for _, dependency := range catalog.CastDependencies(added) {
				if committed.Get(dependency) == nil {
					t.Fatal("retirement removed live cast dependency", dependency)
				}
			}
			// A consumer removed by this same candidate must not impersonate a
			// surviving dependency. Unrelated published work is retained.
			withoutEdge := journal.Clone()
			withoutEdge.RecordDescriptor(committed.State(), added.Address(), nil)
			e.Mu.Lock()
			err = e.CatalogPreflightLocked(withoutEdge)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal("candidate's own edge retirement falsely conflicted", err)
			}
			if base.Get(added.Address()) != nil || committed.Get(added.Address()) != added {
				t.Fatal("preflight mutated immutable predecessor")
			}
		})
	}
}
