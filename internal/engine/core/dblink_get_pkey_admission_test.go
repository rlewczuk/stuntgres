package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqltype"
)

func TestDblinkGetPkeyAdmission(t *testing.T) {
	for _, phase := range []string{"success", "prior_select", "empty", "missing", "kind", "cancel", "acl_readmission", "catalog_readmission"} {
		t.Run(phase, func(t *testing.T) {
			e := engine.New()
			dblinkUnitInstall(t, e)
			for _, q := range []string{
				"CREATE ROLE pkey_access", "CREATE ROLE pkey_reader", "GRANT pkey_access TO pkey_reader",
				"CREATE TABLE pkey_admit(a integer,b integer,PRIMARY KEY(b,a))",
				"CREATE TABLE pkey_empty(a integer)",
				"GRANT SELECT ON pkey_admit,pkey_empty TO pkey_access",
				"CREATE TYPE pkey_kind AS (id integer)",
			} {
				enginetest.Run(t, e, q)
			}
			n := pkeyUnitExpression(t, e)
			reader, ticket := blockingUnitOwner(t, e)
			enginetest.SessionSQL(t, reader, "SET ROLE pkey_reader")
			scope := pgrowlocksUnitScope(t, e, reader)
			scope.RoutineDependencies = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
			if err := scope.WriteHost().Contrib().ValidateDblinkGetPkey(n); err != nil {
				t.Fatal(err)
			}
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "pkey_admit")
			resource := lock.Resource{ObjectRef: ref}
			name := "pkey_admit"
			switch phase {
			case "empty":
				name = "pkey_empty"
			case "missing":
				name = "missing_pkey"
			case "kind":
				name = "pkey_kind"
			}
			if phase == "empty" || phase == "kind" {
				ref = enginetest.CatalogRef(t, e, e.CatalogRead(), name)
				resource.ObjectRef = ref
			}
			if phase == "prior_select" {
				request, err := e.Locks.StartAcquire(context.Background(), ticket, resource, lock.AccessShare, lock.Policy{})
				if err != nil || request != nil {
					t.Fatal("prior SELECT contribution", request, err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			scope.Op.Ctx = ctx
			var rows []sqltype.Value
			invoke := func() error {
				var err error
				rows, err = scope.WriteHost().Contrib().DblinkGetPkey(n, name)
				return err
			}
			var err error
			if phase == "cancel" || phase == "acl_readmission" || phase == "catalog_readmission" {
				_, holder := blockingUnitOwner(t, e)
				amcheckUnitGrant(t, e, holder, resource, lock.AccessExclusive)
				done, stopped := make(chan error, 1), make(chan struct{})
				go func() { defer close(stopped); done <- invoke() }()
				defer func() {
					cancel()
					select {
					case <-stopped:
					case <-time.After(enginetest.Liveness):
						t.Error("metadata worker did not stop")
					}
				}()
				amcheckUnitQueue(t, e, ticket, resource, lock.AccessShare, done)
				switch phase {
				case "cancel":
					cancel()
				case "acl_readmission":
					enginetest.Run(t, e, "REVOKE pkey_access FROM pkey_reader")
				case "catalog_readmission":
					stage := e.BeginCatalogStage(e.CatalogRead())
					defer e.DiscardCatalogStage(stage)
					old := e.CatalogRead().MustRelation(ref.Address)
					next := *old
					next.ObjectRevision = old.Revision() + 1
					if err := stage.Replace(&next); err != nil {
						t.Fatal(err)
					}
					enginetest.PublishStage(t, e, stage)
				}
				e.EndLockOwner(holder)
				err = amcheckUnitJoin(t, done)
			} else {
				err = invoke()
			}
			switch phase {
			case "success", "prior_select":
				if err != nil || len(rows) != 2 || rows[0].Type() != n.Usage.Type {
					t.Fatal("metadata admission", rows, err)
				}
			case "empty":
				if err != nil || len(rows) != 0 {
					t.Fatal("empty metadata admission", rows, err)
				}
			case "missing":
				enginetest.State(t, err, "42P01")
			case "kind":
				enginetest.State(t, err, "42809")
			case "cancel":
				if !errors.Is(err, context.Canceled) || rows != nil {
					t.Fatal("canceled metadata leaked", rows, err)
				}
			case "acl_readmission":
				enginetest.State(t, err, "42501")
				if rows != nil {
					t.Fatal("stale SELECT authority leaked keys")
				}
			case "catalog_readmission":
				if !errors.Is(err, ids.ErrCatalogReplaced) || rows != nil {
					t.Fatal("stale descriptor leaked keys", rows, err)
				}
			}
			if e.Locks.HasRelationMode(ticket, resource, lock.AccessShare) != (phase == "prior_select") {
				t.Fatal("metadata retained its operation contribution or removed earlier SELECT")
			}
			if scope.View != nil {
				t.Fatal("catalog-only metadata captured a tuple view")
			}
		})
	}
}
