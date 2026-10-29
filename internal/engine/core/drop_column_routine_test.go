package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// SQL only builds genuine descriptors here; assertions exercise the package
// dependency/preparation boundary, not a second native-only SQL scenario.
func columnRoutineCatalog(t *testing.T) (*core.Engine, catalog.Read, *catalog.Relation, map[string]*catalog.Procedure) {
	t.Helper()
	e := engine.New()
	for _, sql := range []string{
		"CREATE TABLE column_owner(a integer UNIQUE,b integer,c integer GENERATED ALWAYS AS(a+1) STORED,CONSTRAINT local_a CHECK(a>0))",
		"CREATE FUNCTION column_use() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT a FROM column_owner; END",
		"CREATE FUNCTION column_default(x integer DEFAULT (NULL::column_owner).a) RETURNS integer LANGUAGE SQL RETURN x",
		"CREATE FUNCTION column_caller() RETURNS integer LANGUAGE SQL RETURN column_use()",
		"CREATE FUNCTION column_keep() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT b FROM column_owner; END",
		"ALTER TABLE column_owner ADD CONSTRAINT normal_check CHECK(column_use()>0)",
		"CREATE TABLE column_consumer(x integer DEFAULT column_use())",
	} {
		enginetest.Run(t, e, sql)
	}
	base := e.CatalogRead()
	owner := base.MustRelation(enginetest.RoleTableRef(t, e, "column_owner").Address)
	procedures := make(map[string]*catalog.Procedure)
	for _, descriptor := range base.Descriptors() {
		if procedure, ok := descriptor.(*catalog.Procedure); ok && slices.Contains([]string{"column_use", "column_default", "column_caller", "column_keep"}, procedure.Name()) {
			procedures[procedure.Name()] = procedure
		}
	}
	if len(procedures) != 4 {
		t.Fatal("missing real routine definitions", procedures)
	}
	return e, base, owner, procedures
}

