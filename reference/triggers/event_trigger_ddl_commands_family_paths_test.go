package reference_test

import (
	"context"
	"reflect"
	"strconv"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func ddlCommandsQueryCreation(t *testing.T) {
	for _, tc := range []struct{ query, tag, name string }{{"CREATE TABLE et_dc_query AS SELECT 7 AS i", "CREATE TABLE AS", "et_dc_query"}, {"SELECT 7 AS i INTO et_dc_query", "SELECT INTO", "et_dc_query"}} {
		t.Run(tc.tag, func(t *testing.T) {
			ddlCommandScenario(t, func(d ddlCommandCase) {
				d.install("'" + tc.tag + "'")
				for _, prepared := range []bool{false, true} {
					d.exec("BEGIN")
					s := fixture.Exec(tc.query, 1)
					s.Prepared = prepared
					d.run(s)
					d.rows(ddlCommandRows("1259", tc.tag, "table", "public", "public."+tc.name))
					d.run(fixture.Select("SELECT i FROM et_dc_query", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
					d.exec("ROLLBACK")
				}
			})
		})
	}
}

func ddlCommandsNoopData(t *testing.T) {
	ddlCommandScenario(t, func(d ddlCommandCase) {
		d.exec("CREATE TABLE et_dc_noop(i integer)")
		d.run(fixture.Exec("INSERT INTO et_dc_noop VALUES(7)", 1))
		var oid string
		if err := d.c.QueryRowContext(d.p.Ctx, "SELECT oid::text FROM pg_class WHERE oid='et_dc_noop'::regclass").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		d.install("'ALTER TABLE'")
		for _, query := range []string{"ALTER TABLE et_dc_noop OWNER TO CURRENT_USER", "ALTER TABLE et_dc_noop SET SCHEMA public"} {
			for _, prepared := range []bool{false, true} {
				d.clear()
				s := fixture.Exec(query, 0)
				s.Prepared = prepared
				d.run(s)
				d.rows(ddlCommandRows("1259", "ALTER TABLE", "table", "public", "public.et_dc_noop"))
				d.run(fixture.Select("SELECT objid::text AS oid FROM et_dc_audit ORDER BY pass", []string{"oid"}, []string{"TEXT"}, [][]any{{oid}, {oid}}))
				d.run(fixture.Select("SELECT i FROM et_dc_noop", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
		}
	})
}

func ddlCommandsDropOwned(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"et_dc_role"})
	ddlCommandScenario(t, func(d ddlCommandCase) {
		t := d.t
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, query := range []string{"ROLLBACK", "DROP OWNED BY et_dc_role"} {
				if _, err := d.c.ExecContext(ctx, query); err != nil {
					t.Error("role dependency cleanup", err)
				}
			}
		}()
		d.exec("CREATE ROLE et_dc_role", "CREATE TABLE et_dc_survivor(i integer)", "CREATE FUNCTION et_dc_survivor_fn(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'", "GRANT SELECT ON et_dc_survivor TO et_dc_role", "GRANT EXECUTE ON FUNCTION et_dc_survivor_fn(integer) TO et_dc_role", "CREATE TABLE et_dc_owned(i integer)", "ALTER TABLE et_dc_owned OWNER TO et_dc_role", "ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO et_dc_role")
		d.run(fixture.Exec("INSERT INTO et_dc_survivor VALUES(7)", 1))
		d.install("'DROP OWNED'")
		for _, prepared := range []bool{false, true} {
			d.exec("BEGIN")
			s := fixture.Exec("DROP OWNED BY et_dc_role", 0)
			s.Prepared = prepared
			d.run(s)
			var want [][]any
			// The explicit survivor grant and the default grant on the later
			// audit table are separate pg_shdepend ACL cleanup invocations.
			// Routine cleanup adds its own receipt; do not deduplicate TABLE.
			for _, kind := range []string{"TABLE", "ROUTINE", "TABLE"} {
				for _, pass := range []int64{1, 2} {
					want = append(want, []any{pass, nil, nil, "REVOKE", kind, nil, nil, false, true})
				}
			}
			d.rows(want)
			d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_dc_owned'", 0))
			d.run(fixture.Select("SELECT i,et_dc_survivor_fn(i) AS result FROM et_dc_survivor", []string{"i", "result"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}))
			d.exec("ROLLBACK")
			d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_dc_owned'", 1))
			d.run(fixture.Select("SELECT has_table_privilege('et_dc_role','et_dc_survivor','SELECT') AS granted", []string{"granted"}, []string{"BOOL"}, [][]any{{true}}))
		}
	})
}

func ddlCommandsUserMapping(t *testing.T) {
	ddlCommandScenario(t, func(d ddlCommandCase) {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			d.c.ExecContext(ctx, "ROLLBACK")
			if _, err := d.c.ExecContext(ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS et_dc_fdw CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		d.exec("CREATE FOREIGN DATA WRAPPER et_dc_fdw", "CREATE SERVER et_dc_server FOREIGN DATA WRAPPER et_dc_fdw")
		var role string
		if err := d.c.QueryRowContext(d.p.Ctx, "SELECT quote_ident(current_user)").Scan(&role); err != nil {
			t.Fatal(err)
		}
		d.install("'CREATE USER MAPPING'")
		for _, prepared := range []bool{false, true} {
			d.exec("BEGIN")
			s := fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER et_dc_server", 0)
			s.Prepared = prepared
			d.run(s)
			d.rows(ddlCommandRows("1418", "CREATE USER MAPPING", "user mapping", nil, role+" on server et_dc_server"))
			d.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_audit a JOIN pg_user_mapping m ON a.objid=m.oid JOIN pg_foreign_server s ON m.umserver=s.oid WHERE a.classid='pg_user_mapping'::regclass AND s.srvname='et_dc_server'", 2))
			d.exec("ROLLBACK")
		}
	})
}

func ddlCommandsSubobjects(t *testing.T) {
	for _, tc := range []struct {
		name, query, class, kind, identity, oid string
		sub                                     int64
	}{
		{"comment_column", "COMMENT ON COLUMN et_dc_sub.i IS 'inspected'", "1259", "table column", "public.et_dc_sub.i", "SELECT 'et_dc_sub'::regclass::oid::text", 1},
		{"rename_column", "ALTER TABLE et_dc_sub RENAME COLUMN i TO j", "1259", "table column", "public.et_dc_sub.j", "SELECT 'et_dc_sub'::regclass::oid::text", 1},
		{"rename_constraint", "ALTER TABLE et_dc_sub RENAME CONSTRAINT et_dc_pk TO et_dc_pk_new", "2606", "table constraint", "et_dc_pk_new on public.et_dc_sub", "SELECT oid::text FROM pg_constraint WHERE conname='et_dc_pk' AND conrelid='et_dc_sub'::regclass", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ddlCommandScenario(t, func(d ddlCommandCase) {
				d.exec("CREATE TABLE et_dc_sub(i integer CONSTRAINT et_dc_pk PRIMARY KEY)")
				var oid string
				if err := d.c.QueryRowContext(d.p.Ctx, tc.oid).Scan(&oid); err != nil {
					t.Fatal(err)
				}
				tag := "ALTER TABLE"
				if tc.name == "comment_column" {
					tag = "COMMENT"
				}
				d.install("'" + tag + "'")
				for _, prepared := range []bool{false, true} {
					d.exec("BEGIN")
					s := fixture.Exec(tc.query, 0)
					s.Prepared = prepared
					d.run(s)
					class := ddlCommandRows(tc.class, tag, tc.kind, "public", tc.identity)[0][1]
					d.rows([][]any{{int64(1), class, tc.sub, tag, tc.kind, "public", tc.identity, false, true}, {int64(2), class, tc.sub, tag, tc.kind, "public", tc.identity, false, true}})
					d.run(fixture.Select("SELECT objid::text AS oid FROM et_dc_audit ORDER BY pass", []string{"oid"}, []string{"TEXT"}, [][]any{{oid}, {oid}}))
					d.exec("ROLLBACK")
				}
			})
		})
	}
}

func ddlCommandsNoopDistinctions(t *testing.T) {
	ddlCommandScenario(t, func(d ddlCommandCase) {
		var notices []fixture.Diagnostic
		pool, c, recorder := fixture.NoticeReferencePool(t, d.p, d.b.PG, &notices)
		defer pool.Close()
		defer c.Close()
		defer c.ExecContext(context.WithoutCancel(d.p.Ctx), "ROLLBACK")
		d.c, d.b.Recorder = c, recorder
		d.exec("CREATE DOMAIN et_dc_domain AS integer", "CREATE TABLE et_dc_default(i integer)", "CREATE TYPE et_dc_enum AS ENUM('a','b')")
		var enumOID string
		if err := d.c.QueryRowContext(d.p.Ctx, "SELECT 'et_dc_enum'::regtype::oid::text").Scan(&enumOID); err != nil {
			t.Fatal(err)
		}
		d.install("'ALTER DOMAIN','ALTER TABLE','ALTER TYPE'")
		for _, prepared := range []bool{false, true} {
			d.exec("BEGIN")
			notices = nil
			s := fixture.Exec("ALTER DOMAIN et_dc_domain DROP CONSTRAINT IF EXISTS et_dc_absent", 0)
			s.Prepared = prepared
			d.run(s)
			wantNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "constraint \"et_dc_absent\" of domain \"et_dc_domain\" does not exist, skipping"}
			if got := fixture.NoticeWitnesses(t, d.b.PG, notices, wantNotice); !reflect.DeepEqual(got, []fixture.Diagnostic{wantNotice}) {
				t.Fatalf("missing-constraint notice=%#v", got)
			}
			d.rows(ddlCommandRows("1247", "ALTER DOMAIN", "type", "public", "public.et_dc_domain"))
			d.clear()
			s = fixture.Exec("ALTER DOMAIN et_dc_domain DROP NOT NULL", 0)
			s.Prepared = prepared
			d.run(s)
			d.rows(nil)
			s = fixture.Exec("ALTER DOMAIN et_dc_domain SET NOT NULL", 0)
			s.Prepared = prepared
			d.run(s)
			d.rows(ddlCommandRows("1247", "ALTER DOMAIN", "type", "public", "public.et_dc_domain"))
			d.clear()
			d.run(s)
			d.rows(nil)
			s = fixture.Exec("ALTER TABLE et_dc_default ALTER COLUMN i DROP DEFAULT", 0)
			s.Prepared = prepared
			d.run(s)
			d.rows(ddlCommandRows("1259", "ALTER TABLE", "table", "public", "public.et_dc_default"))
			d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_attrdef WHERE adrelid='et_dc_default'::regclass", 0))
			d.clear()
			notices = nil
			s = fixture.Exec("ALTER TYPE et_dc_enum ADD VALUE IF NOT EXISTS 'a'", 0)
			s.Prepared = prepared
			d.run(s)
			wantNotice = fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: "enum label \"a\" already exists, skipping"}
			if got := fixture.NoticeWitnesses(t, d.b.PG, notices, wantNotice); !reflect.DeepEqual(got, []fixture.Diagnostic{wantNotice}) {
				t.Fatalf("existing-enum-label notice=%#v", got)
			}
			d.rows(ddlCommandRows("1247", "ALTER TYPE", "type", "public", "public.et_dc_enum"))
			address := fixture.Select("SELECT objid::text AS oid FROM et_dc_audit ORDER BY pass", []string{"oid"}, []string{"TEXT"}, [][]any{{enumOID}, {enumOID}})
			d.run(address)
			d.run(fixture.Prepared(address))
			labels := fixture.Select("SELECT enumlabel FROM pg_enum WHERE enumtypid='et_dc_enum'::regtype ORDER BY enumsortorder", []string{"enumlabel"}, []string{"NAME"}, [][]any{{"a"}, {"b"}})
			d.run(labels)
			d.run(fixture.Prepared(labels))
			d.exec("ROLLBACK")
		}
	})
}

func ddlCommandsSameTablespace(t *testing.T) {
	ddlCommandScenario(t, func(d ddlCommandCase) {
		d.exec("CREATE TABLE et_dc_space(i integer)", "CREATE INDEX et_dc_space_idx ON et_dc_space(i)")
		d.run(fixture.Exec("CREATE MATERIALIZED VIEW et_dc_space_mv AS SELECT 7 AS i", 1))
		d.run(fixture.Exec("INSERT INTO et_dc_space VALUES(7)", 1))
		d.install("'ALTER TABLE','ALTER INDEX','ALTER MATERIALIZED VIEW'")
		for _, kind := range []string{"TABLE", "INDEX", "MATERIALIZED VIEW"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("ALTER "+kind+" ALL IN TABLESPACE pg_default OWNED BY CURRENT_USER SET TABLESPACE pg_default NOWAIT", 0)
				if !d.b.PG && kind != "TABLE" {
					diagnostic := eventTriggerDiagnostic("42601", `syntax error at or near "ALL"`)
					diagnostic.Position = strconv.Itoa(len("ALTER "+kind+" ") + 1)
					s = fixture.ErrorDiagnostic(s.Query, diagnostic)
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
				}
				s.Prepared = prepared
				d.run(s)
				// Equal source/destination returns before selecting any relation.
				d.rows(nil)
				placement := fixture.Select("SELECT relname,reltablespace FROM pg_class WHERE relname IN ('et_dc_space','et_dc_space_idx','et_dc_space_mv') ORDER BY relname", []string{"relname", "reltablespace"}, []string{"NAME", "OID"}, [][]any{{"et_dc_space", int64(0)}, {"et_dc_space_idx", int64(0)}, {"et_dc_space_mv", int64(0)}})
				d.run(placement)
				d.run(fixture.Prepared(placement))
				data := fixture.Select("SELECT i FROM et_dc_space", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}})
				d.run(data)
				d.run(fixture.Prepared(data))
				data.Query = "SELECT i FROM et_dc_space_mv"
				d.run(data)
				d.run(fixture.Prepared(data))
			}
		}
	})
}

