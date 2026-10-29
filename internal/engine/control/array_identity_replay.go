package control

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// These IDs are rendering keys only. They never enter SQL, catalog descriptors,
// installation recipes or owner records. All changes below have real SQL owners.
type installationTypeReplay struct {
	names         *typedef.DomainReplayNames
	source        catalog.Read
	ctx           context.Context
	nextType      sqltype.ID
	nextNamespace ids.OID
	serial        uint64
	reservedNames map[string]bool
	constructors  map[sqltype.ID][]*catalog.Procedure
}

func newInstallationTypeReplay(source catalog.Read, ctx context.Context) *installationTypeReplay {
	p := &installationTypeReplay{names: typedef.NewDomainReplayNames(), source: source, ctx: ctx, nextType: ^sqltype.ID(0), nextNamespace: ^ids.OID(0), reservedNames: map[string]bool{}, constructors: map[sqltype.ID][]*catalog.Procedure{}}
	for _, d := range source.Descriptors() {
		switch d := d.(type) {
		case *catalog.Type:
			p.reservedNames[d.Name()] = true
		case *catalog.Procedure:
			if d.RangeConstructor != nil && d.RangeConstructor.Type != nil {
				if family := d.RangeConstructor.Type.RangeType(); family != nil {
					p.constructors[family.ID()] = append(p.constructors[family.ID()], d)
				}
			}
		}
	}
	return p
}
func (p *installationTypeReplay) temporaryID() (sqltype.ID, error) {
	for p.nextType != 0 {
		id := p.nextType
		p.nextType--
		if _, exists := p.names.Names[id]; exists {
			continue
		}
		if p.source.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}) == nil {
			return id, nil
		}
	}
	return 0, fmt.Errorf("stuntgres: exhausted rendering type identities")
}
func (p *installationTypeReplay) namespace() (ids.ObjectAddress, error) {
	for p.nextNamespace != 0 {
		a := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: p.nextNamespace}
		p.nextNamespace--
		if p.source.Get(a) == nil {
			return a, nil
		}
	}
	return ids.ObjectAddress{}, fmt.Errorf("stuntgres: exhausted rendering namespace identities")
}
func replayTypeSQL(schema, name string) string {
	return catalog.QuoteIdentifier(schema) + "." + catalog.QuoteIdentifier(name)
}

// replayInstalledType normalizes in an empty real working namespace, except
// retained pg_catalog identities: those normalize by same-namespace renames.
// No public movement back into the system schema is synthesized.
func (s *dumpScript) replayInstalledType(schedule *installationSchedule, item replayInstallation, current *catalog.Type, temporary string) error {
	p := s.installationTypeReplay
	model := p.names
	inPlace := item.schema == "pg_catalog" && current.Namespace().ObjectID == ids.PGCatalogNamespaceOID
	namespace := current.Namespace()
	schema := "pg_catalog"
	if !inPlace {
		var err error
		namespace, err = p.namespace()
		if err != nil {
			return err
		}
		schema = schedule.schema("__stuntgres_array_identity")
	}
	id := current.SQLType.ID()
	key := ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: temporary}
	if err := model.ReserveIdentity(id, key); err != nil {
		return err
	}
	if current.Array != 0 {
		model.AssociateIdentity(id, current.Array)
	}
	if !inPlace {
		s.installationStatements = append(s.installationStatements, "CREATE SCHEMA "+catalog.QuoteIdentifier(schema)+";\n", "ALTER TYPE "+replayTypeSQL(item.schema, temporary)+" SET SCHEMA "+catalog.QuoteIdentifier(schema)+";\n")
	}
	key.Name = current.Name()
	if err := model.Relocate(id, key, false); err != nil {
		return err
	}
	s.installationStatements = append(s.installationStatements, "ALTER TYPE "+replayTypeSQL(schema, temporary)+" RENAME TO "+catalog.QuoteIdentifier(key.Name)+";\n")
	if current.Array != 0 {
		array, ok := s.CatalogView.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(current.Array)})
		if !ok || array.Namespace() != current.Namespace() {
			return fmt.Errorf("stuntgres: invalid associated array replay identity")
		}
		if model.Names[current.Array].Name != array.Name() {
			if err := s.replayArrayName(schema, current, array.Name(), &s.installationStatements); err != nil {
				return err
			}
		}
	}
	if inPlace {
		return nil
	}
	destination := catalog.DescriptorNameKey(current)
	if err := model.Relocate(id, destination, true); err != nil {
		return err
	}
	s.installationStatements = append(s.installationStatements, "ALTER TYPE "+replayTypeSQL(schema, current.Name())+" SET SCHEMA "+catalog.QuoteIdentifier(s.CatalogView.MustNamespace(current.Namespace()).Name())+";\n", "DROP SCHEMA "+catalog.QuoteIdentifier(schema)+";\n")
	return nil
}

