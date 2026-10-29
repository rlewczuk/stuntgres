package publication

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type Host interface {
	ddl.Host
	RemoveObject(ids.ObjectRef, bool) error
	AddDomainBase(catalog.Descriptor)
	ResolveRelation(ddl.Authority, parser.ObjectName) (ids.ObjectRef, error)
	PublicationSchema(ddl.Authority, parser.PublicationMember) (*catalog.Namespace, error)
	BindPublicationFilter(ddl.Authority, *catalog.Relation, parser.Expr, sqltype.InputEnvironment) (catalog.PolicyPayload, []ids.ObjectAddress, error)
	PublicationWALLevel() string
}

func Stage(h Host, auth ddl.Authority, s *parser.Publication, input sqltype.InputEnvironment) error {
	v := h.AccessView(auth)
	actor, err := v.Roles.Descriptor(v.State.CurrentUser)
	if err != nil {
		return err
	}
	var successor authorization.PrincipalRef
	key := func(name string) ids.NameKey { return ids.NameKey{ClassID: ids.PublicationClassID, Name: name} }
	if s.Action == "drop" {
		var targets []*catalog.Publication
		for _, name := range s.Names {
			d, _ := h.StageRead().Get(h.StageRead().NameAddress(key(name.Text))).(*catalog.Publication)
			if d == nil {
				if !s.IfExists {
					return failure("42704", fmt.Sprintf(`publication "%s" does not exist`, name.Text))
				}
				h.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`publication "%s" does not exist, skipping`, name.Text)})
				continue
			}
			if err = v.Check(auth, access.Requirement{Object: v.Ref(d.Address()), OwnerOnly: true, OwnerNoun: "publication"}); err != nil {
				return err
			}
			targets = append(targets, d)
		}
		for _, d := range targets {
			if err = dropMappings(h, d.Address()); err != nil {
				return err
			}
			h.DropTarget(d.Address())
		}
		return h.RecordDependencyPath(auth)
	}
	d, _ := h.StageRead().Get(h.StageRead().NameAddress(key(s.Name.Text))).(*catalog.Publication)
	var next catalog.Publication
	if s.Action == "create" {
		if err = access.PublicationDatabaseCreate(v, auth); err != nil {
			return err
		}
		if s.AllTables && !actor.Superuser {
			return failure("42501", "must be superuser to create FOR ALL TABLES publication")
		}
		if d != nil {
			return failure("42710", fmt.Sprintf(`publication "%s" already exists`, s.Name.Text))
		}
		next = catalog.Publication{ObjectName: s.Name.Text, Owner: v.State.CurrentUser, AllTables: s.AllTables, Insert: true, Update: true, Delete: true, Truncate: true, GeneratedColumns: 'n'}
		if err = applyOptions(&next, s.Options); err != nil {
			return err
		}
		oid, e := h.AllocateOID()
		if e != nil {
			return e
		}
		next.ObjectAddress = ids.ObjectAddress{ClassID: ids.PublicationClassID, ObjectID: oid}
	} else {
		if d == nil {
			return failure("42704", fmt.Sprintf(`publication "%s" does not exist`, s.Name.Text))
		}
		if s.Action == "owner" {
			successor, err = access.SchemaOwner(v, s.Target.Text, s.OwnerIdentity)
			if err != nil {
				return err
			}
			if successor == d.Owner {
				return nil
			}
			if err = access.PublicationOwnershipEligibility(v, auth, d, successor); err != nil {
				return err
			}
		} else if err = v.Check(auth, access.Requirement{Object: v.Ref(d.Address()), OwnerOnly: true, OwnerNoun: "publication"}); err != nil {
			return err
		}
		next = *d
		switch s.Action {
		case "options":
			if err = applyOptions(&next, s.Options); err != nil {
				return err
			}
		case "rename":
			if err = access.PublicationDatabaseCreate(v, auth); err != nil {
				return err
			}
			if h.StageRead().NameAddress(key(s.Target.Text)) != (ids.ObjectAddress{}) {
				return failure("42710", fmt.Sprintf(`publication "%s" already exists`, s.Target.Text))
			}
			next.ObjectName = s.Target.Text
		case "owner":
			next.Owner = successor
		case "add", "set", "remove":
			if d.AllTables {
				e := failure("55000", fmt.Sprintf(`publication "%s" is defined as FOR ALL TABLES`, d.Name()))
				e.Detail = "Tables cannot be added to or dropped from FOR ALL TABLES publications."
				for _, m := range s.Members {
					if m.Schema.Text != "" || m.CurrentSchema {
						e.Detail = "Schemas cannot be added to or dropped from FOR ALL TABLES publications."
						break
					}
				}
				return e
			}
		default:
			return fmt.Errorf("stuntgres: invalid publication action %q", s.Action)
		}
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	if s.Action == "create" {
		err = h.Stage().Create(&next)
	} else {
		err = h.Stage().Replace(&next)
	}
	if err != nil {
		return err
	}
	if s.Action == "set" {
		if err = dropMappings(h, next.Address()); err != nil {
			return err
		}
	}
	if s.Action == "create" || s.Action == "add" || s.Action == "set" || s.Action == "remove" {
		if err = stageMembers(h, auth, &next, s, input, actor.Superuser); err != nil {
			return err
		}
	}
	if err = validateCandidate(h.StageRead(), &next, s); err != nil {
		return err
	}
	if s.Action == "create" && h.PublicationWALLevel() != "logical" {
		h.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "55000", Message: `"wal_level" is insufficient to publish logical changes`, Hint: `Set "wal_level" to "logical" before creating subscriptions.`})
	}
	return nil
}

