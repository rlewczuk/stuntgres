package control

import (
	"context"
	"hash"
	"io"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
)

// dumpWriter writes one Dump script and tracks the session authority the
// replayed statements run under. It lives for one Dump call.
type dumpWriter struct {
	ctx               context.Context
	dst               io.Writer
	currentOwner      string
	comparisonContext bool
	seal hash.Hash
	sealBuffer []byte
}

// emit writes one block of statements and honors cancellation before and
// after the write.
func (w *dumpWriter) emit(s string) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	n, err := io.WriteString(w.dst, s)
	if err != nil {
		return err
	}
	if n != len(s) {
		return io.ErrShortWrite
	}
	if w.seal != nil {
		remaining := s
		for len(remaining) != 0 {
			n := copy(w.sealBuffer, remaining)
			_, _ = w.seal.Write(w.sealBuffer[:n])
			remaining = remaining[n:]
		}
	}
	return w.ctx.Err()
}

// setOwner switches the replay authority when it changes. The interpreter
// need not be logged in as the native bootstrap owner, so even the first
// owner is established explicitly.
func (w *dumpWriter) setOwner(owner string) error {
	if w.currentOwner == owner {
		return nil
	}
	return w.forceOwner(owner)
}

// forceOwner writes an authority switch even when owner is already current.
func (w *dumpWriter) forceOwner(owner string) error {
	if err := w.emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(owner, "'", "''") + "';\n"); err != nil {
		return err
	}
	w.currentOwner = owner
	return nil
}

// dumpPhaseEmitter is one entry of Dump's Emit table.
type dumpPhaseEmitter struct {
	// owner names the authority the event loop establishes before emit
	// runs. A nil owner leaves the authority to emit.
	owner func(*dumpScript, dump.Event) string
	// emit writes the event's statements; a nil emit writes nothing.
	emit func(context.Context, *dumpScript, *dumpWriter, dump.Event) error
}

// dumpPhaseEmitters is the Emit table, indexed by phase. Domain checks
// render before switching authority, so a rendering error writes nothing.
var dumpPhaseEmitters = [...]dumpPhaseEmitter{
	dump.Declaration:             {nil, emitDumpDeclaration},
	dump.Rename:                  {dumpBootstrapOwner, emitDumpRename},
	dump.Defaults:                {dumpTableOwner, emitDumpDefaults},
	dump.Rows:                    {dumpTableOwner, emitDumpRows},
	dump.InvalidConstraints:      {dumpInvalidConstraintOwner, emitDumpInvalidConstraints},
	dump.IndexAttachment:         {dumpBootstrapOwner, emitDumpIndexAttachment},
	dump.TableAttachment:         {dumpBootstrapOwner, emitDumpTableAttachment},
	dump.TableCompletion:         {}, // orders later phases only
	dump.InvalidIndexPreparation: {dumpBootstrapOwner, emitDumpInvalidIndex},
	dump.InvalidIndexCompletion:  {dumpBootstrapOwner, emitDumpInvalidIndex},
	dump.InheritanceRecipes:      {dumpTableOwner, emitDumpInheritanceRecipes},
	dump.InheritanceChecks:       {dumpBootstrapOwner, emitDumpInheritanceChecks},
	dump.RetiredConstraints:      {dumpBootstrapOwner, emitDumpRetiredConstraints},
	dump.InheritedForeignKeys:    {dumpBootstrapOwner, emitDumpInheritedForeignKeys},
	dump.DomainChecks:            {nil, emitDumpDomainChecks},
	dump.OperatorLinks:           {dumpBootstrapOwner, emitDumpOperatorLinks},
	dump.AttributeRetirement:     {dumpBootstrapOwner, emitDumpAttributeRetirement},
	dump.AttributeConstruction:   {dumpBootstrapOwner, emitDumpAttributeConstruction},
	dump.AttributeCompletion:     {dumpBootstrapOwner, emitDumpAttributeCompletion},
}

