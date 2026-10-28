package control

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

type eventTriggerReplay struct {
	owner, name, create string
	mode                catalog.TriggerMode
}

// Render and validate against the captured authorities before any output byte.
func (s *dumpScript) prepareEventTriggers(ctx context.Context) error {
	read := s.OriginalCatalog
	for _, d := range s.EventTriggers {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := (catalog.EventTriggerProvider{}).Validate(read, d); err != nil {
			return err
		}
		if err := access.ValidateACL(s.OriginalRoles, d); err != nil {
			return err
		}
		owner, err := s.OriginalRoles.Descriptor(d.Owner)
		if err != nil {
			return err
		}
		if !owner.Superuser {
			return fmt.Errorf("stuntgres: dump cannot serialize event trigger %q owned by non-superuser %q", d.Name(), owner.Name)
		}
		function, ok := read.Get(d.Function.Address).(*catalog.Procedure)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		namespace := read.MustNamespace(function.Namespace())
		if namespace.Temporary != (ids.SessionToken{}) {
			return fmt.Errorf("stuntgres: dump cannot serialize an event trigger referencing a temporary function")
		}
		name := catalog.QuoteIdentifier(d.Name())
		var sql strings.Builder
		sql.WriteString("CREATE EVENT TRIGGER " + name + " ON " + d.Event)
		if d.Tags != nil {
			sql.WriteString(" WHEN TAG IN (")
			for i, tag := range d.Tags {
				if i != 0 {
					sql.WriteString(", ")
				}
				sql.WriteString("'" + strings.ReplaceAll(tag, "'", "''") + "'")
			}
			sql.WriteString(")")
		}
		sql.WriteString(" EXECUTE FUNCTION " + catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(function.Name()) + "();\n")
		s.eventTriggerReplay = append(s.eventTriggerReplay, eventTriggerReplay{owner: owner.Name, name: name, create: sql.String(), mode: d.FiringMode})
	}
	slices.SortFunc(s.eventTriggerReplay, func(a, b eventTriggerReplay) int { return strings.Compare(a.name, b.name) })
	return nil
}

// This terminal section follows even raw-codec CAST cleanup. No eligible DDL
// may follow it. CREATE/ALTER EVENT TRIGGER themselves are deliberately excluded
// from callbacks; all definitions are disabled until the declaration pass ends.
func emitDumpEventTriggers(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	for _, replay := range s.eventTriggerReplay {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.setOwner(replay.owner); err != nil {
			return err
		}
		if err := w.emit(replay.create + "ALTER EVENT TRIGGER " + replay.name + " DISABLE;\n"); err != nil {
			return err
		}
	}
	for _, replay := range s.eventTriggerReplay {
		if replay.mode == catalog.TriggerDisabled {
			continue
		}
		action := "ENABLE"
		switch replay.mode {
		case catalog.TriggerReplica:
			action += " REPLICA"
		case catalog.TriggerAlways:
			action += " ALWAYS"
		}
		if err := w.setOwner(replay.owner); err != nil {
			return err
		}
		if err := w.emit("ALTER EVENT TRIGGER " + replay.name + " " + action + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

func collectEventTriggerDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, descriptor catalog.Descriptor) error {
	d := descriptor.(*catalog.EventTrigger)
	if err := (catalog.EventTriggerProvider{}).Validate(c.CatalogView, d); err != nil {
		return err
	}
	if err := access.ValidateACL(c.OriginalRoles, d); err != nil {
		return err
	}
	c.EventTriggers = append(c.EventTriggers, d)
	return nil
}
