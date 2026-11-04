package routine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
)

func stageSepgsqlFunction(h DDLHost, auth ddl.Authority, s *parser.CreateFunction, d *catalog.Procedure, layout *proc.Definition, previous *catalog.Procedure, language *catalog.Language) error {
	signature, result, _, known := catalog.SepgsqlDeclaration(s.Symbol)
	if !known || s.Body != "$libdir/sepgsql" || !s.SymbolSpecified || language.Address().ObjectID != 13 || s.Procedure || d.Signature != signature || d.Result != result || layout.ReturnsSet || layout.TableResult || len(layout.OutputFormals) != 0 || d.Transforms != nil {
		return sqlError("0A000", "unsupported sepgsql C declaration")
	}
	for _, formal := range layout.Formals {
		if formal.Mode != "i" || formal.HasDefault {
			return sqlError("0A000", "unsupported sepgsql C declaration")
		}
	}
	if previous == nil {
		oid, err := h.AllocateOID()
		if err != nil {
			return err
		}
		d.ObjectAddress.ObjectID = oid
		d.ACL = h.CreationACL(d.Owner, d.Namespace(), authorization.DefaultRoutines)
	}
	capability, err := catalog.NewSepgsqlRoutine(h.AccessView(auth).Catalog, h.AccessView(auth).Owner.Sepgsql, s.Symbol, d.Address(), layout.Formals)
	if err != nil {
		return err
	}
	d.Sepgsql = capability
	d.Display.Language, d.Display.Source, d.Display.Binary, d.Display.HasBinary = catalog.CRoutineLanguage, s.Symbol, s.Body, true
	d.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	if previous == nil {
		err = h.Stage().Create(d)
	} else {
		source := h.StageRead()
		if err = h.Stage().Replace(d); err == nil {
			err = h.RefreshStoredRoutineConsumers(source, previous, d)
		}
	}
	if err != nil {
		return err
	}
	ddl.RecordCommand(h.Stage(), "CREATE FUNCTION", d.Address())
	return nil
}

// FmgrBuiltin authenticates the original generated fmgrtab declaration. Current
// names and LANGUAGE presentation confer no fast-path authority. Released SQL
// wrappers and newly installed INTERNAL aliases are deliberately not builtin.
func FmgrBuiltin(p *catalog.Procedure) bool {
	if p == nil {
		return false
	}
	if p.Builtin == nil {
		switch {
		case p.AccessMethod != catalog.NoAccessMethod:
			return catalog.OriginalNativeAccessMethodProcedure(p)
		case p.BrinSupport != 0:
			return catalog.OriginalBrinSupport(p)
		case p.GinSupport != 0:
			return catalog.OriginalGinSupport(p)
		default:
			return false
		}
	}
	declaration := p.Builtin
	if BuiltinRoutine(declaration.Identity.ID) != declaration || declaration.Kind == catalog.AggregateRoutine {
		return false
	}
	display, ok := builtinRoutineDisplay[declaration.Identity.ID]
	return ok && display.Language == catalog.InternalRoutineLanguage
}
