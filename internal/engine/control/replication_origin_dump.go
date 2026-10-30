package control

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

func collectReplicationOriginDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, descriptor catalog.Descriptor) error {
	d := descriptor.(*catalog.ReplicationOrigin)
	if err := (catalog.ReplicationOriginProvider{}).Validate(c.CatalogView, d); err != nil {
		return err
	}
	if subscription, ok := c.CatalogView.Get(d.Subscription).(*catalog.Subscription); ok && subscription.Database != c.CatalogView.Owner().DatabaseAddress() {
		return nil
	}
	c.ReplicationOrigins = append(c.ReplicationOrigins, d)
	return nil
}
func emitDumpReplicationOrigins(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	slices.SortFunc(s.ReplicationOrigins, func(a, b *catalog.ReplicationOrigin) int { return strings.Compare(a.Name(), b.Name()) })
	ownerSet := false
	emit := func(sql string) error {
		if !ownerSet {
			bootstrap, err := s.OriginalRoles.Descriptor(s.OriginalCatalog.Owner().Principals.Principal(10))
			if err != nil {
				return err
			}
			if !bootstrap.Superuser {
				return fmt.Errorf("stuntgres: dump replication origin bootstrap authority is unavailable")
			}
			if err := w.setOwner(bootstrap.Name); err != nil {
				return err
			}
			ownerSet = true
		}
		return w.emit(sql)
	}
	for _, d := range s.ReplicationOrigins {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, subscription := s.OriginalCatalog.Get(d.Subscription).(*catalog.Subscription); subscription {
			continue
		}
		if err := emit("SELECT pg_catalog.pg_replication_origin_create(" + dump.CommentLiteral(d.Name()) + ");\n"); err != nil {
			return err
		}
		if value, active := s.OriginProgress[d.Address().ObjectID]; active {
			if err := emit(fmt.Sprintf("SELECT pg_catalog.pg_replication_origin_advance(%s, '%X/%X'::pg_catalog.pg_lsn);\n", dump.CommentLiteral(d.Name()), value>>32, uint32(value))); err != nil {
				return err
			}
		}
	}
	// Subscription creation itself recreates its canonical origin. Repair only
	// missing registrations or active logical progress, keeping ordinary dumps
	// byte-stable and independent of both replay allocation passes.
	for _, subscription := range s.Subscriptions {
		origin, _ := s.OriginalCatalog.Get(s.OriginalCatalog.NameAddress(catalog.ReplicationOriginNameKey(subscription.ReplicationOriginName()))).(*catalog.ReplicationOrigin)
		suffix := " FROM pg_catalog.pg_subscription WHERE subname = " + dump.CommentLiteral(subscription.Name()) + ";\n"
		if origin == nil {
			if err := emit("SELECT pg_catalog.pg_replication_origin_drop('pg_' || oid::pg_catalog.text)" + suffix); err != nil {
				return err
			}
		} else if value, active := s.OriginProgress[origin.Address().ObjectID]; active {
			if err := emit(fmt.Sprintf("SELECT pg_catalog.pg_replication_origin_advance('pg_' || oid::pg_catalog.text, '%X/%X'::pg_catalog.pg_lsn)", value>>32, uint32(value)) + suffix); err != nil {
				return err
			}
		}
	}
	return nil
}
