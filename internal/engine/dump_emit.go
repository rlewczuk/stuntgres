package engine

import (
	"context"
	"io"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
)

// dumpWriter writes one Dump script and tracks the session authority the
// replayed statements run under. It lives for one Dump call.
type dumpWriter struct {
	ctx          context.Context
	dst          io.Writer
	currentOwner string
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
}

// emitDumpEvents writes every ordered event through its phase's entry.
func emitDumpEvents(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	for _, event := range s.eventOrder {
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
	}
	return nil
}

// table returns a copy of the captured table record for oid; emitters may
// adjust the copy without changing the capture.
func (s *dumpScript) table(oid ids.OID) dumpRelation {
	return s.tables[s.tableIndices[oid]]
}

// dumpBootstrapOwner replays post-data phases as the existing bootstrap
// authority.
func dumpBootstrapOwner(*dumpScript, dump.Event) string { return "postgres" }

func dumpTableOwner(s *dumpScript, event dump.Event) string { return s.table(event.OID).Owner }

// dumpInvalidConstraintOwner validates inheritance participants' NOT VALID
// constraints as bootstrap and others as the table owner.
func dumpInvalidConstraintOwner(s *dumpScript, event dump.Event) string {
	t := s.table(event.OID)
	if catalog.InheritanceParticipant(s.originalCatalog, t.Descriptor) {
		return "postgres"
	}
	return t.Owner
}

func dumpEventRelation(view catalog.Read, event dump.Event) *catalog.Relation {
	return view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: event.OID})
}

func emitDumpRename(_ context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	return w.emit(s.constraintReplay.finalRenameSQL(dumpEventRelation(s.originalCatalog, event)))
}

func emitDumpIndexAttachment(_ context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	return w.emit(partitionIndexDumpAttachmentSQL(s.originalCatalog, dumpEventRelation(s.originalCatalog, event)))
}

func emitDumpTableAttachment(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	descriptor := dumpEventRelation(s.originalCatalog, event)
	text, err := partitionAttachDumpSQL(ctx, s.originalCatalog, descriptor)
	if err != nil {
		return err
	}
	text += s.constraintReplay.membershipRenames(s.catalogView, descriptor, event)
	return w.emit(text)
}

func emitDumpInvalidIndex(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	text, err := s.invalidReplay[event.OID].sql(ctx, s.originalCatalog, event.Phase)
	if err != nil {
		return err
	}
	return w.emit(text)
}

func emitDumpDefaults(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	t := s.table(event.OID)
	return t.EmitDeferredDefaults(ctx, s.originalCatalog, w.emit)
}

func emitDumpRows(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	t := s.table(event.OID)
	return t.EmitRows(ctx, s.originalCatalog, w.emit)
}

func emitDumpInvalidConstraints(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	t := s.table(event.OID)
	return t.EmitInvalidConstraints(ctx, s.originalCatalog, w.emit)
}

func emitDumpInheritanceRecipes(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	t := s.table(event.OID)
	return t.EmitInheritedGenerations(ctx, s.originalCatalog, w.emit)
}

func emitDumpInheritanceChecks(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	t := s.table(event.OID)
	return t.EmitInheritedChecks(ctx, s.originalCatalog, w.emit)
}

func emitDumpInheritedForeignKeys(_ context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	t := s.table(event.OID)
	return t.EmitInheritedForeignKeys(s.originalCatalog, w.emit)
}

func emitDumpRetiredConstraints(_ context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	t := s.table(event.OID)
	if err := t.EmitRetiredConstraints(s.originalCatalog, false, w.emit, w.setOwner); err != nil {
		return err
	}
	return t.EmitRetiredConstraints(s.originalCatalog, true, w.emit, w.setOwner)
}

func emitDumpDomainChecks(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	text, err := typedef.DomainInvalidDumpStatement(ctx, dumpPresenter{}, s.catalogView, s.domainNodes[event.OID])
	if err != nil {
		return err
	}
	// A domain may legally be owned by a role without USAGE on its
	// schema. Replay only this post-data declaration as the existing
	// bootstrap authority; do not transfer ownership or invent ACLs.
	if err := w.setOwner("postgres"); err != nil {
		return err
	}
	return w.emit(text)
}

func emitDumpOperatorLinks(_ context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	return w.emit(s.operatorLinks[event.OID])
}
