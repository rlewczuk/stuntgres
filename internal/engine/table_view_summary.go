package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
)

// Index publication and parent history are one immutable catalog candidate.
// DROP deliberately never clears this flag (PostgreSQL18 pg_class lazy flags).
func (e *Engine) markStageIndexHistoryLocked(stage *CatalogStage) error {
	for _, descriptor := range stage.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		address := index.sqlIndex.parent
		descriptor, staged := stage.descriptors[address]
		if !staged {
			descriptor = stage.base.state.descriptors[address]
		}
		parent, ok := descriptor.(*relationDescriptor)
		if !ok || (parent.relationKind != tableRelation && parent.relationKind != materializedViewRelation && parent.relationKind != partitionedRelation) {
			return fmt.Errorf("stuntgres: invalid index parent")
		}
		if parent.hasIndexes {
			continue
		}
		next := *parent
		next.hasIndexes = true
		if staged {
			// Preserve the complete already-staged parent and its revision/rowtype;
			// UNIQUE can create the parent and its indexes in this same candidate.
			stage.descriptors[address] = &next
		} else {
			revision, err := e.revisionLocked()
			if err != nil {
				return err
			}
			next.revision = revision
			if err := stage.Replace(&next); err != nil {
				return err
			}
		}
	}
	return nil
}

// Replacement admission owns private descriptor maps, but its descriptors still
// belong to retained source snapshots. Normalize represented indexes into that
// candidate without mutating source roots or clearing history-only true flags.
// Structural index validation remains independent of this publication metadata.
func normalizeReplacementIndexHistory(ctx context.Context, catalog CatalogRead) error {
	for _, descriptor := range catalog.state.descriptors {
		if err := ctx.Err(); err != nil {
			return err
		}
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		parent, ok := catalog.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		if !ok || (parent.relationKind != tableRelation && parent.relationKind != materializedViewRelation && parent.relationKind != partitionedRelation) {
			return fmt.Errorf("stuntgres: invalid index parent")
		}
		if !parent.hasIndexes {
			next := *parent
			next.hasIndexes = true
			catalog.state.descriptors[parent.address] = &next
		}
	}
	return nil
}

// Only the represented builtins are classified here. catalogRelation is a
// virtual-source implementation kind, not a PostgreSQL table/view classifier.
func representedSummaryTable(oid OID) bool {
	switch oid {
	case 1213, 2605, 3079, 2224, 2608, 2609, 2396, 2606, 2604, 1259, 2610, 2618:
		return true
	}
	return false
}

func (e *Engine) captureTableViewSummary(ctx context.Context, catalog CatalogRead, roles authorization.Read, ref ObjectRef, source *relationDescriptor, presentation viewPresentation) (*querySource, error) {
	var rows [][]sqltype.Value
	text := func(i int, value string) sqltype.Value { return sqltype.String(source.columns[i].Usage.Type, value) }
	boolean := func(i int, value bool) sqltype.Value { return sqltype.Boolean(source.columns[i].Usage.Type, value) }
	for _, descriptor := range catalog.state.descriptors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		relation, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		builtinTable := relation.relationKind == catalogRelation && representedSummaryTable(relation.address.ObjectID)
		builtinDefinition := ""
		if relation.relationKind == catalogRelation {
			builtinDefinition = representedSummaryViewDefinition(relation.address.ObjectID)
		}
		if source.address.ObjectID == 12003 {
			if relation.relationKind != tableRelation && relation.relationKind != partitionedRelation && !builtinTable {
				continue
			}
		} else if source.address.ObjectID == 12006 {
			if relation.relationKind != materializedViewRelation {
				continue
			}
		} else if relation.relationKind != viewRelation && builtinDefinition == "" {
			continue
		}
		owner, err := roles.Descriptor(relation.owner)
		if err != nil {
			return nil, err
		}
		schema := catalog.state.descriptors[relation.namespace].Name()
		if source.address.ObjectID == 12003 {
			tablespace := sqltype.Null(source.columns[3].Usage.Type)
			// Released catalog metadata, not fictitious native backing indexes/storage.
			// Represented shared catalogs belong to pg_global.
			if builtinTable && (relation.address.ObjectID == 2396 || relation.address.ObjectID == 1213) {
				tablespace = text(3, "pg_global")
			}
			rows = append(rows, []sqltype.Value{text(0, schema), text(1, relation.name), text(2, owner.Name), tablespace, boolean(4, relation.hasIndexes || builtinTable), boolean(5, false), boolean(6, false), boolean(7, relation.rlsEnabled)})
		} else {
			definition := builtinDefinition
			if relation.view != nil {
				// Follow current physical names without changing the caller's
				// replay/display policy or the view's frozen output labels.
				presentation.columns = relation.columns
				renderer := newViewRenderer(relation.view)
				renderer.presentation = presentation
				renderer.currentNames = true
				definition, err = renderer.selectQuerySQL(ctx, catalog, relation.view.syntax, relation.view.queries[relation.view.syntax])
				if err != nil {
					return nil, err
				}
				definition += ";"
			}
			if source.address.ObjectID == 12006 {
				rows = append(rows, []sqltype.Value{text(0, schema), text(1, relation.name), text(2, owner.Name), sqltype.Null(source.columns[3].Usage.Type), boolean(4, relation.hasIndexes), boolean(5, relation.populated), text(6, definition)})
			} else {
				rows = append(rows, []sqltype.Value{text(0, schema), text(1, relation.name), text(2, owner.Name), text(3, definition)})
			}
		}
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}

