package core

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/tsearch"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// Invoke runs one admitted native trigger body inside the body boundary core
// entered for the invocation: the installed tcn, refint, autoinc,
// insert_username, moddatetime or lo_manage provider, or the builtin
// suppress_redundant_updates_trigger and tsvector_update_trigger bodies. Row
// slots are borrowed for this call only.
func Invoke[A tsearch.Authority[A]](h Host[A], d *catalog.Procedure, relation *catalog.Relation, p stored.TriggerParts, event stored.TriggerEvent, next, old []sqltype.Value, targets []int32) (result dml.BeforeResult, err error) {
	result = dml.BeforeResult{Row: next, Proceed: true}
	if extension.TcnProviderOf(d) == extension.StockTcn {
		err = notifyTriggerChange(h, relation, p, event, next, old)
		return result, err
	}
	if extension.RefintProviderOf(d) == extension.StockRefint {
		err = invokeRefint(h, d, relation, p, event, next, old)
		return result, err
	}
	if extension.AutoincProviderOf(d) == extension.StockAutoinc {
		result.Row, err = autoincTrigger(h, relation, p, event, next)
		return result, err
	}
	if extension.StockInsertUsername.SupportsDMLTrigger(d) {
		err = insertTriggerUsername(h, relation, p, event, next)
		return result, err
	}
	if extension.ModdatetimeProviderOf(d) == extension.StockModdatetime {
		err = updateModdatetime(h, relation, p, event, next)
		return result, err
	}
	if extension.StockLo.SupportsDMLTrigger(d) {
		if event == stored.TriggerDelete {
			result.Row = old
		}
		err = manageLargeObjectReference(h, relation, p, event, next, old, targets)
		return result, err
	}
	builtin := catalog.BuiltinDMLTrigger(d)
	if err = builtinTriggerProtocol(builtin == 1291, p, event); err != nil {
		return result, err
	}
	if builtin == 1291 {
		result.Proceed, err = triggerRowChanged(h.Operation().Context(), old, next)
		return result, err
	}
	err = updateTriggerVector(h, builtin == 3753, relation, p.Arguments, event, next, targets)
	return result, err
}

func builtinTriggerProtocol(suppress bool, p stored.TriggerParts, event stored.TriggerEvent) error {
	if suppress {
		if event != stored.TriggerUpdate {
			return sqlError("39P01", "suppress_redundant_updates_trigger: must be called on update")
		}
		if p.Timing != stored.TriggerBefore {
			return sqlError("39P01", "suppress_redundant_updates_trigger: must be called before update")
		}
		if p.Level != stored.TriggerRow {
			return sqlError("39P01", "suppress_redundant_updates_trigger: must be called for each row")
		}
		return nil
	}
	if p.Level != stored.TriggerRow {
		return sqlError("XX000", "tsvector_update_trigger: must be fired for row")
	}
	if p.Timing != stored.TriggerBefore {
		return sqlError("XX000", "tsvector_update_trigger: must be fired BEFORE event")
	}
	if event != stored.TriggerInsert && event != stored.TriggerUpdate {
		return sqlError("XX000", "tsvector_update_trigger: must be fired for INSERT or UPDATE")
	}
	if len(p.Arguments) < 3 {
		return sqlError("XX000", "tsvector_update_trigger: arguments must be tsvector_field, ts_config, text_field1, ...)")
	}
	return nil
}

