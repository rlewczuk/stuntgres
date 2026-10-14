package engine

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
)

func (e *Engine) initializeCatalogSources() error {
	for _, spec := range []struct {
		oid     OID
		name    string
		columns []struct {
			name string
			typ  sqltype.ID
		}
	}{
		{pgAttributeOID, "pg_attribute", []struct {
			name string
			typ  sqltype.ID
		}{
			{"attrelid", sqltype.Oid}, {"attname", sqltype.Name}, {"atttypid", sqltype.Oid},
			{"attlen", sqltype.Int2}, {"attnum", sqltype.Int2}, {"atttypmod", sqltype.Int4}, {"attndims", sqltype.Int2},
			{"attbyval", sqltype.Bool}, {"attalign", sqltype.Char}, {"attstorage", sqltype.Char}, {"attcompression", sqltype.Char},
			{"attnotnull", sqltype.Bool}, {"atthasdef", sqltype.Bool}, {"atthasmissing", sqltype.Bool},
			{"attidentity", sqltype.Char}, {"attgenerated", sqltype.Char}, {"attisdropped", sqltype.Bool}, {"attislocal", sqltype.Bool},
			{"attinhcount", sqltype.Int2}, {"attcollation", sqltype.Oid}, {"attstattarget", sqltype.Int2},
			{"attacl", sqltype.ACLItemArray}, {"attoptions", sqltype.ID(1009)}, {"attfdwoptions", sqltype.ID(1009)}, {"attmissingval", sqltype.ID(2277)},
		}},
		{1213, "pg_tablespace", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"spcname", sqltype.Name}, {"spcowner", sqltype.Oid}, {"spcoptions", sqltype.ID(1009)}}},
		{1262, "pg_database", []struct {
			name string
			typ  sqltype.ID
		}{
			{"oid", sqltype.Oid}, {"datname", sqltype.Name}, {"datdba", sqltype.Oid},
			{"encoding", sqltype.Int4}, {"datlocprovider", sqltype.Char},
			{"datistemplate", sqltype.Bool}, {"datallowconn", sqltype.Bool}, {"dathasloginevt", sqltype.Bool},
			{"datconnlimit", sqltype.Int4}, {"datfrozenxid", sqltype.Xid}, {"datminmxid", sqltype.Xid},
			{"dattablespace", sqltype.Oid}, {"datcollate", sqltype.Text}, {"datctype", sqltype.Text},
			{"datlocale", sqltype.Text}, {"daticurules", sqltype.Text}, {"datcollversion", sqltype.Text},
			{"datacl", sqltype.ID(1034)},
		}},
		{1255, "pg_proc", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"proname", sqltype.Name}, {"pronamespace", sqltype.Oid}, {"proowner", sqltype.Oid}, {"pronargs", sqltype.Int2}, {"prorettype", sqltype.Oid}, {"provolatile", sqltype.Char}, {"proisstrict", sqltype.Bool}, {"proparallel", sqltype.Char}}},
		{3764, "pg_ts_template", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"tmplname", sqltype.Name}, {"tmplnamespace", sqltype.Oid}}},
		{3600, "pg_ts_dict", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"dictname", sqltype.Name}, {"dictnamespace", sqltype.Oid}, {"dictowner", sqltype.Oid}, {"dicttemplate", sqltype.Oid}, {"dictinitoption", sqltype.Text}}},
		{12007, "pg_roles", []struct {
			name string
			typ  sqltype.ID
		}{{"rolname", sqltype.Name}, {"rolsuper", sqltype.Bool}, {"rolinherit", sqltype.Bool}, {"rolcreaterole", sqltype.Bool}, {"rolcreatedb", sqltype.Bool}, {"rolcanlogin", sqltype.Bool}, {"rolreplication", sqltype.Bool}, {"rolconnlimit", sqltype.Int4}, {"rolpassword", sqltype.Text}, {"rolvaliduntil", sqltype.Timestamptz}, {"rolbypassrls", sqltype.Bool}, {"rolconfig", sqltype.ID(1009)}, {"oid", sqltype.Oid}}},
		{12008, "pg_group", []struct {
			name string
			typ  sqltype.ID
		}{{"groname", sqltype.Name}, {"grosysid", sqltype.Oid}, {"grolist", sqltype.ID(1028)}}},
		{12014, "pg_user", []struct {
			name string
			typ  sqltype.ID
		}{{"usename", sqltype.Name}, {"usesysid", sqltype.Oid}, {"usecreatedb", sqltype.Bool}, {"usesuper", sqltype.Bool}, {"userepl", sqltype.Bool}, {"usebypassrls", sqltype.Bool}, {"passwd", sqltype.Text}, {"valuntil", sqltype.Timestamptz}, {"useconfig", sqltype.ID(1009)}}},
		{pgCursorsOID, "pg_cursors", []struct {
			name string
			typ  sqltype.ID
		}{{"name", sqltype.Text}, {"statement", sqltype.Text}, {"is_holdable", sqltype.Bool}, {"is_binary", sqltype.Bool}, {"is_scrollable", sqltype.Bool}, {"creation_time", sqltype.Timestamptz}}},
		{12005, "pg_timezone_names", []struct {
			name string
			typ  sqltype.ID
		}{{"name", sqltype.Text}, {"abbrev", sqltype.Text}, {"utc_offset", sqltype.Interval}, {"is_dst", sqltype.Bool}}},
		{12004, "pg_timezone_abbrevs", []struct {
			name string
			typ  sqltype.ID
		}{{"abbrev", sqltype.Text}, {"utc_offset", sqltype.Interval}, {"is_dst", sqltype.Bool}}},
		{3456, "pg_collation", []struct {
			name string
			typ  sqltype.ID
		}{
			{"oid", sqltype.Oid}, {"collname", sqltype.Name}, {"collnamespace", sqltype.Oid}, {"collowner", sqltype.Oid}, {"collprovider", sqltype.Char}, {"collisdeterministic", sqltype.Bool}, {"collencoding", sqltype.Int4}, {"collcollate", sqltype.Text}, {"collctype", sqltype.Text}, {"colllocale", sqltype.Text}, {"collicurules", sqltype.Text}, {"collversion", sqltype.Text},
		}},
		{2607, "pg_conversion", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"conname", sqltype.Name}, {"connamespace", sqltype.Oid}, {"conowner", sqltype.Oid}, {"conforencoding", sqltype.Int4}, {"contoencoding", sqltype.Int4}, {"conproc", sqltype.Regproc}, {"condefault", sqltype.Bool}}},
		{2605, "pg_cast", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"castsource", sqltype.Oid}, {"casttarget", sqltype.Oid}, {"castfunc", sqltype.Oid}, {"castcontext", sqltype.Char}, {"castmethod", sqltype.Char}}},
		{3501, "pg_enum", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"enumtypid", sqltype.Oid}, {"enumsortorder", sqltype.Float4}, {"enumlabel", sqltype.Name}}},
		{3079, "pg_extension", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"extname", sqltype.Name}, {"extowner", sqltype.Oid}, {"extnamespace", sqltype.Oid}, {"extrelocatable", sqltype.Bool}, {"extversion", sqltype.Text}, {"extconfig", sqltype.ID(1028)}, {"extcondition", sqltype.ID(1009)}}},
		{12081, "pg_available_extensions", []struct {
			name string
			typ  sqltype.ID
		}{{"name", sqltype.Name}, {"default_version", sqltype.Text}, {"installed_version", sqltype.Text}, {"comment", sqltype.Text}}},
		{12085, "pg_available_extension_versions", []struct {
			name string
			typ  sqltype.ID
		}{{"name", sqltype.Name}, {"version", sqltype.Text}, {"installed", sqltype.Bool}, {"superuser", sqltype.Bool}, {"trusted", sqltype.Bool}, {"relocatable", sqltype.Bool}, {"schema", sqltype.Name}, {"requires", sqltype.ID(1003)}, {"comment", sqltype.Text}}},
		{12048, "pg_sequences", []struct {
			name string
			typ  sqltype.ID
		}{{"schemaname", sqltype.Name}, {"sequencename", sqltype.Name}, {"sequenceowner", sqltype.Name}, {"data_type", sqltype.Regtype}, {"start_value", sqltype.Int8}, {"min_value", sqltype.Int8}, {"max_value", sqltype.Int8}, {"increment_by", sqltype.Int8}, {"cycle", sqltype.Bool}, {"cache_size", sqltype.Int8}, {"last_value", sqltype.Int8}}},
		{2224, "pg_sequence", []struct {
			name string
			typ  sqltype.ID
		}{{"seqrelid", sqltype.Oid}, {"seqtypid", sqltype.Oid}, {"seqstart", sqltype.Int8}, {"seqincrement", sqltype.Int8}, {"seqmax", sqltype.Int8}, {"seqmin", sqltype.Int8}, {"seqcache", sqltype.Int8}, {"seqcycle", sqltype.Bool}}},
		{2608, "pg_depend", []struct {
			name string
			typ  sqltype.ID
		}{{"classid", sqltype.Oid}, {"objid", sqltype.Oid}, {"objsubid", sqltype.Int4}, {"refclassid", sqltype.Oid}, {"refobjid", sqltype.Oid}, {"refobjsubid", sqltype.Int4}, {"deptype", sqltype.Char}}},
		{2609, "pg_description", []struct {
			name string
			typ  sqltype.ID
		}{{"objoid", sqltype.Oid}, {"classoid", sqltype.Oid}, {"objsubid", sqltype.Int4}, {"description", sqltype.Text}}},
		{2396, "pg_shdescription", []struct {
			name string
			typ  sqltype.ID
		}{{"objoid", sqltype.Oid}, {"classoid", sqltype.Oid}, {"description", sqltype.Text}}},
		{2606, "pg_constraint", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"conname", sqltype.Name}, {"connamespace", sqltype.Oid}, {"contype", sqltype.Char}, {"condeferrable", sqltype.Bool}, {"condeferred", sqltype.Bool}, {"conenforced", sqltype.Bool}, {"convalidated", sqltype.Bool}, {"conrelid", sqltype.Oid}, {"contypid", sqltype.Oid}, {"conindid", sqltype.Oid}, {"conparentid", sqltype.Oid}, {"confrelid", sqltype.Oid}, {"confupdtype", sqltype.Char}, {"confdeltype", sqltype.Char}, {"confmatchtype", sqltype.Char}, {"conislocal", sqltype.Bool}, {"coninhcount", sqltype.Int2}, {"connoinherit", sqltype.Bool}, {"conperiod", sqltype.Bool}, {"conkey", sqltype.ID(1005)}, {"confkey", sqltype.ID(1005)}, {"conpfeqop", sqltype.ID(1028)}, {"conppeqop", sqltype.ID(1028)}, {"conffeqop", sqltype.ID(1028)}, {"confdelsetcols", sqltype.ID(1005)}, {"conexclop", sqltype.ID(1028)}, {"conbin", sqltype.PgNodeTree}}},
		{2604, "pg_attrdef", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"adrelid", sqltype.Oid}, {"adnum", sqltype.Int2}, {"adbin", sqltype.PgNodeTree}}},
		{1259, "pg_class", []struct {
			name string
			typ  sqltype.ID
		}{
			{"oid", sqltype.Oid}, {"relname", sqltype.Name}, {"relnamespace", sqltype.Oid},
			{"reltype", sqltype.Oid}, {"reloftype", sqltype.Oid}, {"relowner", sqltype.Oid},
			{"relam", sqltype.Oid}, {"relfilenode", sqltype.Oid}, {"reltablespace", sqltype.Oid},
			{"relpages", sqltype.Int4}, {"reltuples", sqltype.Float4},
			{"relallvisible", sqltype.Int4}, {"relallfrozen", sqltype.Int4}, {"reltoastrelid", sqltype.Oid},
			{"relhasindex", sqltype.Bool}, {"relisshared", sqltype.Bool},
			{"relpersistence", sqltype.Char}, {"relkind", sqltype.Char},
			{"relnatts", sqltype.Int2}, {"relchecks", sqltype.Int2},
			{"relhasrules", sqltype.Bool}, {"relhastriggers", sqltype.Bool}, {"relhassubclass", sqltype.Bool},
			{"relrowsecurity", sqltype.Bool}, {"relforcerowsecurity", sqltype.Bool}, {"relispopulated", sqltype.Bool},
			{"relreplident", sqltype.Char}, {"relispartition", sqltype.Bool}, {"relrewrite", sqltype.Oid},
			{"relfrozenxid", sqltype.Xid}, {"relminmxid", sqltype.Xid},
			{"relacl", sqltype.ID(1034)}, {"reloptions", sqltype.ID(1009)}, {"relpartbound", sqltype.PgNodeTree},
		}},
		{2610, "pg_index", []struct {
			name string
			typ  sqltype.ID
		}{{"indexrelid", sqltype.Oid}, {"indrelid", sqltype.Oid}, {"indisunique", sqltype.Bool}, {"indisprimary", sqltype.Bool}, {"indisvalid", sqltype.Bool}, {"indisready", sqltype.Bool}, {"indislive", sqltype.Bool}, {"indnullsnotdistinct", sqltype.Bool}}},
		{2618, "pg_rewrite", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"rulename", sqltype.Name}, {"ev_class", sqltype.Oid}, {"ev_type", sqltype.Char}, {"ev_enabled", sqltype.Char}, {"is_instead", sqltype.Bool}}},
		{12000, "pg_views", []struct {
			name string
			typ  sqltype.ID
		}{{"schemaname", sqltype.Name}, {"viewname", sqltype.Name}, {"viewowner", sqltype.Name}, {"definition", sqltype.Text}}},
		{12001, "pg_prepared_xacts", []struct {
			name string
			typ  sqltype.ID
		}{{"transaction", sqltype.Xid}, {"gid", sqltype.Text}, {"prepared", sqltype.Timestamptz}, {"owner", sqltype.Name}, {"database", sqltype.Name}}},
		{12002, "pg_indexes", []struct {
			name string
			typ  sqltype.ID
		}{{"schemaname", sqltype.Name}, {"tablename", sqltype.Name}, {"indexname", sqltype.Name}, {"tablespace", sqltype.Name}, {"indexdef", sqltype.Text}}},
		{12003, "pg_tables", []struct {
			name string
			typ  sqltype.ID
		}{{"schemaname", sqltype.Name}, {"tablename", sqltype.Name}, {"tableowner", sqltype.Name}, {"tablespace", sqltype.Name}, {"hasindexes", sqltype.Bool}, {"hasrules", sqltype.Bool}, {"hastriggers", sqltype.Bool}, {"rowsecurity", sqltype.Bool}}},
		{12006, "pg_matviews", []struct {
			name string
			typ  sqltype.ID
		}{{"schemaname", sqltype.Name}, {"matviewname", sqltype.Name}, {"matviewowner", sqltype.Name}, {"tablespace", sqltype.Name}, {"hasindexes", sqltype.Bool}, {"ispopulated", sqltype.Bool}, {"definition", sqltype.Text}}},
	} {
		d := e.catalogSourceDescriptor(spec.oid, ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}, spec.name, len(spec.columns))
		d.hasIndexes = representedCatalogHasIndexes(spec.oid)
		if spec.oid == 1213 {
			d.owner = e.authorizationDomain.Principal(authorization.BootstrapPrincipalID)
			d.acl.Entries[0].Grantor = d.owner
			d.attributeHighWater = 5
		}
		d.virtualView = spec.oid == pgCursorsOID || spec.oid == 12081 || spec.oid == 12085 || spec.oid == 12048 || spec.oid == 12005 || spec.oid == 12004 || spec.oid == 12014 || spec.oid == 12007 || spec.oid == 12008
		for i, column := range spec.columns {
			typ, ok := (CatalogRead{owner: e, state: e.state}).LookupType(column.typ)
			if !ok {
				return fmt.Errorf("stuntgres: missing catalog source type")
			}
			d.columns[i] = ColumnDescriptor{Name: column.name, Usage: typ.Usage(), Nullable: true, NullabilityKnown: true, OriginTable: uint32(spec.oid), OriginAttribute: int16(i + 1)}
			if spec.oid == pgAttributeOID {
				d.columns[i].Nullable = i >= 20
				if column.typ == sqltype.ID(1009) {
					d.columns[i].Usage.Collation.ID = 950
				}
			}
			if spec.oid == 3456 {
				d.columns[i].Nullable = i >= 7
				if column.typ == sqltype.Text {
					d.columns[i].Usage.Collation.ID = 950
				}
			}
			if spec.oid == 1262 {
				d.columns[i].Nullable = i == 5 || i == 6 || i >= 8 && i <= 11 || i >= 14
				if column.typ == sqltype.Text {
					d.columns[i].Usage.Collation.ID = 950
				}
			}
			if spec.oid == 1259 {
				d.columns[i].Nullable = i >= 31
				if column.typ == sqltype.ID(1009) {
					d.columns[i].Usage.Collation.ID = 950
				}
			}
			if spec.oid == 2606 {
				d.columns[i].Nullable = i >= 20
			}
			if spec.oid == 2604 || spec.oid == 2224 || spec.oid == 2608 || spec.oid == 2605 || spec.oid == 3501 || spec.oid == 2610 {
				d.columns[i].Nullable = false
			}
			if spec.oid == ProcedureClassID || spec.oid == TSTemplateClassID {
				d.columns[i].Nullable = false
			}
			if spec.oid == TSDictionaryClassID {
				d.columns[i].Nullable = i == 5
			}
			if spec.oid == 2609 || spec.oid == 2396 {
				d.columns[i].Nullable = false
				if column.typ == sqltype.Text {
					d.columns[i].Usage.Collation.ID = 950
				}
			}
			if spec.oid == 3079 {
				d.columns[i].Nullable = i >= 6
				if column.typ == sqltype.Text || column.typ == sqltype.ID(1009) {
					d.columns[i].Usage.Collation.ID = 950
				}
			}
			if spec.oid == 12081 && column.name == "installed_version" {
				d.columns[i].Usage.Collation.ID = 950
			}
			if spec.oid == 12014 && column.name == "useconfig" || spec.oid == 12007 && column.name == "rolconfig" {
				d.columns[i].Usage.Collation.ID = 950
			}
			d.attributes[i] = int32(i + 1)
			if spec.oid == 1213 {
				d.columns[i].Nullable = i == 3
				if i == 3 {
					// Attribute 4 (spcacl) is omitted from this bounded projection.
					d.columns[i].OriginAttribute = 5
					d.attributes[i] = 5
					d.columns[i].Usage.Collation.ID = 950
				}
			}
			d.index[column.name] = i
		}
		e.registerCatalogSource(d)
	}
	return e.initializeTablePrivileges()
}