// Compatibility metadata sourced from the CREATE VIEW sections of
// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/catalog/system_views.sql
// and PostgreSQL18.0 pg_views reconstruction. These are not native executable
// stored definitions: absent catalogs/functions are not implemented by this text.
// In particular native pg_indexes retains its separately documented ACL policy.
func representedSummaryViewDefinition(oid OID) string {
	switch oid {
	case 12081:
		return ` SELECT e.name,
    e.default_version,
    x.extversion AS installed_version,
    e.comment
   FROM (pg_available_extensions() e(name, default_version, comment)
     LEFT JOIN pg_extension x ON ((e.name = x.extname)));`
	case 12085:
		return ` SELECT e.name,
    e.version,
    (x.extname IS NOT NULL) AS installed,
    e.superuser,
    e.trusted,
    e.relocatable,
    e.schema,
    e.requires,
    e.comment
   FROM (pg_available_extension_versions() e(name, version, superuser, trusted, relocatable, schema, requires, comment)
     LEFT JOIN pg_extension x ON (((e.name = x.extname) AND (e.version = x.extversion))));`
	case 12048:
		return ` SELECT n.nspname AS schemaname,
    c.relname AS sequencename,
    pg_get_userbyid(c.relowner) AS sequenceowner,
    (s.seqtypid)::regtype AS data_type,
    s.seqstart AS start_value,
    s.seqmin AS min_value,
    s.seqmax AS max_value,
    s.seqincrement AS increment_by,
    s.seqcycle AS cycle,
    s.seqcache AS cache_size,
    pg_sequence_last_value((c.oid)::regclass) AS last_value
   FROM ((pg_sequence s
     JOIN pg_class c ON ((c.oid = s.seqrelid)))
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
  WHERE ((NOT pg_is_other_temp_schema(n.oid)) AND (c.relkind = 'S'::"char"));`
	case 12008:
		return ` SELECT rolname AS groname,
    oid AS grosysid,
    ARRAY( SELECT pg_auth_members.member
           FROM pg_auth_members
          WHERE (pg_auth_members.roleid = pg_authid.oid)) AS grolist
   FROM pg_authid
  WHERE (NOT rolcanlogin);`
	case 12007:
		return ` SELECT pg_authid.rolname,
    pg_authid.rolsuper,
    pg_authid.rolinherit,
    pg_authid.rolcreaterole,
    pg_authid.rolcreatedb,
    pg_authid.rolcanlogin,
    pg_authid.rolreplication,
    pg_authid.rolconnlimit,
    '********'::text AS rolpassword,
    pg_authid.rolvaliduntil,
    pg_authid.rolbypassrls,
    s.setconfig AS rolconfig,
    pg_authid.oid
   FROM (pg_authid
     LEFT JOIN pg_db_role_setting s ON (((pg_authid.oid = s.setrole) AND (s.setdatabase = (0)::oid))));`
	case 12006:
		return ` SELECT n.nspname AS schemaname,
    c.relname AS matviewname,
    pg_get_userbyid(c.relowner) AS matviewowner,
    t.spcname AS tablespace,
    c.relhasindex AS hasindexes,
    c.relispopulated AS ispopulated,
    pg_get_viewdef(c.oid) AS definition
   FROM ((pg_class c
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
     LEFT JOIN pg_tablespace t ON ((t.oid = c.reltablespace)))
  WHERE (c.relkind = 'm'::"char");`
	case 12014:
		return ` SELECT usename,
    usesysid,
    usecreatedb,
    usesuper,
    userepl,
    usebypassrls,
    '********'::text AS passwd,
    valuntil,
    useconfig
   FROM pg_shadow;`
	case 12002:
		return ` SELECT n.nspname AS schemaname,
    c.relname AS tablename,
    i.relname AS indexname,
    t.spcname AS tablespace,
    pg_get_indexdef(i.oid) AS indexdef
   FROM ((((pg_index x
     JOIN pg_class c ON ((c.oid = x.indrelid)))
     JOIN pg_class i ON ((i.oid = x.indexrelid)))
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
     LEFT JOIN pg_tablespace t ON ((t.oid = i.reltablespace)))
  WHERE ((c.relkind = ANY (ARRAY['r'::"char", 'm'::"char", 'p'::"char"])) AND (i.relkind = ANY (ARRAY['i'::"char", 'I'::"char"])));`
	case 12001:
		return ` SELECT p.transaction,
    p.gid,
    p.prepared,
    u.rolname AS owner,
    d.datname AS database
   FROM ((pg_prepared_xact() p(transaction, gid, prepared, ownerid, dbid)
     LEFT JOIN pg_authid u ON ((p.ownerid = u.oid)))
     LEFT JOIN pg_database d ON ((p.dbid = d.oid)));`
	case 12003:
		return ` SELECT n.nspname AS schemaname,
    c.relname AS tablename,
    pg_get_userbyid(c.relowner) AS tableowner,
    t.spcname AS tablespace,
    c.relhasindex AS hasindexes,
    c.relhasrules AS hasrules,
    c.relhastriggers AS hastriggers,
    c.relrowsecurity AS rowsecurity
   FROM ((pg_class c
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
     LEFT JOIN pg_tablespace t ON ((t.oid = c.reltablespace)))
  WHERE (c.relkind = ANY (ARRAY['r'::"char", 'p'::"char"]));`
	case 12000:
		return ` SELECT n.nspname AS schemaname,
    c.relname AS viewname,
    pg_get_userbyid(c.relowner) AS viewowner,
    pg_get_viewdef(c.oid) AS definition
   FROM (pg_class c
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
  WHERE (c.relkind = 'v'::"char");`
	}
	return ""
}
