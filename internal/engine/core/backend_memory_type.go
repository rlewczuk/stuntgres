package core

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// Called only during private engine construction, after source columns have
// current-generation types and before seed validation/publication. Only this
// source's existing nominal pair gains concrete whole-row/array value support.
func (e *Engine) initializeBackendMemoryRowType() error {
	view := e.Catalog.Current()
	relation, ok := view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.BackendMemoryContextsOID})
	if !ok || relation.RowType != 12144 || relation.Name() != "pg_backend_memory_contexts" {
		return fmt.Errorf("stuntgres: missing backend memory row owner")
	}
	row, rowOK := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12144})
	array, arrayOK := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12143})
	if !rowOK || !arrayOK || row.Array != 12143 || row.OwningRelation != relation.Address() || array.OwningRelation != relation.Address() ||
		row.Namespace() != relation.Namespace() || array.Namespace() != relation.Namespace() || row.Owner != relation.Owner || array.Owner != relation.Owner ||
		row.Name() != relation.Name() || array.Name() != "_pg_backend_memory_contexts" {
		return fmt.Errorf("stuntgres: invalid backend memory nominal pair")
	}
	facts, err := pgcatalog.TypeFacts(context.Background(), view, row, sqltype.OutputEnvironment{})
	if err != nil {
		return err
	}
	if facts.Kind != "c" {
		return fmt.Errorf("stuntgres: backend memory row is not a catalog composite")
	}
	completed, companion, err := sqltype.CompleteBuiltinComposite(row.SQLType, array.SQLType, typedef.CompositeMembers(relation))
	if err != nil {
		return err
	}
	// Copy catalog metadata as well as SQL descriptors. Older generation owners
	// and the process builtin template must retain their original identities.
	nextRow, nextArray := *row, *array
	nextRow.SQLType, nextArray.SQLType = completed, companion
	e.Catalog.State.SetDescriptor(nextRow.Address(), &nextRow)
	e.Catalog.State.SetDescriptor(nextArray.Address(), &nextArray)
	return nil
}