func ddlCommandsCompositeCascade(t *testing.T) {
	ddlCommandScenario(t, func(d ddlCommandCase) {
		d.exec("CREATE TYPE et_dc_composite AS (i integer)", "CREATE TABLE et_dc_typed OF et_dc_composite", "CREATE TABLE et_dc_typed_child() INHERITS(et_dc_typed)")
		d.run(fixture.Exec("INSERT INTO et_dc_typed VALUES(7)", 1))
		d.run(fixture.Exec("INSERT INTO et_dc_typed_child VALUES(8)", 1))
		var oid string
		if err := d.c.QueryRowContext(d.p.Ctx, "SELECT typrelid::text FROM pg_type WHERE oid='et_dc_composite'::regtype").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		d.install("'ALTER TYPE'")
		for _, prepared := range []bool{false, true} {
			d.exec("BEGIN")
			s := fixture.Exec("ALTER TYPE et_dc_composite ADD ATTRIBUTE label text CASCADE", 0)
			s.Prepared = prepared
			d.run(s)
			d.rows(ddlCommandRows("1259", "ALTER TYPE", "composite type", "public", "public.et_dc_composite"))
			address := fixture.Select("SELECT objid::text AS oid FROM et_dc_audit ORDER BY pass", []string{"oid"}, []string{"TEXT"}, [][]any{{oid}, {oid}})
			d.run(address)
			d.run(fixture.Prepared(address))
			d.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_audit a JOIN pg_type t ON a.objid=t.typrelid JOIN pg_class c ON a.objid=c.oid WHERE t.oid='et_dc_composite'::regtype AND c.relkind='c' AND a.classid='pg_class'::regclass", 2))
			shape := fixture.Select("SELECT c.relname,a.attnum,a.atttypid FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid WHERE c.relname IN ('et_dc_composite','et_dc_typed','et_dc_typed_child') AND a.attname='label' ORDER BY c.relname", []string{"relname", "attnum", "atttypid"}, []string{"NAME", "INT2", "OID"}, [][]any{{"et_dc_composite", int64(2), int64(25)}, {"et_dc_typed", int64(2), int64(25)}, {"et_dc_typed_child", int64(2), int64(25)}})
			d.run(shape)
			d.run(fixture.Prepared(shape))
			data := fixture.Select("SELECT i,label FROM et_dc_typed ORDER BY i", []string{"i", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), nil}, {int64(8), nil}})
			d.run(data)
			d.run(fixture.Prepared(data))
			d.clear()
			s = fixture.Exec("ALTER TYPE et_dc_composite RENAME ATTRIBUTE i TO renamed CASCADE", 0)
			s.Prepared = prepared
			d.run(s)
			d.rows([][]any{{int64(1), int64(1259), int64(1), "ALTER TYPE", "composite type column", "public", "public.et_dc_composite.renamed", false, true}, {int64(2), int64(1259), int64(1), "ALTER TYPE", "composite type column", "public", "public.et_dc_composite.renamed", false, true}})
			d.run(address)
			d.run(fixture.Prepared(address))
			d.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_audit a JOIN pg_attribute c ON a.objid=c.attrelid AND a.objsubid=c.attnum JOIN pg_type t ON c.attrelid=t.typrelid WHERE t.oid='et_dc_composite'::regtype AND c.attname='renamed' AND a.classid='pg_class'::regclass", 2))
			renamedShape := fixture.Select("SELECT c.relname,a.attnum,a.atttypid FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid WHERE c.relname IN ('et_dc_composite','et_dc_typed','et_dc_typed_child') AND a.attname='renamed' ORDER BY c.relname", []string{"relname", "attnum", "atttypid"}, []string{"NAME", "INT2", "OID"}, [][]any{{"et_dc_composite", int64(1), int64(23)}, {"et_dc_typed", int64(1), int64(23)}, {"et_dc_typed_child", int64(1), int64(23)}})
			d.run(renamedShape)
			d.run(fixture.Prepared(renamedShape))
			d.run(shape)
			d.run(fixture.Prepared(shape))
			d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_attribute a JOIN pg_class c ON a.attrelid=c.oid WHERE c.relname IN ('et_dc_composite','et_dc_typed','et_dc_typed_child') AND a.attname='i'", 0))
			renamedData := fixture.Select("SELECT renamed,label FROM et_dc_typed ORDER BY renamed", []string{"renamed", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), nil}, {int64(8), nil}})
			d.run(renamedData)
			d.run(fixture.Prepared(renamedData))
			d.exec("ROLLBACK")
		}
	})
}

