package extension_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func buffercacheRejectPublication(t *testing.T, e *core.Engine, s *session.Session, ast parser.Statement, d core.Description, accepted exec.AcceptedFolds) {
	t.Helper()
	owner := d.Folds
	snapshot := slices.Clone(accepted.Entries)
	for i := range snapshot { snapshot[i].Inputs = slices.Clone(snapshot[i].Inputs) }
	op, err := s.Acquire(t.Context())
	if err != nil { t.Fatal(err) }
	scope, err := e.StartStatement(op)
	if err != nil { op.Done(); t.Fatal(err) }
	local := enginetest.CloneFoldDescription(t, d)
	scope.Expected = &local
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil { e.Mu.Unlock(); scope.Finish(err); op.Done(); t.Fatal(err) }
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil { e.Mu.Unlock(); scope.Finish(err); op.Done(); t.Fatal(err) }
	publication, err := core.BeginFoldPublication(&local, ast, environment, auth.Catalog, nil)
	if err != nil { e.Mu.Unlock(); scope.Finish(err); op.Done(); t.Fatal(err) }
	publication.Group.Entries = slices.Clone(publication.Group.Entries)
	publication.Group.Private = true
	publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
	ctx, cancel := context.WithCancel(op.Ctx)
	cancel()
	err = publication.Accept(ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
	e.Mu.Unlock()
	scope.Finish(err)
	op.Done()
	if !errors.Is(err, context.Canceled) || publication.Group.Sealed || !owner.Live() || owner.Current().Generation != accepted.Generation || local.Folds.Current().Generation != accepted.Generation || len(owner.Current().Entries) != len(snapshot) || len(local.Folds.Current().Entries) != len(snapshot) || &owner.Current().Entries[0] != &accepted.Entries[0] || &local.Folds.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("rejected publication changed real accepted owner/generation/backing", err)
	}
	for i, want := range snapshot {
		if !enginetest.SamePublishedFold(owner.Current().Entries[i], want) { t.Fatal("rejected publication mutated accepted artifact", i) }
	}
}

func buffercacheNUMADynamicDependency(t *testing.T, e *core.Engine) {
	t.Helper()
	enginetest.Run(t, e, "CREATE VIEW numa_observation_wrapper AS SELECT bufferid FROM pg_buffercache_numa")
	enginetest.Run(t, e, "CREATE VIEW numa_mutation_wrapper AS SELECT (pg_buffercache_evict_all()).buffers_evicted AS n")
	enginetest.Run(t, e, "CREATE VIEW numa_summary_wrapper AS SELECT (pg_buffercache_summary()).buffers_used AS n")
	enginetest.Run(t, e, "CREATE VIEW numa_usage_wrapper AS SELECT usage_count FROM pg_buffercache_usage_counts()")
	read := e.CatalogRead()
	for _, tc := range []struct { name string; dynamic bool }{{"numa_observation_wrapper", false}, {"numa_summary_wrapper", false}, {"numa_usage_wrapper", false}, {"numa_mutation_wrapper", true}} {
		relation := read.MustRelation(enginetest.CatalogRef(t, e, read, tc.name).Address)
		if stored.ViewOf(relation) == nil { t.Fatal("missing actual analyzed stored wrapper", tc.name) }
		found, err := stored.HasDynamicSourceDependency(read, nil, []exec.Leaf{{Relation: relation}})
		if err != nil || found != tc.dynamic { t.Fatal("sealed observation/mutation dependency classification", tc.name, found, err) }
	}
}
