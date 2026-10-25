package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/index"
)

func TestExclusionReplacement(t *testing.T) {
	for _, self := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreign", true: "same_owner"}[self], func(t *testing.T) {
			ctx := context.Background()
			source := engine.New()
			a := enginetest.StartSession(t, source, nil)
			// Match the destination's actual table/toast namespace allocation.
			enginetest.SessionSQL(t, a, "CREATE SCHEMA collision_padding")
			enginetest.SessionSQL(t, a, "CREATE SCHEMA portable CREATE TABLE t(k INT,CONSTRAINT ex EXCLUDE USING btree(k WITH =) DEFERRABLE INITIALLY DEFERRED)")
			enginetest.SessionSQL(t, a, "INSERT INTO portable.t VALUES(7)")
			enginetest.SessionSQL(t, a, "CREATE DOMAIN portable.d AS INT")
			enginetest.SessionSQL(t, a, "CREATE TABLE portable.domain_t(k portable.d,CONSTRAINT domain_ex EXCLUDE USING hash(k WITH =))")
			enginetest.SessionSQL(t, a, "INSERT INTO portable.domain_t VALUES(7)")
			before := source.CatalogRead()
			original := before.MustRelation(enginetest.RoleTableRef(t, source, "portable.t").Address)
			constraint := original.Constraints.Exclusion[0]
			sourceRoot := source.Storage.IndexRoots[constraint.Index]
			target := engine.New()
			b := enginetest.StartSession(t, target, nil)
			if self {
				target, b = source, a
			}
			enginetest.SessionSQL(t, b, "CREATE TEMP SEQUENCE kept CACHE 10")
			sequenceScalar(t, b, "SELECT nextval('kept')", 1)
			_, description := sequencePreparedDescription(t, target, b, "SELECT nextval('kept')")
			tempRef := description.Sequences.Constants[0].Ref
			temp, generator, memory := target.Catalog.State.Get(tempRef.Address), target.Storage.Sequences[tempRef.Address], engine.PayloadOf(b).Sequences.Memory(tempRef.Address)
			if !self && tempRef.Address.ObjectID != original.Address().ObjectID {
				t.Fatal("fixture must collide with a real retained TEMP address")
			}
			if _, err := target.ReplaceCatalog(ctx, source, before); err != nil {
				t.Fatal(err)
			}
			if target.Catalog.State.Get(tempRef.Address) != temp || target.Storage.Sequences[tempRef.Address] != generator || engine.PayloadOf(b).Sequences.Memory(tempRef.Address) != memory {
				t.Fatal("replacement changed retained TEMP identity")
			}
			parent := target.Catalog.State.MustRelation(enginetest.RoleTableRef(t, target, "portable.t").Address)
			idx, root := btreeRoot(t, target, "portable.ex")
			current := parent.Constraints.Exclusion[0]
			if current.Index != idx.Address() || !index.BackingConstraintPresent(parent, idx) || !current.Deferrable || !current.InitiallyDeferred || len(root.Entries) != 1 {
				t.Fatal("replacement broke the actual exclusion closure")
			}
			if root == sourceRoot || self && root.Entries[0].Row == sourceRoot.Entries[0].Row {
				t.Fatal("replacement imported source root or row identities")
			}
			if !reflect.DeepEqual(original.Constraints.Exclusion[0], constraint) {
				t.Fatal("replacement mutated the source definition")
			}
			// Domain providers must bind the destination descriptor, not merely its OID.
			result := enginetest.SessionSQL(t, b, "INSERT INTO portable.domain_t VALUES(7) ON CONFLICT ON CONSTRAINT domain_ex DO NOTHING")
			if result.Count != 0 {
				t.Fatal("destination domain equality did not recognize its imported key")
			}
			sequenceScalar(t, b, "SELECT nextval('kept')", 2)
			if target.Exclusions.CountLocked() != 0 {
				t.Fatal("replacement retained transient exclusion state")
			}
			uniqueClaimsEmpty(t, target)
		})
	}
}