func updateTriggerVector[A tsearch.Authority[A]](h Host[A], byColumn bool, relation *catalog.Relation, args []string, event stored.TriggerEvent, next []sqltype.Value, targets []int32) error {
	ctx := h.Context()
	h.ReacquireEngine()
	auth, err := h.NamespaceAuthorizationLocked()
	h.ReleaseEngineLocked()
	if err != nil {
		return err
	}
	read := auth.Access().Catalog
	column := func(name, missing, wrong string, target sqltype.ID) (int, error) {
		slot, ok := relation.Index[name]
		var id sqltype.ID
		if ok {
			id = relation.Columns[slot].Usage.Type.ID()
		} else if relation.HasSystemAttributes() && catalog.IsSystemColumn(name) {
			id, _ = sqltype.SystemAttributeType(name)
			slot = -1
			if name == "tableoid" && relation.HasSupportedAttribute(-6) {
				slot = -6
			}
		} else {
			return 0, sqlError("42703", fmt.Sprintf(missing, name))
		}
		if _, ok := catalog.ImplicitBinaryCast(read, id, target); !ok {
			return 0, sqlError("42804", fmt.Sprintf(wrong, name))
		}
		return slot, nil
	}
	destination, err := column(args[0], "tsvector column %q does not exist", "column %q is not of tsvector type", sqltype.TSVector)
	if err != nil {
		return err
	}
	var oid uint32
	var captured tsearch.Capture[A]
	if byColumn {
		slot, err := column(args[1], "configuration column %q does not exist", "column %q is not of regconfig type", sqltype.Regconfig)
		if err != nil {
			return err
		}
		config, err := triggerNewColumnValue(read, next, slot)
		if err != nil {
			return err
		}
		if config.IsNull() {
			return sqlError("22004", fmt.Sprintf("configuration column %q must not be null", args[1]))
		}
		value, err := triggerBinaryValue(ctx, read, config, sqltype.Regconfig)
		if err != nil {
			return err
		}
		oid, err = value.RegTSOID()
		if err != nil {
			return err
		}
	} else {
		oid, err = h.TextSearchInput(auth).ResolveTriggerConfig(ctx, args[1])
		if err != nil {
			return err
		}
		h.ReacquireEngine()
		captured, err = tsearch.CaptureConfiguration[A](ctx, read, oid)
		h.ReleaseEngineLocked()
		if err != nil {
			return err
		}
	}
	rebuild := event == stored.TriggerInsert
	if !rebuild {
		for _, name := range args[2:] {
			if slot, ok := relation.Index[name]; ok && slices.Contains(targets, relation.Attributes[slot]) {
				rebuild = true
				break
			}
		}
	}
	ctx = h.ActivateRuntime(ctx)
	position := 0
	var builder fts.VectorBuilder
	for _, name := range args[2:] {
		if err := ctx.Err(); err != nil {
			return err
		}
		slot, err := column(name, "column %q does not exist", "column %q is not of a character type", sqltype.Text)
		if err != nil {
			return err
		}
		source, err := triggerNewColumnValue(read, next, slot)
		if err != nil {
			return err
		}
		if source.IsNull() {
			continue
		}
		value, err := triggerBinaryValue(ctx, read, source, sqltype.Text)
		if err != nil {
			return err
		}
		text, err := value.Text()
		if err != nil {
			return err
		}
		if !captured.Captured() {
			h.ReacquireEngine()
			captured, err = tsearch.CaptureConfiguration[A](ctx, read, oid)
			h.ReleaseEngineLocked()
			if err != nil {
				return err
			}
		}
		// Parsing remains observable on non-rebuilding UPDATEs too, but the
		// final-vector builder need not retain any lexical output in that case.
		if err := captured.Walk(ctx, h, text, &position, func(p fts.LexicalPosition) {
			if !rebuild {
				return
			}
			for _, word := range p.Lexemes {
				builder.Add(word.Text, p.Position)
			}
		}); err != nil {
			return err
		}
	}
	if !rebuild {
		return captured.Accept(ctx, h)
	}
	vector, err := builder.Finish(ctx)
	if err != nil {
		return err
	}
	typ, _ := read.LookupType(sqltype.TSVector)
	value, err := sqltype.TSVectorValue(typ, vector)
	if err != nil {
		return err
	}
	// Tuple replacement is not SQL assignment: retain the destination's
	// owned logical tag without performing fresh domain input checks.
	value, err = value.DomainValue(relation.Columns[destination].Usage.Type)
	if err != nil {
		return err
	}
	if err := captured.Accept(ctx, h); err != nil {
		return err
	}
	next[destination] = value
	return nil
}
