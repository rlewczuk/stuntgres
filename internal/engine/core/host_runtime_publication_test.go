package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func hostOwnerResultError(result core.Result, primary error) error {
	if result.Rows == nil {
		return primary
	}
	defer result.Rows.Close()
	row := make([]sqltype.Value, len(result.Rows.Schema()))
	for primary == nil {
		var present bool
		present, primary = result.Rows.Next(context.Background(), row)
		if !present {
			break
		}
	}
	if err := result.Rows.Close(); primary == nil {
		primary = err
	}
	return primary
}

// A real callback-free CREATE is staged, checked and attached to the ordinary
// statement journal. Cancellation happens after that attachment but before
// Finish's one committed catalog/storage boundary. No compile callback or
// unconditional caller rollback can conceal publication here.
func hostOwnerPublicationCancellation(t *testing.T) {
	t.Helper()
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-publication-fence", true: "replacement-publication-fence"}[replace], func(t *testing.T) {
			callbacks := 0
			e, original := hostOwnerEngine(t, func() error { callbacks++; return nil })
			enginetest.Run(t, e, "CREATE TABLE owner_host_backing(v integer)")
			enginetest.Run(t, e, "INSERT INTO owner_host_backing VALUES (83)")
			before := e.CatalogRead()
			backing := make(map[ids.ObjectAddress]*storage.Relation, len(e.Storage.Relations))
			for a, r := range e.Storage.Relations {
				backing[a] = r
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, done, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(context.Canceled)
			scope.Catalog = session.NewJournal()
			query := "CREATE FUNCTION owner_host_unpublished(v hstore DEFAULT 'a=>7'::hstore) RETURNS hstore LANGUAGE owner_host TRANSFORM FOR TYPE hstore AS 'increment'"
			if replace {
				query = "CREATE OR REPLACE FUNCTION owner_host_fn(v hstore DEFAULT 'a=>7'::hstore) RETURNS hstore LANGUAGE owner_host TRANSFORM FOR TYPE hstore AS 'increment'"
			}
			raw := enginetest.Statement(t, query).(*parser.CreateFunction)
			if err = scope.Gate(raw); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			var stage *core.CatalogStage
			if err == nil {
				stage = e.BeginCatalogStage(auth.Catalog)
				stage.AuthorizeLocked(auth, op.Session)
				err = routine.StageCreateFunction(schemacmds.RoutineHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, raw, sqltype.InputEnvironment{})
			}
			e.Mu.Unlock()
			if stage != nil {
				defer e.DiscardCatalogStage(stage)
			}
			if err != nil {
				t.Fatal(err)
			}
			var candidate *catalog.Procedure
			for _, d := range stage.Descriptors() {
				if p, ok := d.(*catalog.Procedure); ok && p.Host != nil && p.Name() == raw.Name.Parts[len(raw.Name.Parts)-1].Text {
					candidate = p
				}
			}
			if candidate == nil || candidate.Host == original.Host || candidate.Host.Layout == original.Host.Layout || callbacks != 0 {
				t.Fatal("CREATE did not stage a genuine distinct callback-free host declaration", callbacks)
			}
			if scope.ReplacementRead {
				e.ReplacementMu.RUnlock()
				scope.ReplacementRead = false
			}
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = e.PublishCatalogOperation(op, scope, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
				t.Fatal(err)
			}
			if e.CatalogRead().State() != before.State() || scope.Catalog == nil ||
				scope.Catalog.Descriptors[candidate.Address()] != candidate || scope.TxRef.Epoch() != before.Epoch() {
				t.Fatal("candidate was not privately adopted in the current transaction epoch")
			}
			cancel()
			if err = scope.Finish(nil); !errors.Is(err, context.Canceled) {
				t.Fatal("Finish did not reject canceled host declaration", err)
			}
			after := e.CatalogRead()
			current, _ := after.Procedure(original.Address())
			typ, _ := after.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: original.Result})
			if after.State() != before.State() || after.Epoch() != before.Epoch() || current != original || current.Host.Layout != original.Host.Layout || typ.SQLType != original.Host.Layout.Result.Type || callbacks != 0 {
				t.Fatal("canceled candidate changed committed declaration/type generation", callbacks)
			}
			for _, d := range after.Descriptors() {
				if p, ok := d.(*catalog.Procedure); ok && p.Name() == "owner_host_unpublished" {
					t.Fatal("canceled CREATE left an orphan")
				}
			}
			if len(backing) != len(e.Storage.Relations) {
				t.Fatal("canceled declaration changed backing roots")
			}
			for a, r := range backing {
				if e.Storage.Relations[a] != r {
					t.Fatal("canceled declaration mutated backing root")
				}
			}
			if core.ActiveScopeOfLease(op.Lease) != nil || scope.ReplacementRead || op.Session.Transactions.Catalog != nil {
				t.Fatal("canceled publication retained live candidate/lease/reservation")
			}
		})
	}
}