func dropMappings(h Host, p ids.ObjectAddress) error {
	v := h.StageRead()
	for a, d := range v.PublicationRelations() {
		if d.Publication == p {
			if err := h.RemoveObject(v.Domain().Ref(v.Epoch(), a), false); err != nil {
				return err
			}
		}
	}
	for a, d := range v.PublicationNamespaces() {
		if d.Publication == p {
			if err := h.RemoveObject(v.Domain().Ref(v.Epoch(), a), false); err != nil {
				return err
			}
		}
	}
	return nil
}

func stageMembers(h Host, auth ddl.Authority, p *catalog.Publication, s *parser.Publication, input sqltype.InputEnvironment, superuser bool) error {
	requested := make(map[ids.ObjectAddress]*catalog.PublicationRelation)
	schemas := make(map[ids.ObjectAddress]bool)
	for _, m := range s.Members {
		if m.Schema.Text != "" || m.CurrentSchema {
			if s.Action != "remove" && !superuser {
				message := "must be superuser to add or set schemas"
				if s.Action == "create" {
					message = "must be superuser to create FOR TABLES IN SCHEMA publication"
				}
				return failure("42501", message)
			}
			ns, err := h.PublicationSchema(auth, m)
			if err != nil {
				return err
			}
			h.AddDomainBase(ns)
			if schemas[ns.Address()] {
				continue
			}
			schemas[ns.Address()] = true
			var existing *catalog.PublicationNamespace
			for _, n := range h.StageRead().PublicationNamespaces() {
				if n.Publication == p.Address() && n.Schema == ns.Address() {
					existing = n
					break
				}
			}
			if s.Action == "remove" {
				if existing == nil {
					return failure("42704", fmt.Sprintf(`tables from schema "%s" are not part of the publication`, ns.Name()))
				}
				if err = h.RemoveObject(h.StageRead().Domain().Ref(h.StageRead().Epoch(), existing.Address()), false); err != nil {
					return err
				}
				continue
			}
			if existing != nil {
				return failure("42710", fmt.Sprintf(`schema "%s" is already member of publication "%s"`, ns.Name(), p.Name()))
			}
			if ns.Temporary != (ids.SessionToken{}) {
				e := failure("22023", fmt.Sprintf(`cannot add schema "%s" to publication`, ns.Name()))
				e.Detail = "Temporary schemas cannot be replicated."
				return e
			}
			if ns.Address().ObjectID == ids.PGCatalogNamespaceOID || ns.Address().ObjectID == ids.PGToastNamespaceOID {
				e := failure("22023", fmt.Sprintf(`cannot add schema "%s" to publication`, ns.Name()))
				e.Detail = "This operation is not supported for system schemas."
				return e
			}
			oid, err := h.AllocateOID()
			if err != nil {
				return err
			}
			revision, err := h.Revision()
			if err != nil {
				return err
			}
			n := &catalog.PublicationNamespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.PublicationNamespaceClassID, ObjectID: oid}, ObjectRevision: revision, Publication: p.Address(), Schema: ns.Address()}
			if err = h.Stage().Create(n); err != nil {
				return err
			}
			continue
		}
		if s.Action == "remove" && m.Columns != nil {
			return failure("42601", "column list must not be specified in ALTER PUBLICATION ... DROP")
		}
		if s.Action == "remove" && m.Filter != nil {
			return failure("42601", "cannot use a WHERE clause when removing a table from a publication")
		}
		ref, err := h.ResolveRelation(auth, m.Table)
		if err != nil {
			return err
		}
		relation, _ := h.StageRead().Relation(ref.Address)
		relations := []*catalog.Relation{relation}
		if !m.Only && relation.Partition == nil {
			for i := 0; i < len(relations); i++ {
				for _, child := range catalog.InheritanceChildren(h.StageRead(), relations[i].Address()) {
					if !slices.Contains(relations, child) {
						relations = append(relations, child)
					}
				}
			}
		}
		for _, r := range relations {
			h.AddDomainBase(r)
			prior, seen := requested[r.Address()]
			if seen {
				if m.Filter != nil && prior.Filter != nil {
					return failure("42710", fmt.Sprintf(`conflicting or redundant WHERE clauses for table "%s"`, r.Name()))
				}
				if m.Columns != nil && prior.ColumnsSpecified {
					return failure("42710", fmt.Sprintf(`conflicting or redundant column lists for table "%s"`, r.Name()))
				}
				if m.Filter == nil && m.Columns == nil {
					continue
				}
			}
			var existing *catalog.PublicationRelation
			for _, edge := range h.StageRead().PublicationRelations() {
				if edge.Publication == p.Address() && edge.Relation == r.Address() {
					existing = edge
					break
				}
			}
			if s.Action == "remove" {
				if existing == nil {
					return failure("42704", fmt.Sprintf(`relation "%s" is not part of the publication`, r.Name()))
				}
				if err = h.RemoveObject(h.StageRead().Domain().Ref(h.StageRead().Epoch(), existing.Address()), false); err != nil {
					return err
				}
				requested[r.Address()] = &catalog.PublicationRelation{}
				continue
			}
			if existing != nil && !seen {
				return failure("42710", fmt.Sprintf(`relation "%s" is already member of publication "%s"`, r.Name(), p.Name()))
			}
			if err = checkRelation(h.StageRead(), r); err != nil {
				return err
			}
			v := h.AccessView(auth)
			if err = v.Check(auth, access.Requirement{Object: v.Ref(r.Address()), OwnerOnly: true, OwnerNoun: "table"}); err != nil {
				return err
			}
			edge := &catalog.PublicationRelation{Publication: p.Address(), Relation: r.Address(), ColumnsSpecified: m.Columns != nil}
			if seen {
				copy := *prior
				edge = &copy
			}
			if m.Columns != nil {
				selected := make(map[int32]bool, len(m.Columns))
				for _, column := range m.Columns {
					slot, ok := r.Index[column.Text]
					if !ok {
						if catalog.IsSystemColumn(column.Text) {
							return failure("42P10", fmt.Sprintf(`cannot use system column "%s" in publication column list`, column.Text))
						}
						return failure("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, column.Text, r.Name()))
					}
					sub := r.Attributes[slot]
					if selected[sub] {
						return failure("42710", fmt.Sprintf(`duplicate column "%s" in publication column list`, column.Text))
					}
					if generation, ok := r.ColumnMeta.Generations[sub]; ok && generation.Kind == catalog.GenerationVirtual {
						return failure("42P10", fmt.Sprintf(`cannot use virtual generated column "%s" in publication column list`, column.Text))
					}
					selected[sub] = true
				}
				edge.ColumnsSpecified = true
				edge.Columns = nil
				for _, sub := range r.Attributes {
					if selected[sub] {
						edge.Columns = append(edge.Columns, sub)
					}
				}
			}
			if m.Filter != nil {
				payload, refs, e := h.BindPublicationFilter(auth, r, m.Filter, input)
				if e != nil {
					return e
				}
				edge.Filter = payload
				edge.SetFilterDependencies(refs)
			}
			if seen {
				h.Stage().UpdateDescriptor(edge)
			} else {
				oid, e := h.AllocateOID()
				if e != nil {
					return e
				}
				edge.ObjectAddress = ids.ObjectAddress{ClassID: ids.PublicationRelationClassID, ObjectID: oid}
				edge.ObjectRevision, e = h.Revision()
				if e != nil {
					return e
				}
				if e = h.Stage().Create(edge); e != nil {
					return e
				}
			}
			requested[r.Address()] = edge
		}
	}
	return nil
}