func ddlCommandsImplicitFamily(t *testing.T) {
	ddlCommandScenario(t, func(d ddlCommandCase) {
		d.install("'CREATE OPERATOR CLASS'")
		for _, prepared := range []bool{false, true} {
			d.exec("BEGIN")
			s := fixture.Exec("CREATE OPERATOR CLASS et_dc_implicit FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)", 0)
			s.Prepared = prepared
			d.run(s)
			want := append(ddlCommandRows("2753", "CREATE OPERATOR FAMILY", "operator family", "public", "public.et_dc_implicit USING btree"), ddlCommandRows("2616", "CREATE OPERATOR CLASS", "operator class", "public", "public.et_dc_implicit USING btree")...)
			d.rows(want)
			d.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_audit a JOIN pg_opfamily f ON a.objid=f.oid JOIN pg_opclass c ON c.opcfamily=f.oid JOIN pg_am m ON f.opfmethod=m.oid WHERE a.classid='pg_opfamily'::regclass AND f.opfname='et_dc_implicit' AND c.opcname='et_dc_implicit' AND c.opcmethod=f.opfmethod AND m.amname='btree'", 2))
			d.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_audit a JOIN pg_opclass c ON a.objid=c.oid JOIN pg_opfamily f ON c.opcfamily=f.oid JOIN pg_am m ON c.opcmethod=m.oid WHERE a.classid='pg_opclass'::regclass AND c.opcname='et_dc_implicit' AND f.opfname='et_dc_implicit' AND c.opcintype='integer'::regtype AND m.amname='btree'", 2))
			d.exec("ROLLBACK")
		}
	})
}