// emitDumpEvents writes every ordered event through its phase's entry.
func emitDumpEvents(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	for _, event := range s.eventOrder {
		if event.Phase == dump.Declaration && s.lateForeignCasts[event.OID] {
			continue
		}
		if err := s.openCastWindow(w, castConsumer{Event: event}); err != nil {
			return err
		}
		entry := dumpPhaseEmitters[event.Phase]
		if entry.owner != nil {
			if err := w.setOwner(entry.owner(s, event)); err != nil {
				return err
			}
		}
		if entry.emit != nil {
			if err := entry.emit(ctx, s, w, event); err != nil {
				return err
			}
		}
		if event.Phase == dump.Declaration {
			if err := emitDumpProviderEvent(s, w, event.OID); err != nil {
				return err
			}
			if err := emitObjectACLStatements(ctx, w, s.typeACLStatements[event.OID]); err != nil {
				return err
			}
		}
		if err := s.closeCastWindow(w, castConsumer{Event: event}); err != nil {
			return err
		}
	}
	return nil
}

// table returns a copy of the captured table record for oid; emitters may
// adjust the copy without changing the capture.
func (s *dumpScript) table(oid ids.OID) table.DumpRelation {
	return s.Tables[s.TableIndices[oid]]
}

// dumpBootstrapOwner replays post-data phases as the existing bootstrap
// authority.
func dumpBootstrapOwner(*dumpScript, dump.Event) string { return "postgres" }

func dumpTableOwner(s *dumpScript, event dump.Event) string { return s.table(event.OID).Owner }

// dumpInvalidConstraintOwner validates inheritance participants' NOT VALID
// constraints as bootstrap and others as the table owner.
func dumpInvalidConstraintOwner(s *dumpScript, event dump.Event) string {
	t := s.table(event.OID)
	if catalog.InheritanceParticipant(s.OriginalCatalog, t.Descriptor) {
		return "postgres"
	}
	return t.Owner
}

func dumpEventRelation(view catalog.Read, event dump.Event) *catalog.Relation {
	return view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: event.OID})
}

func emitDumpRename(_ context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	return w.emit(s.ConstraintReplay.FinalRenameSQL(dumpEventRelation(s.OriginalCatalog, event)))
}

func emitDumpIndexAttachment(_ context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	return w.emit(partition.IndexDumpAttachmentSQL(s.OriginalCatalog, dumpEventRelation(s.OriginalCatalog, event)))
}

func emitDumpTableAttachment(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	descriptor := dumpEventRelation(s.OriginalCatalog, event)
	text, err := partition.AttachDumpSQL(ctx, s.OriginalCatalog, descriptor)
	if err != nil {
		return err
	}
	text += s.ConstraintReplay.MembershipRenames(s.CatalogView, descriptor, event)
	return w.emit(text)
}

func emitDumpInvalidIndex(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	text, err := s.invalidReplay[event.OID].SQL(ctx, s.OriginalCatalog, event.Phase)
	if err != nil {
		return err
	}
	return w.emit(text)
}

func emitDumpDefaults(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	t := s.table(event.OID)
	for _, attribute := range t.Attributes {
		definition, exists := t.Defaults[attribute]
		if !exists || !t.DeferredDefinitions[attribute] {
			continue
		}
		member := t
		member.Defaults = map[int32]catalog.ColumnDefault{attribute: definition}
		site := castConsumer{Event: event, Attribute: attribute}
		if err := s.emitCastConsumer(w, site, t.Owner, func(emit func(string) error) error {
			return member.EmitDeferredDefaults(ctx, s.OriginalCatalog, emit)
		}); err != nil {
			return err
		}
	}
	return nil
}

func emitDumpRows(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	t := s.table(event.OID)
	return t.EmitRows(ctx, s.OriginalCatalog, w.emit)
}