func validateCandidate(v catalog.Read, p *catalog.Publication, s *parser.Publication) error {
	action := s.Action
	addingSchema := false
	if action == "add" {
		for _, member := range s.Members {
			addingSchema = addingSchema || member.Schema.Text != "" || member.CurrentSchema
		}
	}
	hasSchemas := false
	for _, n := range v.PublicationNamespaces() {
		hasSchemas = hasSchemas || n.Publication == p.Address()
	}
	for _, m := range v.PublicationRelations() {
		if m.Publication != p.Address() {
			continue
		}
		r, _ := v.Relation(m.Relation)
		if hasSchemas && m.ColumnsSpecified {
			if addingSchema {
				e := failure("22023", fmt.Sprintf(`cannot add schema to publication "%s"`, p.Name()))
				e.Detail = "Schemas cannot be added if any tables that specify a column list are already part of the publication."
				return e
			}
			ns := v.MustNamespace(r.Namespace())
			e := failure("22023", fmt.Sprintf(`cannot use column list for relation "%s.%s" in publication "%s"`, ns.Name(), r.Name(), p.Name()))
			e.Detail = "Column lists cannot be specified in publications containing FOR TABLES IN SCHEMA elements."
			return e
		}
		if r.Partition == nil || p.ViaRoot || m.Filter == nil && !m.ColumnsSpecified {
			continue
		}
		if action == "options" {
			e := failure("22023", fmt.Sprintf(`cannot set parameter "publish_via_partition_root" to false for publication "%s"`, p.Name()))
			kind := "a WHERE clause"
			if m.Filter == nil {
				kind = "a column list"
			}
			e.Detail = fmt.Sprintf(`The publication contains %s for partitioned table "%s", which is not allowed when "publish_via_partition_root" is false.`, kind, r.Name())
			return e
		}
		if m.Filter != nil {
			e := failure("22023", fmt.Sprintf(`cannot use publication WHERE clause for relation "%s"`, r.Name()))
			e.Detail = "WHERE clause cannot be used for a partitioned table when publish_via_partition_root is false."
			return e
		}
		ns := v.MustNamespace(r.Namespace())
		e := failure("22023", fmt.Sprintf(`cannot use column list for relation "%s.%s" in publication "%s"`, ns.Name(), r.Name(), p.Name()))
		e.Detail = "Column lists cannot be specified for partitioned tables when publish_via_partition_root is false."
		return e
	}
	return nil
}