func ddlCommandsTriggerRename(t *testing.T) {
	ddlCommandScenario(t, func(d ddlCommandCase) {
		d.exec("CREATE TABLE et_dc_trigger_base(i integer)", "CREATE FUNCTION et_dc_trig_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", "CREATE TRIGGER et_dc_trig BEFORE INSERT ON et_dc_trigger_base FOR EACH ROW EXECUTE FUNCTION et_dc_trig_fn()")
		var oid string
		if err := d.c.QueryRowContext(d.p.Ctx, "SELECT oid::text FROM pg_trigger WHERE tgrelid='et_dc_trigger_base'::regclass AND tgname='et_dc_trig'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		d.install("'ALTER TRIGGER'")
		for _, prepared := range []bool{false, true} {
			d.exec("BEGIN")
			s := fixture.Exec("ALTER TRIGGER et_dc_trig ON et_dc_trigger_base RENAME TO et_dc_trig_new", 0)
			s.Prepared = prepared
			d.run(s)
			d.rows(ddlCommandRows("2620", "ALTER TRIGGER", "trigger", nil, "et_dc_trig_new on public.et_dc_trigger_base"))
			address := fixture.Select("SELECT objid::text AS oid FROM et_dc_audit ORDER BY pass", []string{"oid"}, []string{"TEXT"}, [][]any{{oid}, {oid}})
			d.run(address)
			d.run(fixture.Prepared(address))
			d.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_audit a JOIN pg_trigger t ON a.objid=t.oid WHERE a.classid='pg_trigger'::regclass AND t.tgname='et_dc_trig_new' AND t.tgrelid='et_dc_trigger_base'::regclass AND t.tgfoid='et_dc_trig_fn()'::regprocedure", 2))
			d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_trigger WHERE tgrelid='et_dc_trigger_base'::regclass AND tgname='et_dc_trig'", 0))
			d.run(fixture.Exec("INSERT INTO et_dc_trigger_base VALUES(7)", 1))
			data := fixture.Select("SELECT i FROM et_dc_trigger_base", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}})
			d.run(data)
			d.run(fixture.Prepared(data))
			d.exec("ROLLBACK")
		}
	})
}

