package core

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// invokeNativeTrigger borrows row slots for one admitted invocation. It enters
// the same balanced body boundary as PL without constructing a PL frame.
func (s *StatementScope) invokeNativeTrigger(d *catalog.Procedure, relation *catalog.Relation, p stored.TriggerParts, event stored.TriggerEvent, next, old []sqltype.Value, targets []int32) (result dml.BeforeResult, err error) {
	result = dml.BeforeResult{Row: next, Proceed: true}
	if err = s.Op.Context().Err(); err != nil {
		return result, err
	}
	if err = s.enterSQLRoutine(); err != nil {
		return result, err
	}
	defer s.leaveSQLRoutine()
	s.enterTrigger()
	defer s.leaveTrigger()
	leave, err := s.enterRoutineEnvironment(d)
	if err != nil {
		return result, err
	}
	defer func() { err = leave(err) }()
	leavePolicy := s.activateRoutineCommandPolicy(d, false)
	defer leavePolicy()
	if extension.TcnProviderOf(d) == extension.StockTcn {
		err = s.notifyTriggerChange(relation, p, event, next, old)
		return result, err
	}
	if extension.RefintProviderOf(d) == extension.StockRefint {
		err = s.invokeRefint(d, relation, p, event, next, old)
		return result, err
	}
	if extension.AutoincProviderOf(d) == extension.StockAutoinc {
		result.Row, err = s.autoincTrigger(relation, p, event, next)
		return result, err
	}
	if extension.StockInsertUsername.SupportsDMLTrigger(d) {
		err = s.insertTriggerUsername(relation, p, event, next)
		return result, err
	}
	if extension.ModdatetimeProviderOf(d) == extension.StockModdatetime {
		err = s.updateModdatetime(relation, p, event, next)
		return result, err
	}
	if extension.StockLo.SupportsDMLTrigger(d) {
		if event == stored.TriggerDelete {
			result.Row = old
		}
		err = s.manageLargeObjectReference(relation, p, event, next, old, targets)
		return result, err
	}
	builtin := catalog.BuiltinDMLTrigger(d)
	if err = builtinTriggerProtocol(builtin == 1291, p, event); err != nil {
		return result, err
	}
	if builtin == 1291 {
		result.Proceed, err = triggerRowChanged(s.Op.Context(), old, next)
		return result, err
	}
	err = s.updateTriggerVector(builtin == 3753, relation, p.Arguments, event, next, targets)
	return result, err
}

func builtinTriggerProtocol(suppress bool, p stored.TriggerParts, event stored.TriggerEvent) error {
	if suppress {
		if event != stored.TriggerUpdate {
			return SQLError("39P01", "suppress_redundant_updates_trigger: must be called on update")
		}
		if p.Timing != stored.TriggerBefore {
			return SQLError("39P01", "suppress_redundant_updates_trigger: must be called before update")
		}
		if p.Level != stored.TriggerRow {
			return SQLError("39P01", "suppress_redundant_updates_trigger: must be called for each row")
		}
		return nil
	}
	if p.Level != stored.TriggerRow {
		return SQLError("XX000", "tsvector_update_trigger: must be fired for row")
	}
	if p.Timing != stored.TriggerBefore {
		return SQLError("XX000", "tsvector_update_trigger: must be fired BEFORE event")
	}
	if event != stored.TriggerInsert && event != stored.TriggerUpdate {
		return SQLError("XX000", "tsvector_update_trigger: must be fired for INSERT or UPDATE")
	}
	if len(p.Arguments) < 3 {
		return SQLError("XX000", "tsvector_update_trigger: arguments must be tsvector_field, ts_config, text_field1, ...)")
	}
	return nil
}

func (s *StatementScope) updateTriggerVector(byColumn bool, relation *catalog.Relation, args []string, event stored.TriggerEvent, next []sqltype.Value, targets []int32) error {
	ctx := s.executionContext()
	e := s.Engine()
	e.Mu.Lock()
	auth, err := s.namespaceAuthorizationLocked(e)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	read := auth.Catalog
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
			return 0, SQLError("42703", fmt.Sprintf(missing, name))
		}
		if _, ok := catalog.ImplicitBinaryCast(read, id, target); !ok {
			return 0, SQLError("42804", fmt.Sprintf(wrong, name))
		}
		return slot, nil
	}
	destination, err := column(args[0], "tsvector column %q does not exist", "column %q is not of tsvector type", sqltype.TSVector)
	if err != nil {
		return err
	}
	var oid uint32
	var captured textSearchCapture
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
			return SQLError("22004", fmt.Sprintf("configuration column %q must not be null", args[1]))
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
		oid, err = regobj.NewTextSearchInput(inputCapture(auth, nil)).ResolveTriggerConfig(ctx, args[1])
		if err != nil {
			return err
		}
		e.Mu.Lock()
		captured, err = captureTextSearch(ctx, read, oid)
		e.Mu.Unlock()
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
	ctx = s.ActivateRuntime(ctx)
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
		if captured.config == nil {
			e.Mu.Lock()
			captured, err = captureTextSearch(ctx, read, oid)
			e.Mu.Unlock()
			if err != nil {
				return err
			}
		}
		// Parsing remains observable on non-rebuilding UPDATEs too, but the
		// final-vector builder need not retain any lexical output in that case.
		if err := captured.walk(ctx, s, text, &position, func(p fts.LexicalPosition) {
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
		if captured.acquisition != nil {
			return captured.acquisition.accept(ctx, s.WriteHost())
		}
		return nil
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
	if captured.acquisition != nil {
		if err := captured.acquisition.accept(ctx, s.WriteHost()); err != nil {
			return err
		}
	}
	next[destination] = value
	return nil
}
