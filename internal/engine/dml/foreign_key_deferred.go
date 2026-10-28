package dml

import (
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

// Enlist only the check-side and NO ACTION work. Referential actions have
// already run. Trigger suppression was captured at row admission, not here.
func enlistForeignKeyEvent(h FinishHost, auth Authority, job ForeignKeyJob, transition ForeignActionTransition, parentEvent bool) {
	view := auth.Access().Catalog
	owner := transition.Owner
	if owner.ID() == 0 {
		owner = auth.Access().State.CurrentUser
	}
	payload := &index.ForeignKeyEvent{Child: view.Domain().Ref(view.Epoch(), job.Child.Address()), Parent: view.Domain().Ref(view.Epoch(), job.Parent.Address()), Index: view.Domain().Ref(view.Epoch(), job.Index.Address()), Old: foreignEventRow(transition.Old), Next: foreignEventRow(transition.Next), Owner: owner, ParentEvent: parentEvent, Definition: job.Definition}
	row := transition.Next
	if parentEvent {
		row = transition.Old
	}
	event := index.ConstraintEvent{Parent: view.Domain().Ref(view.Epoch(), transition.Relation), Constraint: job.Definition.OID, Row: row.Row, Version: row.Version, ForeignKey: payload}
	h.IndexScope(true).AppendConstraintEvent(event)
}

func foreignEventRow(row storage.RowVersion) storage.RowVersion {
	return storage.RowVersion{Row: row.Row, Version: row.Version, Base: row.Base, Born: row.Born, Values: row.Values}
}

// CheckDeferredForeignKey binds fresh providers without retaining a statement
// job, frame or authority across commands. Original identities, timing and actor
// remain event-owned; successful checks never manufacture publication rights.
func CheckDeferredForeignKey(h FinishHost, event index.ConstraintEvent) error {
	payload := event.ForeignKey
	auth, err := h.ForeignKeyAuthority()
	if err != nil {
		return err
	}
	view := auth.Access().Catalog
	if payload.Child.Domain() != view.Domain() || payload.Child.Epoch != view.Epoch() || payload.Parent.Epoch != view.Epoch() || payload.Index.Epoch != view.Epoch() {
		return ids.ErrCatalogReplaced
	}
	child, cok := view.Relation(payload.Child.Address)
	parent, pok := view.Relation(payload.Parent.Address)
	idx, iok := view.Relation(payload.Index.Address)
	if !cok || !pok || !iok {
		return nil
	}
	var job ForeignKeyJob
	found := false
	for _, definition := range child.Constraints.ForeignKeys {
		if definition.OID != event.Constraint {
			continue
		}
		// A legitimate FK timing ALTER keeps existing queued trigger flags.
		// The executable definition still has to match its original providers.
		original := payload.Definition
		original.Deferrable, original.InitiallyDeferred = definition.Deferrable, definition.InitiallyDeferred
		if !SameForeignKeyDefinition(original, definition) {
			return ids.ErrCatalogReplaced
		}
		job = ForeignKeyJob{Child: child, Parent: parent, Index: idx, Definition: definition}
		found = true
		break
	}
	if !found {
		return nil
	}
	job.Comparisons, err = table.BindForeignKeyComparison(view, child, parent, idx, job.Definition)
	if err != nil {
		return err
	}
	job.ChildKey = make([]sqltype.Value, len(job.Comparisons))
	auth = auth.ActingAs(payload.Owner)
	parentRows, err := h.ForeignKeyRows(payload.Parent, job.Definition.Index)
	if err != nil {
		return err
	}
	input, output := h.Environments()
	if payload.ParentEvent {
		children, err := h.ForeignKeyRows(payload.Child, ids.ObjectAddress{})
		if err != nil {
			return err
		}
		return children.Each(func(row storage.RowVersion) (bool, error) {
			match, err := table.ForeignKeyRowsMatch(h.Context(), job.Comparisons, row.Values, payload.Old.Values, input)
			if err != nil || !match {
				return false, err
			}
			found, err := foreignKeyParentExists(h, payload.Parent, job, &parentRows, row.Values, false)
			if err != nil || found {
				return false, err
			}
			return false, table.ForeignKeyDiagnostic(h.Context(), auth.Access(), auth, child, parent, job.Definition, payload.Old.Values, true, false, output)
		})
	}
	private := h.PrivateRows(payload.Child.Address)
	row, _, live := storage.RowByID(private.Staged, event.Row, -1)
	if !live {
		row, _, live = storage.RowByID(private.Own, event.Row, -1)
	}
	if !live || row.Version != event.Version || index.UniqueRowDeleted(event.Row, private.OwnDeletes, private.StagedDeletes) {
		return nil
	}
	any, all := table.ForeignKeyNullState(job.Comparisons, row.Values)
	if any {
		if job.Definition.Full && !all {
			return table.ForeignKeyDiagnostic(h.Context(), auth.Access(), auth, child, parent, job.Definition, row.Values, false, true, output)
		}
		return nil
	}
	if payload.Old.Born != 0 {
		unchanged := true
		for _, comparison := range job.Comparisons {
			equal, err := comparison.Comparison.EqualChildren(h.Context(), payload.Old.Values[comparison.Child], row.Values[comparison.Child], input)
			if err != nil {
				return err
			}
			if !equal {
				unchanged = false
				break
			}
		}
		if unchanged {
			return nil
		}
	}
	found, err = foreignKeyParentExists(h, payload.Parent, job, &parentRows, row.Values, true)
	if err != nil {
		return err
	}
	if !found {
		if err := h.CheckForeignKeySnapshot(payload.Parent, job, row.Values); err != nil {
			return err
		}
		return table.ForeignKeyDiagnostic(h.Context(), auth.Access(), auth, child, parent, job.Definition, row.Values, false, false, output)
	}
	return h.Context().Err()
}

// ForeignKeyCheckQuery is diagnostic text for the actual RI key-share probe,
// not SQL executed by an alternate enforcement path.
func ForeignKeyCheckQuery(job ForeignKeyJob, schema string) string {
	var query strings.Builder
	query.WriteString("SELECT 1 FROM ONLY ")
	query.WriteString(catalog.QuoteIdentifier(schema))
	query.WriteByte('.')
	query.WriteString(catalog.QuoteIdentifier(job.Parent.Name()))
	query.WriteString(" x WHERE ")
	for i, c := range job.Comparisons {
		if i != 0 {
			query.WriteString(" AND ")
		}
		query.WriteString(catalog.QuoteIdentifier(job.Parent.Columns[c.Parent].Name))
		query.WriteString(" OPERATOR(pg_catalog.=) $")
		query.WriteString(strconv.Itoa(i + 1))
	}
	query.WriteString(" FOR KEY SHARE OF x")
	return query.String()
}