func TestDropColumnRoutineOwnership(t *testing.T) {
	for _, adapter := range []string{"stage", "preparation"} {
		t.Run(adapter, func(t *testing.T) {
			e, base, owner, routines := columnRoutineCatalog(t)
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			provider := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
			if adapter == "preparation" {
				provider = &core.DropProvider{Base: base, Owner: e, Ctx: context.Background(), Prepared: &table.DropPreparation{Base: base, Discovery: base}}
			}
			target := owner.Address()
			target.SubID = owner.Attributes[0]
			before := base.State()
			if err := provider.ColumnDependencies(target, parser.DropRestrict); err == nil {
				t.Fatal("full closure accepted RESTRICT")
			} else {
				enginetest.State(t, err, "2BP01")
			}
			if provider.Replacement(owner.Address()) != nil || provider.Removed(routines["column_use"].Address()) || e.CatalogRead().State() != before {
				t.Fatal("RESTRICT changed a descriptor or published")
			}
			if err := provider.ColumnDependencies(target, parser.DropCascade); err != nil {
				t.Fatal(err)
			}
			current := provider.Descriptor(owner.Address()).(*catalog.Relation)
			if provider.Removed(owner.Address()) || !slices.Equal(current.Attributes, []int32{1, 2}) || len(current.Constraints.Unique) != 1 {
				t.Fatal("root shape/AUTO index ownership was stolen", current.Attributes, current.Constraints.Unique)
			}
			if len(current.Constraints.Check) != 1 || current.Constraints.Check[0].Name != "local_a" {
				t.Fatal("local AUTO CHECK or NORMAL same-table CHECK misclassified", current.Constraints.Check)
			}
			index := current.Constraints.Unique[0].Index
			if provider.Removed(index) || provider.Descriptor(index) == nil {
				t.Fatal("root index retired before metadata pruning")
			}
			for _, name := range []string{"column_use", "column_default", "column_caller"} {
				if !provider.Removed(routines[name].Address()) {
					t.Fatal("actual recursive routine survived", name)
				}
			}
			if provider.Removed(routines["column_keep"].Address()) {
				t.Fatal("unused-attribute routine retired")
			}
			rowAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(owner.RowType)}
			originalRow := base.MustType(rowAddress)
			projectedRow := provider.Descriptor(rowAddress).(*catalog.Type)
			retained := provider.RetirementRead()
			if projectedRow.SQLType == originalRow.SQLType || retained.MustType(rowAddress) != projectedRow || retained.MustRelation(owner.Address()) != current {
				t.Fatal("retirement facts did not retain the genuine projected generation")
			}
			if retained.Owner() != base.Owner() || retained.Domain() != base.Domain() || retained.Epoch() != base.Epoch() || retained.MustProcedure(routines["column_use"].Address()) != routines["column_use"] || provider.RetirementDescriptor(owner.Address()) != owner {
				t.Fatal("retirement projection replaced authority or original AUTO owners")
			}
			var originalCodec, projectedCodec []ids.ObjectAddress
			if err := pgcatalog.VisitTypeProcedureDependencies(t.Context(), base, originalRow, func(a ids.ObjectAddress) bool { originalCodec = append(originalCodec, a); return true }); err != nil {
				t.Fatal("original codec facts", err)
			}
			if err := pgcatalog.VisitTypeProcedureDependencies(t.Context(), retained, projectedRow, func(a ids.ObjectAddress) bool { projectedCodec = append(projectedCodec, a); return true }); err != nil || len(projectedCodec) == 0 || !slices.Equal(originalCodec, projectedCodec) {
				t.Fatal("projected generation lost its actual codec facts", originalCodec, projectedCodec, err)
			}
			for _, pair := range []struct {
				read catalog.Read
				row  *catalog.Type
			}{{base, projectedRow}, {retained, originalRow}} {
				called := false
				if err := pgcatalog.VisitTypeProcedureDependencies(t.Context(), pair.read, pair.row, func(ids.ObjectAddress) bool { called = true; return true }); err == nil || called {
					t.Fatal("mismatched original/projected authority visited", called, err)
				}
			}
			consumer := enginetest.RoleTableRef(t, e, "column_consumer")
			if len(provider.Descriptor(consumer.Address).(*catalog.Relation).ColumnMeta.Defaults) != 0 {
				t.Fatal("recursive surviving DEFAULT owner was not pruned")
			}
			next, err := table.OrdinaryColumnDrop(provider, current, provider.BaseRef(owner.Address()), parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: "a"}, Mode: parser.DropCascade})
			if err != nil {
				t.Fatal("column owner could not consume its original AUTO payload", err)
			}
			if !slices.Equal(next.Attributes, []int32{2}) || len(next.Constraints.Unique) != 0 || len(next.Constraints.Check) != 0 || !next.ColumnMeta.Dropped[1].Dropped || !next.ColumnMeta.Dropped[3].Dropped {
				t.Fatal("column metadata was not consumed exactly once", next)
			}
			if !slices.Equal(owner.Attributes, []int32{1, 2, 3}) || len(owner.Constraints.Check) != 2 || len(owner.Constraints.Unique) != 1 || e.CatalogRead().State() != before || base.Get(owner.Address()) != owner {
				t.Fatal("retained root or committed state mutated")
			}
			if retained.MustType(rowAddress) != projectedRow || retained.MustRelation(owner.Address()) != current || !slices.Equal(current.Attributes, []int32{1, 2}) || len(current.Constraints.Unique) != 1 {
				t.Fatal("consuming original AUTO payload mutated retained dependency facts")
			}
			if provider.Prepared != nil {
				// Actual immutable projection lineage records only the recursive
				// generated-column shape; the original root remains caller-owned.
				var projections []table.DropEffect
				for _, effect := range provider.Prepared.Effects() {
					if effect.Before != nil {
						projections = append(projections, effect)
					}
				}
				if len(projections) != 1 || !slices.Equal(projections[0].Before.Attributes, []int32{1, 2, 3}) || !slices.Equal(projections[0].After.Attributes, []int32{1, 2}) {
					t.Fatal("wrong recursive/root projection ownership", projections)
				}
			}
		})
	}
}