// parentDDLKind names the existing SQL identity owner. Table/view rowtypes are
// protected projections and must never be renamed through ALTER TYPE.
func (s *dumpScript) parentDDLKind(parent *catalog.Type) (string, error) {
	if parent.OwningRelation != (ids.ObjectAddress{}) {
		relation, ok := s.CatalogView.Relation(parent.OwningRelation)
		if !ok || relation.Name() != parent.Name() || relation.Namespace() != parent.Namespace() {
			return "", fmt.Errorf("stuntgres: invalid rowtype parent replay identity")
		}
		switch relation.RelationKind {
		case catalog.CompositeRelation:
			return "TYPE", nil
		case catalog.ViewRelation:
			return "VIEW", nil
		case catalog.MaterializedViewRelation:
			return "MATERIALIZED VIEW", nil
		case catalog.ForeignRelation:
			return "FOREIGN TABLE", nil
		default:
			return "TABLE", nil
		}
	}
	if parent.SQLType.IsDomain() {
		return "DOMAIN", nil
	}
	return "TYPE", nil
}

// ordinaryTypeArrayReplay is invoked immediately after the creator's actual
// array allocation is modeled. The epilogue is emitted at that SAME declaration
// boundary, never in the earlier installation prefix or as a suffix-only CREATE.
func (s *dumpScript) ordinaryTypeArrayReplay(oid ids.OID, parent *catalog.Type) error {
	if creation := s.rangeCreations[oid]; creation != nil && creation.root.SQLType.ID() == parent.SQLType.ID() {
		return s.completeRangeCreation(oid, creation)
	}
	sql, err := s.normalizeArrayInWork(parent, s.CatalogView.MustNamespace(parent.Namespace()).Name())
	if err != nil {
		return err
	}
	s.typeReplayEpilogues[oid] += sql
	return nil
}

func (s *dumpScript) normalizeArrayInWork(parent *catalog.Type, originalSchema string) (string, error) {
	if parent.Array == 0 {
		return "", nil
	}
	p := s.installationTypeReplay
	model := p.names
	id := parent.SQLType.ID()
	array, ok := s.CatalogView.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(parent.Array)})
	if !ok || array.Namespace() != parent.Namespace() {
		return "", fmt.Errorf("stuntgres: invalid ordinary associated array identity")
	}
	if model.Names[parent.Array].Name == array.Name() {
		return "", nil
	}
	namespace, err := p.namespace()
	if err != nil {
		return "", err
	}
	kind, err := s.parentDDLKind(parent)
	if err != nil {
		return "", err
	}
	schema := ""
	for {
		p.serial++
		schema = "__stuntgres_array_work_" + strconv.FormatUint(p.serial, 10)
		if s.CatalogView.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: schema}) == (ids.ObjectAddress{}) {
			break
		}
	}
	original := model.Names[id]
	working := original
	working.Namespace = namespace
	if err := model.Relocate(id, working, true); err != nil {
		return "", err
	}
	statements := []string{"CREATE SCHEMA " + catalog.QuoteIdentifier(schema) + ";\n", "ALTER " + kind + " " + replayTypeSQL(originalSchema, parent.Name()) + " SET SCHEMA " + catalog.QuoteIdentifier(schema) + ";\n"}
	if err := s.replayArrayName(schema, parent, array.Name(), &statements); err != nil {
		return "", err
	}
	if err := model.Relocate(id, original, true); err != nil {
		return "", err
	}
	statements = append(statements, "ALTER "+kind+" "+replayTypeSQL(schema, parent.Name())+" SET SCHEMA "+catalog.QuoteIdentifier(originalSchema)+";\n", "DROP SCHEMA "+catalog.QuoteIdentifier(schema)+";\n")
	return strings.Join(statements, ""), nil
}

type replayDomain struct {
	id   sqltype.ID
	name string
}