func ddlCommandsPartitionChanges(t *testing.T) {
	ddlCommandScenario(t, func(d ddlCommandCase) {
		d.exec("CREATE TABLE et_dc_partitioned(i integer) PARTITION BY RANGE(i)", "CREATE TABLE et_dc_attached(i integer)")
		d.run(fixture.Exec("INSERT INTO et_dc_attached VALUES(7)", 1))
		var oid string
		if err := d.c.QueryRowContext(d.p.Ctx, "SELECT 'et_dc_partitioned'::regclass::oid::text").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		d.install("'ALTER TABLE'")
		for _, prepared := range []bool{false, true} {
			d.exec("BEGIN")
			for _, attach := range []bool{true, false} {
				query := "ALTER TABLE et_dc_partitioned DETACH PARTITION et_dc_attached"
				if attach {
					query = "ALTER TABLE et_dc_partitioned ATTACH PARTITION et_dc_attached FOR VALUES FROM(0) TO(10)"
				}
				d.clear()
				s := fixture.Exec(query, 0)
				s.Prepared = prepared
				d.run(s)
				d.rows(ddlCommandRows("1259", "ALTER TABLE", "table", "public", "public.et_dc_partitioned"))
				address := fixture.Select("SELECT objid::text AS oid FROM et_dc_audit ORDER BY pass", []string{"oid"}, []string{"TEXT"}, [][]any{{oid}, {oid}})
				d.run(address)
				d.run(fixture.Prepared(address))
				state := fixture.Select("SELECT relispartition FROM pg_class WHERE oid='et_dc_attached'::regclass", []string{"relispartition"}, []string{"BOOL"}, [][]any{{attach}})
				d.run(state)
				d.run(fixture.Prepared(state))
				n := int64(0)
				if attach {
					n = 1
				}
				d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_inherits WHERE inhparent='et_dc_partitioned'::regclass AND inhrelid='et_dc_attached'::regclass", n))
				var want [][]any
				if attach {
					want = [][]any{{int64(7)}}
				}
				data := fixture.Select("SELECT i FROM et_dc_partitioned", []string{"i"}, []string{"INT4"}, want)
				d.run(data)
				d.run(fixture.Prepared(data))
				data.Query = "SELECT i FROM et_dc_attached"
				data.Rows = [][]any{{int64(7)}}
				d.run(data)
				d.run(fixture.Prepared(data))
			}
			d.exec("ROLLBACK")
		}
	})
}