func (e *Engine) catalogSourceDescriptor(oid OID, namespace ObjectAddress, name string, count int) *relationDescriptor {
	owner := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	return &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: oid}, namespace: namespace, name: name, revision: 1, relationKind: catalogRelation, owner: owner, index: make(map[string]int, count), attributes: make([]int32, count), columns: make([]ColumnDescriptor, count), attributeHighWater: int32(count), acl: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}}}
}

func (e *Engine) registerCatalogSource(d *relationDescriptor) {
	initializeCatalogAttributes(d)
	if address, ok := e.state.names[NameKey{ClassID: TypeClassID, Namespace: d.namespace, Name: d.name}]; ok {
		row := e.state.descriptors[address].(*typeDescriptor)
		row.owningRelation, row.owner = d.address, d.owner
		d.rowType = row.typ.ID()
		if row.array != 0 {
			array := e.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(row.array)}].(*typeDescriptor)
			array.owningRelation, array.owner = d.address, d.owner
		}
	}
	e.state.descriptors[d.address] = d
	e.state.names[NameKey{ClassID: RelationClassID, Namespace: d.namespace, Name: d.name}] = d.address
}

func (e *Engine) captureCatalogSourceLocked(ctx context.Context, catalog CatalogRead, roles authorization.Read, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	if source.address.ObjectID == pgAttributeOID {
		return e.capturePGAttribute(ctx, catalog, roles, ref, source)
	}
	if source.address.ObjectID == 1262 {
		return e.capturePGDatabase(ctx, catalog, roles, ref, source)
	}
	if source.address.ObjectID == 1213 {
		return capturePGTablespace(ctx, ref, source)
	}
	if source.address.ObjectID == 3079 || source.address.ObjectID == 1255 || source.address.ObjectID == 3764 || source.address.ObjectID == 3600 {
		return captureExtensionCatalog(ctx, catalog, ref, source)
	}
	if source.address.ObjectID == 3456 {
		return capturePGCollation(ctx, catalog, ref, source)
	}
	if source.address.ObjectID == 2607 {
		return capturePGConversion(ctx, catalog, ref, source)
	}
	if source.address.ObjectID == 2605 {
		return capturePGCast(ctx, catalog, ref, source)
	}
	if source.address.ObjectID == 3501 {
		return capturePGEnum(ctx, catalog, ref, source)
	}
	if source.address.ObjectID == 2606 {
		return capturePGConstraint(ctx, catalog, ref, source)
	}
	if source.address.ObjectID == 2224 {
		return capturePGSequence(ctx, catalog.state, nil, ref, source)
	}
	if source.address.ObjectID == 2609 {
		return capturePGDescription(ctx, catalog, ref, source)
	}
	if source.address.ObjectID == 12001 {
		return e.capturePreparedSourceLocked(ctx, ref, source)
	}
	if source.address.ObjectID == 2608 {
		return capturePGDepend(ctx, catalog, ref, source)
	}
	if source.address.ObjectID == 12000 || source.address.ObjectID == 12003 {
		return e.captureTableViewSummary(ctx, catalog, roles, ref, source, viewPresentation{})
	}
	var relations []*relationDescriptor
	for _, descriptor := range catalog.state.descriptors {
		if relation, ok := descriptor.(*relationDescriptor); ok {
			relations = append(relations, relation)
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].address.ObjectID < relations[j].address.ObjectID })
	rows := make([][]sqltype.Value, 0, len(relations))
	var captureErr error
	integer := func(i int, n int64) sqltype.Value {
		value, err := sqltype.Integer(source.columns[i].Usage.Type, n)
		if err != nil && captureErr == nil {
			captureErr = err
		}
		return value
	}
	oid := func(i int, n uint32) sqltype.Value {
		value, err := sqltype.OIDValue(source.columns[i].Usage.Type, n)
		if err != nil && captureErr == nil {
			captureErr = err
		}
		return value
	}
	text := func(i int, s string) sqltype.Value { return sqltype.String(source.columns[i].Usage.Type, s) }
	boolean := func(i int, b bool) sqltype.Value { return sqltype.Boolean(source.columns[i].Usage.Type, b) }
	for _, relation := range relations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch source.address.ObjectID {
		case 2610:
			if definition := relation.sqlIndex; definition != nil {
				rows = append(rows, []sqltype.Value{oid(0, uint32(relation.address.ObjectID)), oid(1, uint32(definition.parent.ObjectID)), boolean(2, definition.unique), boolean(3, definition.constraint != nil && definition.constraint.Primary), boolean(4, definition.valid), boolean(5, definition.ready), boolean(6, definition.live), boolean(7, definition.nullsNotDistinct)})
			}
		case 2604:
			for attribute, definition := range relation.defaults {
				tree, err := catalogTree(source.columns[3].Usage.Type, definition.expression)
				if err != nil {
					return nil, err
				}
				rows = append(rows, []sqltype.Value{oid(0, uint32(definition.OID)), oid(1, uint32(relation.address.ObjectID)), integer(2, int64(attribute)), tree})
			}
			for attribute, definition := range relation.generations {
				tree, err := catalogTree(source.columns[3].Usage.Type, definition.expression)
				if err != nil {
					return nil, err
				}
				rows = append(rows, []sqltype.Value{oid(0, uint32(definition.OID)), oid(1, uint32(relation.address.ObjectID)), integer(2, int64(attribute)), tree})
			}
		case 1259:
			row, err := e.pgClassRow(ctx, catalog, roles, source, relation)
			if err != nil {
				return nil, err
			}
			rows = append(rows, row)
		case 2618:
			if relation.view == nil {
				continue
			}
			rows = append(rows, []sqltype.Value{oid(0, uint32(relation.view.rule)), text(1, "_RETURN"), oid(2, uint32(relation.address.ObjectID)), text(3, "1"), text(4, "O"), boolean(5, true)})
		default:
			return nil, fmt.Errorf("stuntgres: unknown catalog source")
		}
		if captureErr != nil {
			return nil, captureErr
		}
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}

func nonuniqueConstraintProvenance(catalog CatalogRead, table *relationDescriptor, name string, attribute int32, check bool) (bool, int64) {
	if graph := table.inheritance; graph != nil {
		provenance, exists := graph.notNull[attribute]
		if check {
			provenance, exists = graph.checks[name]
		}
		if exists {
			return provenance.local, int64(len(provenance.origins))
		}
	}
	if table.membership != nil {
		parent := catalog.state.descriptors[table.membership.parent].(*relationDescriptor)
		if check {
			for _, constraint := range parent.checkConstraints {
				if constraint.Name == name {
					return false, 1
				}
			}
		} else {
			slot := slices.Index(table.attributes, attribute)
			if slot >= 0 {
				parentSlot, exists := parent.index[table.columns[slot].Name]
				if exists {
					parentAttribute := parent.attributes[parentSlot]
					for _, constraint := range parent.notNullConstraints {
						if constraint.Attribute == parentAttribute {
							return false, 1
						}
					}
				}
			}
		}
	}
	return true, 0
}