func (s *dumpScript) createReplayDomain(schema string, key ids.NameKey, statements *[]string) (replayDomain, error) {
	p := s.installationTypeReplay
	id, err := p.temporaryID()
	if err != nil {
		return replayDomain{}, err
	}
	array, err := p.temporaryID()
	if err != nil {
		return replayDomain{}, err
	}
	if err := p.names.ReserveIdentity(id, key); err != nil {
		return replayDomain{}, err
	}
	p.names.AssociateIdentity(id, array)
	*statements = append(*statements, "CREATE DOMAIN "+replayTypeSQL(schema, key.Name)+" AS integer;\n")
	return replayDomain{id: id, name: key.Name}, nil
}
func (s *dumpScript) retireReplayDomain(schema string, d replayDomain, statements *[]string) {
	*statements = append(*statements, "DROP DOMAIN "+replayTypeSQL(schema, d.name)+";\n")
	s.installationTypeReplay.names.RetireIdentity(d.id)
}

func (s *dumpScript) replayArrayName(schema string, parent *catalog.Type, target string, statements *[]string) error {
	trace, err := arrayNameTrace(parent.Name(), target)
	if err != nil {
		return err
	}
	p := s.installationTypeReplay
	model := p.names
	id, array := parent.SQLType.ID(), parent.Array
	if err := p.ctx.Err(); err != nil {
		return err
	}
	kind, err := s.parentDDLKind(parent)
	if err != nil {
		return err
	}
	base := parent.Name()
	association := true
	for _, suffix := range trace {
		next := typedef.AssociatedArrayNameCandidate(base, suffix)
		if next == base {
			continue
		}
		if model.Names[array].Name != next {
			if !association && model.Names[array].Name != base {
				return fmt.Errorf("stuntgres: divergent associated array trace")
			}
			key := model.Names[id]
			key.Name = next
			if model.Occupied(key) {
				return fmt.Errorf("stuntgres: occupied associated array trace target")
			}
			// Protect the requested target while earlier candidate blockers allocate
			// their own arrays. A blocker must never steal this newly-free target.
			protection, err := s.createReplayDomain(schema, key, statements)
			if err != nil {
				return err
			}
			protectionArray := model.Arrays[protection.id]
			var blockers []replayDomain
			for earlier := uint64(0); earlier < suffix; earlier++ {
				if err := p.ctx.Err(); err != nil {
					return err
				}
				key.Name = typedef.AssociatedArrayNameCandidate(base, earlier)
				if key == model.Names[id] {
					continue
				}
				occupied := model.Occupied(key)
				replace := key == model.Names[protectionArray] || association && key == model.Names[array]
				if occupied && !replace {
					continue
				}
				blocker, err := s.createReplayDomain(schema, key, statements)
				if err != nil {
					return err
				}
				blockers = append(blockers, blocker)
			}
			s.retireReplayDomain(schema, protection, statements)
			if association {
				// Rename out and back through the genuine owner. The first naming edge
				// associates with the element name, unlike subsequent displacement edges.
				temporary := ""
				for {
					p.serial++
					temporary = "__stuntgres_array_parent_" + strconv.FormatUint(p.serial, 10)
					key = model.Names[id]
					key.Name = temporary
					if model.Occupied(key) {
						continue
					}
					generated := typedef.ChooseAssociatedArrayName(temporary, func(name string) bool { k := key; k.Name = name; return model.Occupied(k) && k != model.Names[array] })
					if generated == next {
						continue
					}
					break
				}
				old := model.Names[id]
				if err := model.Relocate(id, key, false); err != nil {
					return err
				}
				*statements = append(*statements, "ALTER "+kind+" "+replayTypeSQL(schema, old.Name)+" RENAME TO "+catalog.QuoteIdentifier(temporary)+";\n")
				if err := model.Relocate(id, old, false); err != nil {
					return err
				}
				*statements = append(*statements, "ALTER "+kind+" "+replayTypeSQL(schema, temporary)+" RENAME TO "+catalog.QuoteIdentifier(old.Name)+";\n")
			} else {
				key = model.Names[array]
				trigger, err := s.createReplayDomain(schema, key, statements)
				if err != nil {
					return err
				}
				s.retireReplayDomain(schema, trigger, statements)
			}
			if model.Names[array].Name != next {
				return fmt.Errorf("stuntgres: associated array SQL prediction diverged")
			}
			for i := len(blockers) - 1; i >= 0; i-- {
				s.retireReplayDomain(schema, blockers[i], statements)
			}
		}
		association = false
		base = next
	}
	if model.Names[array].Name != target {
		return fmt.Errorf("stuntgres: incomplete associated array naming trace")
	}
	return nil
}
