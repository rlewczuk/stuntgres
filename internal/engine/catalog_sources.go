package engine

import (
	"context"
	"fmt"
	"sort"
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
		{2605, "pg_cast", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"castsource", sqltype.Oid}, {"casttarget", sqltype.Oid}, {"castfunc", sqltype.Oid}, {"castcontext", sqltype.Char}, {"castmethod", sqltype.Char}}},
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
		}{{"oid", sqltype.Oid}, {"conname", sqltype.Name}, {"contype", sqltype.Char}, {"conrelid", sqltype.Oid}, {"conbin", sqltype.PgNodeTree}}},
		{2604, "pg_attrdef", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"adrelid", sqltype.Oid}, {"adnum", sqltype.Int2}, {"adbin", sqltype.PgNodeTree}}},
		{1259, "pg_class", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"relname", sqltype.Name}, {"relnamespace", sqltype.Oid}, {"relowner", sqltype.Oid}, {"relkind", sqltype.Char}, {"relnatts", sqltype.Int2}, {"relhasrules", sqltype.Bool}, {"relispopulated", sqltype.Bool}, {"reloftype", sqltype.Oid}}},
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
		d.virtualView = spec.oid == 12081 || spec.oid == 12085 || spec.oid == 12048 || spec.oid == 12005 || spec.oid == 12004 || spec.oid == 12014 || spec.oid == 12007 || spec.oid == 12008
		for i, column := range spec.columns {
			typ, ok := (CatalogRead{owner: e, state: e.state}).LookupType(column.typ)
			if !ok {
				return fmt.Errorf("stuntgres: missing catalog source type")
			}
			d.columns[i] = ColumnDescriptor{Name: column.name, Usage: typ.Usage(), Nullable: true, NullabilityKnown: true, OriginTable: uint32(spec.oid), OriginAttribute: int16(i + 1)}
			if spec.oid == 3456 {
				d.columns[i].Nullable = i >= 7
			}
			if spec.oid == 2604 || spec.oid == 2224 || spec.oid == 2608 || spec.oid == 2605 {
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
			d.index[column.name] = i
		}
		e.registerCatalogSource(d)
	}
	return e.initializeTablePrivileges()
}

func (e *Engine) catalogSourceDescriptor(oid OID, namespace ObjectAddress, name string, count int) *relationDescriptor {
	owner := e.roles.names["postgres"]
	return &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: oid}, namespace: namespace, name: name, revision: 1, relationKind: catalogRelation, owner: owner, index: make(map[string]int, count), attributes: make([]int32, count), columns: make([]ColumnDescriptor, count), attributeHighWater: int32(count), acl: ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: owner, Grantee: ACLGrantee{Public: true}, Privileges: SelectPrivilege}}}}
}

func (e *Engine) registerCatalogSource(d *relationDescriptor) {
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

func (e *Engine) captureCatalogSourceLocked(ctx context.Context, catalog CatalogRead, roles RoleRead, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	if source.address.ObjectID == 3079 || source.address.ObjectID == 1255 || source.address.ObjectID == 3764 || source.address.ObjectID == 3600 {
		return captureExtensionCatalog(ctx, catalog, ref, source)
	}
	if source.address.ObjectID == 3456 {
		return capturePGCollation(ctx, catalog, ref, source)
	}
	if source.address.ObjectID == 2605 {
		return capturePGCast(ctx, catalog, ref, source)
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
		case 2606:
			for _, definition := range relation.notNullConstraints {
				rows = append(rows, []sqltype.Value{oid(0, uint32(definition.OID)), text(1, definition.Name), text(2, "n"), oid(3, uint32(relation.address.ObjectID)), sqltype.Null(source.columns[4].Usage.Type)})
			}
			for _, definition := range relation.checkConstraints {
				tree, err := catalogTree(source.columns[4].Usage.Type, definition.expression)
				if err != nil {
					return nil, err
				}
				rows = append(rows, []sqltype.Value{oid(0, uint32(definition.OID)), text(1, definition.Name), text(2, "c"), oid(3, uint32(relation.address.ObjectID)), tree})
			}
			for _, definition := range relation.uniqueConstraints {
				kind := "u"
				if definition.Primary {
					kind = "p"
				}
				rows = append(rows, []sqltype.Value{oid(0, uint32(definition.OID)), text(1, definition.Name), text(2, kind), oid(3, uint32(relation.address.ObjectID)), sqltype.Null(source.columns[4].Usage.Type)})
			}
			for _, definition := range relation.exclusionConstraints {
				rows = append(rows, []sqltype.Value{oid(0, uint32(definition.OID)), text(1, definition.Name), text(2, "x"), oid(3, uint32(relation.address.ObjectID)), sqltype.Null(source.columns[4].Usage.Type)})
			}
			for _, definition := range relation.foreignKeys {
				rows = append(rows, []sqltype.Value{oid(0, uint32(definition.OID)), text(1, definition.Name), text(2, "f"), oid(3, uint32(relation.address.ObjectID)), sqltype.Null(source.columns[4].Usage.Type)})
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
			kind := "r"
			if relation.relationKind == sequenceRelation {
				kind = "S"
			} else if relation.relationKind == materializedViewRelation {
				kind = "m"
			} else if relation.relationKind == viewRelation || relation.virtualView {
				kind = "v"
			} else if relation.relationKind == indexRelation {
				kind = "i"
			} else if relation.relationKind == compositeRelation {
				kind = "c"
			} else if relation.relationKind == partitionedRelation {
				kind = "p"
			}
			rows = append(rows, []sqltype.Value{oid(0, uint32(relation.address.ObjectID)), text(1, relation.name), oid(2, uint32(relation.namespace.ObjectID)), oid(3, uint32(relation.owner.id)), text(4, kind), integer(5, int64(relation.attributeHighWater)), boolean(6, relation.view != nil), boolean(7, relation.relationKind != materializedViewRelation || relation.populated), oid(8, uint32(relation.ofType.ObjectID))})
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