func ddlCommandsSelectRule(t *testing.T) {
	ddlCommandScenario(t, func(d ddlCommandCase) {
		d.exec("CREATE TABLE et_dc_rule_base(i integer)")
		d.run(fixture.Exec("INSERT INTO et_dc_rule_base VALUES(7)", 1))
		d.install("'CREATE VIEW','CREATE RULE'")
		for _, prepared := range []bool{false, true} {
			d.exec("BEGIN")
			s := fixture.Exec("CREATE VIEW et_dc_rule_view AS SELECT i FROM et_dc_rule_base", 0)
			s.Prepared = prepared
			d.run(s)
			d.rows(ddlCommandRows("1259", "CREATE VIEW", "view", "public", "public.et_dc_rule_view"))
			d.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_audit a JOIN pg_class c ON a.objid=c.oid WHERE a.classid='pg_class'::regclass AND c.oid='et_dc_rule_view'::regclass AND c.relkind='v'", 2))
			var oid string
			if err := d.c.QueryRowContext(d.p.Ctx, "SELECT oid::text FROM pg_rewrite WHERE ev_class='et_dc_rule_view'::regclass AND rulename='_RETURN'").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			if oid == "0" {
				t.Fatal("view SELECT rule has invalid OID")
			}
			d.clear()
			s = fixture.Exec("CREATE OR REPLACE RULE \"_RETURN\" AS ON SELECT TO et_dc_rule_view DO INSTEAD SELECT i+10 AS i FROM et_dc_rule_base", 0)
			s.Prepared = prepared
			d.run(s)
			d.rows(ddlCommandRows("2618", "CREATE RULE", "rule", nil, "\"_RETURN\" on public.et_dc_rule_view"))
			address := fixture.Select("SELECT objid::text AS oid FROM et_dc_audit ORDER BY pass", []string{"oid"}, []string{"TEXT"}, [][]any{{oid}, {oid}})
			d.run(address)
			d.run(fixture.Prepared(address))
			d.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_audit a JOIN pg_rewrite r ON a.objid=r.oid JOIN pg_class c ON r.ev_class=c.oid WHERE a.classid='pg_rewrite'::regclass AND r.rulename='_RETURN' AND c.oid='et_dc_rule_view'::regclass AND c.relkind='v'", 2))
			data := fixture.Select("SELECT i FROM et_dc_rule_view", []string{"i"}, []string{"INT4"}, [][]any{{int64(17)}})
			d.run(data)
			d.run(fixture.Prepared(data))
			data = fixture.Select("SELECT i FROM et_dc_rule_base", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}})
			d.run(data)
			d.run(fixture.Prepared(data))
			d.exec("ROLLBACK")
		}
	})
}