// Both real adapters consume one captured sibling batch. In particular UNIQUE
// index descriptors remain readable until all doomed generation metadata is
// gone, and the surviving generation is remapped only to the final layout.
func TestDropColumnCapturedBatch(t *testing.T) {
	for _, adapter := range []string{"stage", "preparation"} {
		t.Run(adapter, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE captured_batch(a integer,b integer,g integer GENERATED ALWAYS AS(a+1) STORED UNIQUE,h integer GENERATED ALWAYS AS(a+2) STORED UNIQUE,k integer GENERATED ALWAYS AS(b+3) STORED UNIQUE,CONSTRAINT keep_k CHECK(k>0))")
			enginetest.Run(t, e, "INSERT INTO captured_batch(a,b) VALUES(7,8)")
			enginetest.Run(t, e, "CREATE FUNCTION captured_batch_use() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT a FROM captured_batch; END")
			enginetest.Run(t, e, "ALTER TABLE captured_batch ADD CONSTRAINT doomed_provider CHECK(captured_batch_use()>0)")
			base := e.CatalogRead()
			old := base.MustRelation(enginetest.RoleTableRef(t, e, "captured_batch").Address)
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			provider := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
			if adapter == "preparation" {
				provider = &core.DropProvider{Base: base, Owner: e, Ctx: context.Background(), Prepared: &table.DropPreparation{Base: base, Discovery: base}}
			}
			target := old.Address()
			target.SubID = 1
			if err := provider.ColumnDependencies(target, parser.DropRestrict); err == nil {
				t.Fatal("captured generated descendants did not reject RESTRICT")
			} else {
				enginetest.State(t, err, "2BP01")
			}
			if provider.Replacement(old.Address()) != nil {
				t.Fatal("RESTRICT cooked the column batch")
			}
			if err := provider.ColumnDependencies(target, parser.DropCascade); err != nil {
				t.Fatal(err)
			}
			next := provider.Descriptor(old.Address()).(*catalog.Relation)
			if !slices.Equal(next.Attributes, []int32{1, 2, 5}) || len(next.ColumnMeta.Generations) != 1 || !next.ColumnMeta.Dropped[3].Dropped || !next.ColumnMeta.Dropped[4].Dropped {
				t.Fatal("wrong final stable-attribute batch", next)
			}
			if next.Revision() != old.Revision()+1 {
				t.Fatal("batch incremented the already-pruned metadata revision", old.Revision(), next.Revision())
			}
			if _, exists := next.ColumnMeta.Generations[5]; !exists || len(next.Constraints.Check) != 1 || next.Constraints.Check[0].Name != "keep_k" {
				t.Fatal("surviving generation/CHECK was lost", next)
			}
			if len(next.Constraints.Unique) != 1 || !slices.Equal(next.Constraints.Unique[0].Attributes, []int32{5}) {
				t.Fatal("UNIQUE metadata was pruned before its index was read", next.Constraints.Unique)
			}
			for _, constraint := range old.Constraints.Unique {
				doomed := constraint.Attributes[0] != 5
				if provider.Removed(constraint.Index) != doomed {
					t.Fatal("wrong captured AUTO index retirement", constraint)
				}
				if base.Get(constraint.Index) == nil {
					t.Fatal("retained index authority mutated")
				}
			}
			if provider.Removed(old.Address()) || e.CatalogRead().State() != base.State() || !slices.Equal(old.Attributes, []int32{1, 2, 3, 4, 5}) || len(old.ColumnMeta.Generations) != 3 || len(old.Constraints.Unique) != 3 || len(old.Constraints.Check) != 2 {
				t.Fatal("root shape ownership or retained catalog changed")
			}
			if provider.Prepared != nil {
				projections := 0
				var beforeBatch *catalog.Relation
				for _, effect := range provider.Prepared.Effects() {
					if descriptor, ok := effect.Descriptor.(*catalog.Relation); ok && descriptor.Address() == old.Address() && slices.Equal(descriptor.Attributes, old.Attributes) && len(descriptor.Constraints.Check) == 1 {
						beforeBatch = descriptor
					}
					if effect.Before != nil {
						projections++
						if beforeBatch == nil || beforeBatch == old || beforeBatch.Revision() != old.Revision()+1 || effect.Before != old || !slices.Equal(effect.After.Attributes, []int32{1, 2, 5}) {
							t.Fatal("intermediate generated projection recorded", effect)
						}
					}
				}
				if projections != 1 {
					t.Fatal("batch was not projected once", projections)
				}
			}
		})
	}
}

