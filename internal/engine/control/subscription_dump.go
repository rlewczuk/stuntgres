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

type subscriptionReplay struct{ owner, sql, finalSQL string }

func collectSubscriptionDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, descriptor catalog.Descriptor) error {
	d := descriptor.(*catalog.Subscription)
	if d.Database != c.CatalogView.Owner().DatabaseAddress() {
		return nil
	}
	if err := (catalog.SubscriptionProvider{}).Validate(c.CatalogView, d); err != nil {
		return err
	}
	if err := access.ValidateACL(c.OriginalRoles, d); err != nil {
		return err
	}
	c.Subscriptions = append(c.Subscriptions, d)
	return nil
}
func (s *dumpScript) prepareSubscriptions(ctx context.Context) error {
	quote := func(text string) string { return "'" + strings.ReplaceAll(text, "'", "''") + "'" }
	if len(s.Subscriptions) == 0 {
		return nil
	}
	bootstrap, err := s.OriginalRoles.Descriptor(s.OriginalCatalog.Owner().Principals.Principal(10))
	if err != nil {
		return err
	}
	if !bootstrap.Superuser {
		return fmt.Errorf("stuntgres: dump subscription bootstrap authority is unavailable")
	}
	// Reserve final and implicit-slot creation names before allocating staging
	// names. Several definitions may share an origin after ordinary renames.
	reserved := make(map[string]string, 3*len(s.Subscriptions))
	for _, d := range s.Subscriptions {
		if reserved[d.Name()] == "" {
			reserved[d.Name()] = catalog.QuoteIdentifier(d.Name())
		}
		if d.HasSlot && reserved[d.SlotName] == "" {
			reserved[d.SlotName] = catalog.QuoteIdentifier(d.SlotName)
		}
	}
	slices.SortFunc(s.Subscriptions, func(a, b *catalog.Subscription) int {
		return strings.Compare(reserved[a.Name()], reserved[b.Name()])
	})
	serial := 0
	for _, d := range s.Subscriptions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := (catalog.SubscriptionProvider{}).Validate(s.OriginalCatalog, d); err != nil {
			return err
		}
		owner, err := s.OriginalRoles.Descriptor(d.Owner)
		if err != nil {
			return err
		}
		name := reserved[d.Name()]
		var staging string
		for {
			serial++
			staging = fmt.Sprintf("_stuntgres_subscription_%d", serial)
			if reserved[staging] == "" {
				break
			}
		}
		stagedName := catalog.QuoteIdentifier(staging)
		reserved[staging] = stagedName
		creationName := stagedName
		if d.HasSlot {
			creationName = reserved[d.SlotName]
		}
		var b strings.Builder
		b.WriteString("CREATE SUBSCRIPTION " + creationName + " CONNECTION " + quote(d.Connection) + " PUBLICATION ")
		for i, pub := range d.Publications() {
			if i != 0 {
				b.WriteString(", ")
			}
			b.WriteString(catalog.QuoteIdentifier(pub))
		}
		// Reconstruct non-NULL slots implicitly: explicit syntax cannot preserve
		// uppercase, UTF8 or the literal "none" slot.
		b.WriteString(" WITH (connect = false")
		if !d.HasSlot {
			b.WriteString(", slot_name = NONE")
		}
		stream := "parallel"
		if d.Streaming == 'f' {
			stream = "false"
		} else if d.Streaming == 't' {
			stream = "true"
		}
		fmt.Fprintf(&b, ", binary = %t, streaming = %s, two_phase = %t, disable_on_error = %t, password_required = %t, run_as_owner = %t, failover = %t, synchronous_commit = %s, origin = %s);\n", d.Binary, stream, d.TwoPhase == 'p', d.DisableOnError, d.PasswordRequired, d.RunAsOwner, d.Failover, quote(d.SynchronousCommit), quote(d.Origin))
		if creationName != stagedName {
			b.WriteString("ALTER SUBSCRIPTION " + creationName + " RENAME TO " + stagedName + ";\n")
		}
		r := subscriptionReplay{owner: bootstrap.Name, sql: b.String()}
		b.Reset()
		b.WriteString("ALTER SUBSCRIPTION " + stagedName + " RENAME TO " + name + ";\n")
		if d.SkipLSN != 0 {
			fmt.Fprintf(&b, "ALTER SUBSCRIPTION %s SKIP (lsn = '%X/%X');\n", name, d.SkipLSN>>32, uint32(d.SkipLSN))
		}
		if d.Enabled {
			b.WriteString("ALTER SUBSCRIPTION " + name + " ENABLE;\n")
		}
		b.WriteString("ALTER SUBSCRIPTION " + name + " OWNER TO " + catalog.QuoteIdentifier(owner.Name) + ";\n")
		r.finalSQL = b.String()
		s.subscriptionReplay = append(s.subscriptionReplay, r)
	}
	return nil
}
func emitDumpSubscriptions(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	for _, r := range s.subscriptionReplay {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.setOwner(r.owner); err != nil {
			return err
		}
		if err := w.emit(r.sql); err != nil {
			return err
		}
	}
	// Free every creation name before installing finals, including name cycles.
	for _, r := range s.subscriptionReplay {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.setOwner(r.owner); err != nil {
			return err
		}
		if err := w.emit(r.finalSQL); err != nil {
			return err
		}
	}
	return nil
}