func ddlCommandsAlterConstraintIndexes(t *testing.T) {
	for _, tc := range []struct {
		name, definition, kind     string
		primary, unique, exclusion bool
	}{
		{"primary", "PRIMARY KEY(i)", "p", true, true, false},
		{"unique", "UNIQUE(i)", "u", false, true, false},
		{"exclusion", "EXCLUDE USING btree(i WITH =)", "x", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ddlCommandScenario(t, func(d ddlCommandCase) {
				d.exec("CREATE TABLE et_dc_constraint_base(i integer)")
				d.run(fixture.Exec("INSERT INTO et_dc_constraint_base VALUES(7)", 1))
				var oid string
				if err := d.c.QueryRowContext(d.p.Ctx, "SELECT 'et_dc_constraint_base'::regclass::oid::text").Scan(&oid); err != nil {
					t.Fatal(err)
				}
				d.install("'ALTER TABLE','CREATE INDEX'")
				for _, prepared := range []bool{false, true} {
					d.exec("BEGIN")
					s := fixture.Exec("ALTER TABLE et_dc_constraint_base ADD CONSTRAINT et_dc_added "+tc.definition, 0)
					s.Prepared = prepared
					d.run(s)
					d.rows(ddlCommandRows("1259", "ALTER TABLE", "table", "public", "public.et_dc_constraint_base"))
					address := fixture.Select("SELECT objid::text AS oid FROM et_dc_audit ORDER BY pass", []string{"oid"}, []string{"TEXT"}, [][]any{{oid}, {oid}})
					d.run(address)
					d.run(fixture.Prepared(address))
					index := fixture.Select("SELECT c.contype,r.relname,r.relkind,i.indisprimary,i.indisunique,c.contype='x' AS is_exclusion,i.indisvalid FROM pg_constraint c JOIN pg_class r ON c.conindid=r.oid JOIN pg_index i ON i.indexrelid=r.oid AND i.indrelid=c.conrelid WHERE c.conrelid='et_dc_constraint_base'::regclass AND c.conname='et_dc_added'", []string{"contype", "relname", "relkind", "indisprimary", "indisunique", "is_exclusion", "indisvalid"}, []string{"CHAR", "NAME", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{tc.kind, "et_dc_added", "i", tc.primary, tc.unique, tc.exclusion, true}})
					d.run(index)
					d.run(fixture.Prepared(index))
					d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_index WHERE indrelid='et_dc_constraint_base'::regclass", 1))
					d.exec("SAVEPOINT et_dc_duplicate")
					diagnostic := fixture.UniqueReferenceDuplicate("et_dc_constraint_base", "et_dc_added", "Key (i)=(7) already exists.")
					if tc.exclusion {
						diagnostic = fixture.ExclusionReferenceConflict("et_dc_constraint_base", "et_dc_added", "i", "7", "7")
					}
					s = fixture.ErrorDiagnostic("INSERT INTO et_dc_constraint_base VALUES(7)", diagnostic)
					s.Prepared = prepared
					d.run(s)
					d.exec("ROLLBACK TO SAVEPOINT et_dc_duplicate")
					data := fixture.Select("SELECT i FROM et_dc_constraint_base", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}})
					d.run(data)
					d.run(fixture.Prepared(data))
					d.rows(ddlCommandRows("1259", "ALTER TABLE", "table", "public", "public.et_dc_constraint_base"))
					d.exec("ROLLBACK")
				}
			})
		})
	}
}
