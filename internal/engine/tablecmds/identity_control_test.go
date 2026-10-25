package tablecmds_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

func TestIdentityControlsTemporaryCollision(t *testing.T) {
	for _, mode := range []string{"ReplaceCatalog", "Restore"} {
		t.Run(mode, func(t *testing.T) {
			source, dest := engine.New(), engine.New()
			a, b := enginetest.StartSession(t, source, nil), enginetest.StartSession(t, dest, nil)
			enginetest.SessionSQL(t, a, `CREATE SCHEMA collision_padding`)
			enginetest.SessionSQL(t, a, `CREATE SCHEMA portable`)
			enginetest.SessionSQL(t, a, `CREATE TABLE portable.i(id INT GENERATED ALWAYS AS IDENTITY(START 7 CACHE 2))`)
			enginetest.SessionSQL(t, a, `INSERT INTO portable.i DEFAULT VALUES`)
			// Consume one persistent relation address before TEMP namespace/sequence
			// creation so kept collides with the source's linked identity sequence.
			enginetest.SessionSQL(t, b, `CREATE TABLE displaced(v INT)`)
			enginetest.SessionSQL(t, b, `CREATE TEMP SEQUENCE kept START 30 CACHE 10`)
			sequenceScalar(t, b, `SELECT nextval('kept')`, 30)
			_, tempDescription := sequencePreparedDescription(t, dest, b, `SELECT nextval('kept')`)
			tempRef := tempDescription.Sequences.Constants[0].Ref.Address
			sourceRef := enginetest.RoleTableRef(t, source, "portable.i").Address
			original := source.Catalog.State.MustRelation(sourceRef)
			originalIdentity := original.ColumnMeta.Identities[original.Attributes[0]]
			if originalIdentity.Sequence != tempRef {
				t.Fatal("fixture lacks actual sequence/TEMP collision", originalIdentity.Sequence, tempRef)
			}
			originalSequence := source.Catalog.State.MustRelation(originalIdentity.Sequence)
			originalOwned := sourceRef
			originalOwned.SubID = original.Attributes[0]
			if !originalSequence.Sequence.Identity || originalSequence.Sequence.Owned != originalOwned {
				t.Fatal("source identity lacks reciprocal ownership")
			}
			tempDefinition, tempGenerator, tempMemory := dest.Catalog.State.Get(tempRef), dest.Storage.Sequences[tempRef], core.PayloadOf(b).Sequences.Memory(tempRef)
			generatorState, memoryState := *tempGenerator, *tempMemory
			if mode == "Restore" {
				var dump bytes.Buffer
				if err := engine.Dump(context.Background(), source, &dump); err != nil {
					t.Fatal(err)
				}
				if err := engine.Restore(context.Background(), dest, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := dest.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
					t.Fatal(err)
				}
			}
			if dest.Catalog.State.Get(tempRef) != tempDefinition || dest.Storage.Sequences[tempRef] != tempGenerator || core.PayloadOf(b).Sequences.Memory(tempRef) != tempMemory || *tempGenerator != generatorState || *tempMemory != memoryState {
				t.Fatal("retained TEMP authority changed")
			}
			ref := enginetest.RoleTableRef(t, dest, "portable.i").Address
			table := dest.Catalog.State.MustRelation(ref)
			identity := table.ColumnMeta.Identities[table.Attributes[0]]
			if identity.Sequence == tempRef {
				t.Fatal("identity forward address not remapped")
			}
			sequence := dest.Catalog.State.MustRelation(identity.Sequence)
			owned := ref
			owned.SubID = table.Attributes[0]
			if !sequence.Sequence.Identity || sequence.Sequence.Owned != owned {
				t.Fatal("identity reciprocal address not remapped")
			}
			enginetest.SessionSQL(t, b, `INSERT INTO portable.i DEFAULT VALUES`)
			rows := enginetest.SessionSQL(t, b, `SELECT id FROM portable.i ORDER BY id`)
			got := enginetest.Tuples(rows)
			rows.Rows.Close()
			if len(got) != 2 || got[0][0] != int64(7) || got[1][0] != int64(9) {
				t.Fatal("restored state/rows", got)
			}
			sequenceScalar(t, b, `SELECT nextval('kept')`, 31)
			enginetest.SessionSQL(t, b, `DROP TABLE portable.i`)
			if dest.Catalog.State.Get(identity.Sequence) != nil || dest.Storage.Sequences[identity.Sequence] != nil {
				t.Fatal("remapped dependency closure failed")
			}
			if source.Catalog.State.Get(sourceRef) != original || original.ColumnMeta.Identities[original.Attributes[0]] != originalIdentity {
				t.Fatal("source immutable definition changed")
			}
		})
	}
}