func emitDumpInvalidConstraints(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	t := s.table(event.OID)
	// Non-CHECK constraints have no scalar recipe windows.
	descriptor := *t.Descriptor
	descriptor.Constraints.Check = nil
	base := t
	base.Descriptor = &descriptor
	if err := base.EmitInvalidConstraints(ctx, s.OriginalCatalog, w.emit); err != nil {
		return err
	}
	for _, check := range table.OrderedChecks(t.Descriptor) {
		if !check.NotValid || !table.DumpCheckDeclaration(s.OriginalCatalog, t.Descriptor, check) {
			continue
		}
		member := t
		d := *t.Descriptor
		d.Constraints.Check = []catalog.CheckConstraint{check}
		d.Constraints.ForeignKeys = nil
		member.Descriptor = &d
		member.NotNullConstraints = nil
		if err := s.emitCastConsumer(w, castConsumer{Event: event, Check: check.Name}, dumpInvalidConstraintOwner(s, event), func(emit func(string) error) error {
			return member.EmitInvalidConstraints(ctx, s.OriginalCatalog, emit)
		}); err != nil {
			return err
		}
	}
	return nil
}

func emitDumpInheritanceRecipes(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	t := s.table(event.OID)
	for _, attribute := range t.Attributes {
		generation, exists := t.Descriptor.ColumnMeta.Generations[attribute]
		if !exists {
			continue
		}
		member := t
		d := *t.Descriptor
		d.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{attribute: generation}
		member.Descriptor = &d
		if err := s.emitCastConsumer(w, castConsumer{Event: event, Attribute: attribute}, t.Owner, func(emit func(string) error) error {
			return member.EmitInheritedGenerations(ctx, s.OriginalCatalog, emit)
		}); err != nil {
			return err
		}
	}
	return nil
}

func emitDumpInheritanceChecks(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	t := s.table(event.OID)
	for _, check := range table.OrderedChecks(t.Descriptor) {
		if !t.DeferredChecks[check.Name] || !table.DumpCheckDeclaration(s.OriginalCatalog, t.Descriptor, check) {
			continue
		}
		member := t
		d := *t.Descriptor
		d.Constraints.Check = []catalog.CheckConstraint{check}
		member.Descriptor = &d
		owner := t.Owner
		if t.RecipePlaceholders {
			owner = "postgres"
		}
		if err := s.emitCastConsumer(w, castConsumer{Event: event, Check: check.Name}, owner, func(emit func(string) error) error {
			only := !t.RecipePlaceholders && catalog.InheritanceParticipant(s.OriginalCatalog, t.Descriptor) && (check.NoInherit || !catalog.InheritanceHasChildren(s.OriginalCatalog, t.Descriptor.Address()))
			return member.EmitInheritedChecks(ctx, s.OriginalCatalog, only, emit)
		}); err != nil {
			return err
		}
	}
	return nil
}

func emitDumpInheritedForeignKeys(_ context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	t := s.table(event.OID)
	return t.EmitInheritedForeignKeys(s.OriginalCatalog, w.emit)
}

func emitDumpRetiredConstraints(_ context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	t := s.table(event.OID)
	if err := t.EmitRetiredConstraints(s.OriginalCatalog, false, w.emit, w.setOwner); err != nil {
		return err
	}
	return t.EmitRetiredConstraints(s.OriginalCatalog, true, w.emit, w.setOwner)
}

func emitDumpDomainChecks(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	domain := s.domainNodes[event.OID]
	for _, check := range domain.DomainChecks {
		if !check.NotValid && !s.deferredDomainChecks[event.OID][check.Name] {
			continue
		}
		text, err := typedef.DomainCheckDumpStatement(ctx, Presenter{}, s.CatalogView, domain, check)
		if err != nil {
			return err
		}
		// A domain owner need not have schema USAGE. Retain the existing
		// bootstrap replay authority, without transferring ownership.
		if err := s.emitCastConsumer(w, castConsumer{Event: event, Check: check.Name}, "postgres", func(emit func(string) error) error {
			return emit(text)
		}); err != nil {
			return err
		}
	}
	return nil
}

func emitDumpOperatorLinks(_ context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	return w.emit(s.operatorLinks[event.OID])
}