func TestDropColumnRoutineFences(t *testing.T) {
	for _, change := range []string{"routine_revision", "root_revision", "replacement_epoch", "canceled"} {
		t.Run(change, func(t *testing.T) {
			e, base, owner, routines := columnRoutineCatalog(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			statement := new(int)
			tx := e.Catalog.Domain.Tx(ids.SessionToken{}, 1, base.Epoch(), nil, 0)
			prepared := &table.DropPreparation{Old: owner, Ref: e.Catalog.Domain.Ref(base.Epoch(), owner.Address()), Base: base, Discovery: base, Statement: statement, TxRef: tx}
			provider := &core.DropProvider{Base: base, Owner: e, Ctx: ctx, Prepared: prepared}
			target := owner.Address()
			target.SubID = owner.Attributes[0]
			if change == "canceled" {
				cancel()
				if err := provider.ColumnDependencies(target, parser.DropCascade); !errors.Is(err, context.Canceled) || len(prepared.Effects()) != 0 {
					t.Fatal("canceled preparation produced effects", err, prepared.Effects())
				}
				if e.CatalogRead().State() != base.State() || base.Get(routines["column_use"].Address()) != routines["column_use"] {
					t.Fatal("canceled preparation changed committed providers")
				}
				return
			}
			if err := provider.ColumnDependencies(target, parser.DropCascade); err != nil {
				t.Fatal(err)
			}
			if err := prepared.ValidateObservations(statement, tx, base); err != nil {
				t.Fatal("fresh preparation fenced itself", err)
			}
			switch change {
			case "routine_revision":
				enginetest.Run(t, e, "ALTER FUNCTION column_use() RENAME TO column_use_renamed")
			case "root_revision":
				enginetest.Run(t, e, "ALTER TABLE column_owner RENAME b TO renamed")
			case "replacement_epoch":
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			}
			current := e.CatalogRead()
			if change == "routine_revision" {
				next, ok := current.Procedure(routines["column_use"].Address())
				if !ok || next.Revision() == routines["column_use"].Revision() {
					t.Fatal("supported ALTER did not revise the same routine identity")
				}
			}
			if err := prepared.ValidateObservations(statement, tx, current); !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("stale column/routine effects remained admissible", err)
			}
			if e.CatalogRead().State() != current.State() || !slices.Equal(owner.Attributes, []int32{1, 2, 3}) || base.Get(routines["column_use"].Address()) != routines["column_use"] {
				t.Fatal("rejected preparation changed current or retained roots")
			}
		})
	}
	t.Run("stage_publication", func(t *testing.T) {
		for _, change := range []string{"routine_revision", "root_revision", "replacement_epoch", "canceled"} {
			t.Run(change, func(t *testing.T) {
				e, base, owner, routines := columnRoutineCatalog(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				stage := e.BeginCatalogStage(base)
				defer e.DiscardCatalogStage(stage)
				provider := core.StageDropProvider(ctx, stage, nil, nil, nil)
				target := owner.Address()
				target.SubID = owner.Attributes[0]
				if err := provider.ColumnDependencies(target, parser.DropCascade); err != nil {
					t.Fatal(err)
				}
				publication, err := e.ValidateCatalogStage(ctx, stage)
				if err != nil {
					t.Fatal(err)
				}
				switch change {
				case "routine_revision":
					enginetest.Run(t, e, "ALTER FUNCTION column_use() RENAME TO column_use_renamed")
				case "root_revision":
					enginetest.Run(t, e, "ALTER TABLE column_owner RENAME b TO renamed")
				case "replacement_epoch":
					if err := engine.Reset(context.Background(), e); err != nil {
						t.Fatal(err)
					}
				case "canceled":
					cancel()
				}
				current := e.CatalogRead()
				if change == "routine_revision" {
					next, ok := current.Procedure(routines["column_use"].Address())
					if !ok || next.Revision() == routines["column_use"].Revision() {
						t.Fatal("supported ALTER did not revise the same routine identity")
					}
				}
				changes, err := enginetest.PublishCatalog(e, publication, core.PublicationToken{Epoch: base.Epoch()})
				var conflict catalog.Conflict
				switch change {
				case "canceled":
					if !errors.Is(err, context.Canceled) {
						t.Fatal("canceled publication was not rejected", err)
					}
				case "replacement_epoch":
					if !errors.Is(err, ids.ErrCatalogReplaced) {
						t.Fatal("replacement publication was not rejected", err)
					}
				default:
					if !errors.As(err, &conflict) {
						t.Fatal("changed provider publication was not rejected", err)
					}
				}
				if len(changes.Definitions) != 0 || e.CatalogRead().State() != current.State() || !slices.Equal(owner.Attributes, []int32{1, 2, 3}) || base.Get(routines["column_use"].Address()) != routines["column_use"] {
					t.Fatal("failed publication leaked retirement or mutated retained roots", changes)
				}
			})
		}
	})
}
