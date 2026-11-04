package reference_test

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSepgsqlSimulationBoundary(t *testing.T) {
	t.Run("provider_without_sql_installation", func(t *testing.T) {
		sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			run(fixture.Exec("CREATE TABLE provider_only(v integer)", 0))
			run(sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname IN ('sepgsql_getcon','sepgsql_setcon','sepgsql_restorecon','sepgsql_mcstrans_in','sepgsql_mcstrans_out')", 0))
			run(fixture.ImplicitError("SECURITY LABEL FOR selinux ON TABLE provider_only IS '"+sepgsqlS+"'", fixture.Diagnostic{Code: "22023", Message: "security label provider \"selinux\" is not loaded"}))
		}, func(config *stuntgres.SepgsqlSimulationConfig) { config.InstallSQL = false }, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			run(fixture.Exec("CREATE TABLE provider_only(v integer)", 0))
			run(fixture.Exec("SECURITY LABEL FOR selinux ON TABLE provider_only IS '"+sepgsqlS+"'", 0))
			run(sepgsqlText("SELECT label FROM pg_seclabel WHERE objoid='provider_only'::regclass AND objsubid=0", "label", sepgsqlS))
			run(sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname IN ('sepgsql_getcon','sepgsql_setcon','sepgsql_restorecon','sepgsql_mcstrans_in','sepgsql_mcstrans_out')", 0))
		})
	})
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		sepgsqlViewMetadata(run, false)
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_shseclabel", 0))
		for _, provider := range []string{"selinux", "sepgsql"} {
			run(fixture.ImplicitError("SECURITY LABEL FOR "+provider+" ON TABLE absent_object IS NULL", fixture.Diagnostic{Code: "22023", Message: "security label provider \"" + provider + "\" is not loaded"}))
		}
		run(fixture.ImplicitError("SECURITY LABEL ON TABLE absent_object IS 'x'", fixture.Diagnostic{Code: "22023", Message: "no security label providers have been loaded"}))
	}, nil, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		sepgsqlViewMetadata(run, true)
		run(sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname IN ('sepgsql_getcon','sepgsql_setcon','sepgsql_mcstrans_in','sepgsql_mcstrans_out','sepgsql_restorecon') AND prolang=13 AND provolatile='v' AND proparallel='u'", 5))
		run(sepgsqlCount("SELECT count(*) FROM pg_extension WHERE extname='sepgsql'", 0))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlA))
		run(fixture.ImplicitError("SECURITY LABEL FOR sepgsql ON TABLE absent_object IS NULL", fixture.Diagnostic{Code: "22023", Message: "security label provider \"sepgsql\" is not loaded"}))
		for _, context := range []string{"", "unknown"} {
			db := sql.OpenDB(p.NativeConnector.WithSepgsqlContext(context))
			sepgsqlHostError(t, db.PingContext(p.Ctx))
			db.Close()
		}
		peer := sql.OpenDB(p.NativeConnector.WithSepgsqlContext(sepgsqlR))
		defer peer.Close()
		var actor, got string
		if err := peer.QueryRowContext(p.Ctx, "SELECT current_user,sepgsql_getcon()").Scan(&actor, &got); err != nil || actor != "postgres" || got != sepgsqlR {
			t.Fatal("explicit physical context changed SQL actor", actor, got, err)
		}
	})
}
func TestReferenceSepgsqlSimulationLabels(t *testing.T) {
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		sepgsqlOrdinaryTable(t, p, c, run)
		run(fixture.Exec("COMMENT ON COLUMN sepgsql_items.v IS 'ordinary comment'", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='sepgsql_items'::regclass", 0))
		run(sepgsqlText("SELECT col_description('sepgsql_items'::regclass,2) AS comment", "comment", "ordinary comment"))
		run(fixture.Select("SELECT col_description('sepgsql_items'::regclass,1) AS comment", []string{"comment"}, []string{"TEXT"}, [][]any{{nil}}))
		run(fixture.Exec("ALTER TABLE sepgsql_items RENAME COLUMN v TO current_name", 0))
		run(sepgsqlText("SELECT attname::text AS name FROM pg_attribute WHERE attrelid='sepgsql_items'::regclass AND attnum=2", "name", "current_name"))
		run(sepgsqlText("SELECT col_description('sepgsql_items'::regclass,2) AS comment", "comment", "ordinary comment"))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='sepgsql_items'::regclass", 0))
	}, nil, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		sepgsqlTable(run)
		for _, target := range []string{"TABLE sepgsql_items", "COLUMN sepgsql_items.v", "COLUMN sepgsql_items.tableoid"} {
			run(fixture.Exec("SECURITY LABEL FOR selinux ON "+target+" IS '"+sepgsqlS+"'", 0))
		}
		run(fixture.Select("SELECT objsubid,label FROM pg_seclabel WHERE objoid='sepgsql_items'::regclass AND provider='selinux' AND label='"+sepgsqlS+"' ORDER BY objsubid", []string{"objsubid", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(-6), sepgsqlS}, {int64(0), sepgsqlS}, {int64(2), sepgsqlS}}))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabels WHERE objoid='sepgsql_items'::regclass AND provider='selinux' AND label='"+sepgsqlS+"'", 3))
		run(fixture.ImplicitError("SECURITY LABEL ON TABLE sepgsql_items IS 'invalid'", fixture.Diagnostic{Code: "42602", Message: "SELinux: invalid security label: \"invalid\""}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT before_label", 0))
		run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_items IS NULL", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='sepgsql_items'::regclass AND objsubid=0", 0))
		run(fixture.Exec("ROLLBACK TO before_label", 0))
		run(fixture.Exec("COMMIT", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='sepgsql_items'::regclass AND objsubid=0 AND label='"+sepgsqlS+"'", 1))
		run(fixture.Exec("ALTER TABLE sepgsql_items RENAME COLUMN v TO renamed", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='sepgsql_items'::regclass AND objsubid=2 AND label='"+sepgsqlS+"'", 1))
		run(fixture.Exec("ALTER TABLE sepgsql_items DROP COLUMN renamed", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='sepgsql_items'::regclass AND objsubid=2", 0))
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlDenied("SECURITY LABEL ON TABLE sepgsql_items IS '" + sepgsqlP + "'"))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(fixture.Exec("DROP TABLE sepgsql_items", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabels WHERE objname='sepgsql_items'", 0))
	})
}
func TestReferenceSepgsqlSimulationAccess(t *testing.T) {
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		sepgsqlOrdinaryTable(t, p, c, run)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("UPDATE sepgsql_items SET v='changed' WHERE id=1", 1))
		run(fixture.Exec("ROLLBACK", 0))
		run(sepgsqlCount("SELECT count(*) FROM sepgsql_items WHERE v='one'", 1))
		run(fixture.Exec("CREATE VIEW sepgsql_visible AS SELECT id FROM sepgsql_items", 0))
		run(sepgsqlCount("SELECT count(*) FROM sepgsql_visible", 2))
	}, nil, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		sepgsqlTable(run)
		run(fixture.Exec("CREATE VIEW sepgsql_visible AS SELECT id FROM sepgsql_items", 0))
		held, err := c.PrepareContext(p.Ctx, "SELECT id FROM sepgsql_items ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlCount("SELECT count(*) FROM sepgsql_items", 2))
		run(sepgsqlCount("SELECT count(*) FROM sepgsql_visible", 2))
		run(fixture.Select("SELECT id FROM sepgsql_items ORDER BY id FOR SHARE", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		for _, query := range []string{"INSERT INTO sepgsql_items VALUES(3,'three')", "UPDATE sepgsql_items SET v='bad' WHERE id=1", "DELETE FROM sepgsql_items WHERE id=1", "TRUNCATE sepgsql_items"} {
			run(sepgsqlDenied(query))
		}
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(fixture.Exec("SECURITY LABEL ON COLUMN sepgsql_items.v IS '"+sepgsqlS+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlCount("SELECT count(*) FROM sepgsql_items", 2))
		run(sepgsqlDenied("SELECT v FROM sepgsql_items WHERE false"))
		run(sepgsqlCount("SELECT count(*) FROM sepgsql_visible", 2))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_items IS '"+sepgsqlS+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlDenied("SELECT id FROM sepgsql_items"))
		run(sepgsqlDenied("SELECT id FROM sepgsql_visible"))
		rows, err := held.QueryContext(p.Ctx)
		if rows != nil {
			rows.Close()
		}
		var state interface{ SQLState() string }
		if !errors.As(err, &state) || state.SQLState() != "42501" {
			t.Fatal("held statement reused previous allow", err)
		}
	})
}
func TestReferenceSepgsqlSimulationDDL(t *testing.T) {
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		sepgsqlOrdinaryTable(t, p, c, run)
		run(fixture.Exec("CREATE INDEX sepgsql_index ON sepgsql_items(id)", 0))
		run(fixture.Exec("ALTER TABLE sepgsql_items ADD COLUMN extra integer", 0))
		run(fixture.Exec("ALTER TABLE sepgsql_items RENAME TO sepgsql_renamed", 0))
		run(sepgsqlCount("SELECT count(*) FROM sepgsql_renamed", 2))
		run(fixture.Exec("DROP TABLE sepgsql_renamed", 0))
	}, func(config *stuntgres.SepgsqlSimulationConfig) {
		config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_schema", Allow: []string{"drop", "remove_name"}}, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_table", Allow: []string{"drop"}}, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_column", Allow: []string{"drop"}})
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(fixture.Exec("CREATE SCHEMA sepgsql_cascade", 0))
		run(fixture.Exec("CREATE TABLE sepgsql_cascade.first_table(id integer)", 0))
		run(fixture.Exec("CREATE TABLE sepgsql_cascade.last_table(id integer)", 0))
		run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_cascade.last_table IS '"+sepgsqlS+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlDenied("DROP SCHEMA sepgsql_cascade CASCADE"))
		run(sepgsqlCount("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='sepgsql_cascade' AND c.relkind='r'", 2))
		run(sepgsqlDenied("CREATE TABLE should_not_publish(id integer)"))
		run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname='should_not_publish'", 0))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		sepgsqlTable(run)
		run(fixture.Exec("CREATE INDEX sepgsql_index ON sepgsql_items(id)", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='sepgsql_index'::regclass", 0))
		run(fixture.ImplicitError("LOAD '$libdir/sepgsql'", fixture.Diagnostic{Code: "42501", Message: "SELinux: LOAD is not permitted"}))
	})
}

// Ordinary expectations run independently on inactive native and pinned PostgreSQL.
// Active expectations come from REL_18_6 relation.c/DefineSequence, not an active
// PostgreSQL provider or explicit fixture relabel of the newly created sequence.
func TestReferenceSepgsqlSimulationSequenceCreate(t *testing.T) {
	t.Run("creation_and_lifetime", func(t *testing.T) {
		body := func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun, active bool) {
			sepgsqlSequenceCreation(t, p, b, c, run, active)
			sepgsqlSequenceLifetime(t, p, b, c, run, active)
		}
		sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			body(t, p, b, c, run, false)
		}, sepgsqlSequenceConfig, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			body(t, p, b, c, run, true)
		})
	})
	for _, permissive := range []bool{false, true} {
		t.Run(fmt.Sprintf("denial_permissive=%t", permissive), func(t *testing.T) {
			var mu sync.Mutex
			var events []stuntgres.SepgsqlAuditEvent
			fixture.RoleReferenceCleanup(t, []string{"sepgsql_sq_guest"})
			sepgsqlCase(t, sepgsqlSequenceOrdinaryDenials, func(config *stuntgres.SepgsqlSimulationConfig) {
				sepgsqlSequenceConfig(config)
				config.Permissive = permissive
				config.AuditAllowed = true
				config.Audit = func(event stuntgres.SepgsqlAuditEvent) {
					mu.Lock()
					events = append(events, event)
					mu.Unlock()
				}
			}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
				snapshot := func() []stuntgres.SepgsqlAuditEvent {
					mu.Lock()
					defer mu.Unlock()
					return append([]stuntgres.SepgsqlAuditEvent(nil), events...)
				}
				sepgsqlSequenceDenials(t, p, b, c, run, permissive, snapshot)
			})
		})
	}
}

func sepgsqlSequenceConfig(config *stuntgres.SepgsqlSimulationConfig) {
	for _, name := range []string{"sepgsql_sq_selected", "sepgsql_sq_denied", "sepgsql_sq_badowner", "sepgsql_sq_temp_denied", "sepgsql_sq_serialfail_id_seq", "sepgsql_sq_identityfail_id_seq", "sepgsql_sq_addfail_id_seq", "Sequence Denied"} {
		config.Policy.CreateTransitions = append(config.Policy.CreateTransitions,
			stuntgres.SepgsqlCreateTransition{Source: "*", Parent: sepgsqlP, Class: "db_sequence", Name: name, Result: sepgsqlS})
	}
	// P is inherited; S is the independently selected Q label. R may create P,
	// but not Q. Table/column permissions let a genuine owned-sequence denial
	// decide SERIAL/identity, rather than an earlier unrelated table denial.
	config.Policy.Rules = append(config.Policy.Rules,
		stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_schema", Allow: []string{"create", "add_name"}},
		stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_sequence", Allow: []string{"create", "get_value"}},
		stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_table", Allow: []string{"create"}},
		stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_column", Allow: []string{"create"}},
		stuntgres.SepgsqlRule{Source: sepgsqlT, Target: "*", Class: "process", Allow: []string{"setcurrent", "dyntransition"}})
}

func sepgsqlSequenceState(run sepgsqlRun, name string, last int64, called bool) {
	run(fixture.Select("SELECT last_value,log_cnt,is_called FROM "+name,
		[]string{"last_value", "log_cnt", "is_called"}, []string{"INT8", "INT8", "BOOL"},
		[][]any{{last, int64(0), called}}))
}

func sepgsqlSequenceAssociation(run sepgsqlRun, name, label string, active bool) {
	want := int64(0)
	if active {
		want = 1
	}
	run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE classoid='pg_class'::regclass AND objoid='"+name+"'::regclass AND objsubid=0 AND provider='selinux' AND label='"+label+"'", want))
	run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE classoid='pg_class'::regclass AND objoid='"+name+"'::regclass AND objsubid<>0", 0))
	if active {
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='"+name+"'::regclass", 1))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabels WHERE objoid='"+name+"'::regclass AND objsubid=0 AND objtype='sequence' AND objnamespace='public'::regnamespace AND provider='selinux' AND label='"+label+"'", 1))
	}
}

func sepgsqlSequenceCreation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun, active bool) {
	t.Helper()
	sepgsqlViewMetadata(run, active)
	raw := fixture.Select("SELECT * FROM pg_seclabel WHERE false", []string{"objoid", "classoid", "objsubid", "provider", "label"}, []string{"OID", "OID", "INT4", "TEXT", "TEXT"}, nil)
	raw.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 5 {
			t.Fatal(types, err)
		}
		for _, column := range types {
			nullable, known := column.Nullable()
			if active && !known || known && nullable {
				t.Fatal("raw security-label columns must be nonnullable", column.Name(), nullable, known)
			}
		}
	}
	run(raw)
	run(fixture.Prepared(raw))
	for _, mode := range []string{"direct", "prepared", "held"} {
		name := "sepgsql_sq_" + mode
		q := "CREATE SEQUENCE " + name + " START 5 RESTART WITH 9 CACHE 3 MAXVALUE 99"
		step := fixture.Exec(q, 0)
		if mode == "held" {
			held, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("analysis-only CREATE preparation", err)
			}
			defer held.Close()
			_, err = held.ExecContext(p.Ctx, 1)
			fixture.TransactionOrdinary(t, err, "sql: expected 0 arguments, got 1")
			run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname='"+name+"'", 0))
			executor := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
			run(fixture.Exec("DROP SEQUENCE "+name, 0))
			// The same retained handle must perform a fresh creation.
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
			run(fixture.Exec("DROP SEQUENCE "+name, 0))
			rows, err := held.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal("same held utility Query", err)
			}
			defer rows.Close()
			columns, columnErr := rows.Columns()
			types, typeErr := rows.ColumnTypes()
			if columnErr != nil || typeErr != nil || len(columns) != 0 || len(types) != 0 || rows.Next() || rows.Err() != nil {
				t.Fatal("CREATE must return no result columns or rows", columns, types, columnErr, typeErr, rows.Err())
			}
			if err = rows.Close(); err != nil {
				t.Fatal(err)
			}
		} else {
			step.Prepared = mode == "prepared"
			run(step)
		}
		sepgsqlSequenceState(run, name, 9, false)
		run(sepgsqlCount("SELECT count(*) FROM pg_sequence WHERE seqrelid='"+name+"'::regclass AND seqtypid='int8'::regtype AND seqstart=5 AND seqincrement=1 AND seqmin=1 AND seqmax=99 AND seqcache=3 AND NOT seqcycle", 1))
		run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE oid='"+name+"'::regclass AND relkind='S' AND relnamespace='public'::regnamespace AND relowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)", 1))
		run(sepgsqlCount("SELECT count(*) FROM pg_attribute WHERE attrelid='"+name+"'::regclass AND attnum>0 AND NOT attisdropped", 3))
		sepgsqlSequenceAssociation(run, name, sepgsqlP, active)
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT last_value,log_cnt,is_called FROM "+name, []string{"last_value", "log_cnt", "is_called"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{int64(9), int64(0), false}})
			s.Prepared = prepared
			run(s)
		}
	}
	run(fixture.Exec("CREATE SEQUENCE sepgsql_sq_selected", 0))
	sepgsqlSequenceAssociation(run, "sepgsql_sq_selected", sepgsqlS, active)
	run(fixture.Exec("CREATE UNLOGGED SEQUENCE sepgsql_sq_unlogged START 5", 0))
	sepgsqlSequenceAssociation(run, "sepgsql_sq_unlogged", sepgsqlP, active)
	run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE oid='sepgsql_sq_unlogged'::regclass AND relpersistence='u'", 1))
	if !active {
		run(fixture.ImplicitError("SECURITY LABEL FOR selinux ON SEQUENCE sepgsql_sq_selected IS NULL", fixture.Diagnostic{Code: "22023", Message: `security label provider "selinux" is not loaded`}))
	}
}

func sepgsqlSequenceLifetime(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun, active bool) {
	t.Helper()
	peer, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peerRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("CREATE SEQUENCE sepgsql_sq_rollback START 17", 0))
	sepgsqlSequenceAssociation(run, "sepgsql_sq_rollback", sepgsqlP, active)
	peerRun(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname='sepgsql_sq_rollback'", 0))
	peerRun(sepgsqlCount("SELECT count(*) FROM pg_seclabels WHERE objname='sepgsql_sq_rollback'", 0))
	run(fixture.Exec("ROLLBACK", 0))
	run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname='sepgsql_sq_rollback'", 0))
	run(sepgsqlCount("SELECT count(*) FROM pg_seclabels WHERE objname='sepgsql_sq_rollback'", 0))
	run(fixture.Exec("CREATE SEQUENCE sepgsql_sq_rollback START 47", 0))
	sepgsqlSequenceState(run, "sepgsql_sq_rollback", 47, false)
	run(fixture.Exec("DROP SEQUENCE sepgsql_sq_rollback", 0))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("SAVEPOINT sequence_before", 0))
	run(fixture.Exec("CREATE SEQUENCE sepgsql_sq_savepoint", 0))
	sepgsqlSequenceAssociation(run, "sepgsql_sq_savepoint", sepgsqlP, active)
	run(fixture.Exec("ROLLBACK TO sequence_before", 0))
	run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname='sepgsql_sq_savepoint'", 0))
	run(sepgsqlCount("SELECT count(*) FROM pg_seclabels WHERE objname='sepgsql_sq_savepoint'", 0))
	run(fixture.Exec("CREATE SEQUENCE sepgsql_sq_savepoint START 23", 0))
	run(fixture.Exec("RELEASE sequence_before", 0))
	run(fixture.Exec("COMMIT", 0))
	sepgsqlSequenceAssociation(peerRun, "sepgsql_sq_savepoint", sepgsqlP, active)
	sepgsqlSequenceState(peerRun, "sepgsql_sq_savepoint", 23, false)
	// First lazy TEMP namespace, then an existing real TEMP namespace.
	run(fixture.Exec("CREATE TEMP SEQUENCE sepgsql_sq_temp_first START 31", 0))
	run(fixture.Exec("CREATE TEMP SEQUENCE sepgsql_sq_temp_second START 37", 0))
	peerRun(fixture.Exec("CREATE TEMP SEQUENCE sepgsql_sq_peer START 43", 0))
	for _, name := range []string{"sepgsql_sq_temp_first", "sepgsql_sq_temp_second"} {
		want := int64(0)
		if active {
			want = 1
		}
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='"+name+"'::regclass AND objsubid=0 AND provider='selinux' AND label='"+sepgsqlP+"'", want))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='"+name+"'::regclass AND objsubid<>0", 0))
	}
	run(fixture.Exec("DISCARD TEMP", 0))
	run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname IN ('sepgsql_sq_temp_first','sepgsql_sq_temp_second')", 0))
	run(sepgsqlCount("SELECT count(*) FROM pg_seclabels WHERE objname IN ('sepgsql_sq_temp_first','sepgsql_sq_temp_second')", 0))
	sepgsqlSequenceState(peerRun, "sepgsql_sq_savepoint", 23, false)
	sepgsqlSequenceState(peerRun, "sepgsql_sq_peer", 43, false)
	if active {
		peerRun(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='sepgsql_sq_peer'::regclass AND objsubid=0 AND label='"+sepgsqlP+"'", 1))
	}
	for _, q := range []string{
		"CREATE TABLE sepgsql_sq_serial(id serial)",
		"CREATE TABLE sepgsql_sq_identity(id bigint GENERATED ALWAYS AS IDENTITY)",
		"CREATE TABLE sepgsql_sq_add(id bigint NOT NULL)",
		"ALTER TABLE sepgsql_sq_add ALTER id ADD GENERATED BY DEFAULT AS IDENTITY",
	} {
		run(fixture.Exec(q, 0))
	}
	for _, table := range []string{"sepgsql_sq_serial", "sepgsql_sq_identity", "sepgsql_sq_add"} {
		name := table + "_id_seq"
		sepgsqlSequenceAssociation(run, name, sepgsqlP, active)
		run(sepgsqlText("SELECT pg_get_serial_sequence('"+table+"','id') AS owned", "owned", "public."+name))
		run(sepgsqlCount("SELECT count(*) FROM pg_depend WHERE classid='pg_class'::regclass AND objid='"+name+"'::regclass AND refclassid='pg_class'::regclass AND refobjid='"+table+"'::regclass AND refobjsubid=1 AND deptype IN ('a','i')", 1))
		run(fixture.Exec("INSERT INTO "+table+" DEFAULT VALUES", 1))
		run(fixture.Select("SELECT id FROM "+table, []string{"id"}, []string{map[bool]string{true: "INT4", false: "INT8"}[table == "sepgsql_sq_serial"]}, [][]any{{int64(1)}}))
	}
	run(fixture.Exec("CREATE TABLE sepgsql_sq_owner(id integer)", 0))
	run(fixture.Exec("CREATE SEQUENCE sepgsql_sq_owned START 59 OWNED BY sepgsql_sq_owner.id", 0))
	sepgsqlSequenceAssociation(run, "sepgsql_sq_owned", sepgsqlP, active)
	run(sepgsqlText("SELECT pg_get_serial_sequence('sepgsql_sq_owner','id') AS owned", "owned", "public.sepgsql_sq_owned"))
	run(sepgsqlCount("SELECT count(*) FROM pg_depend WHERE classid='pg_class'::regclass AND objid='sepgsql_sq_owned'::regclass AND refclassid='pg_class'::regclass AND refobjid='sepgsql_sq_owner'::regclass AND refobjsubid=1 AND deptype='a'", 1))
	run(fixture.Exec("DROP TABLE sepgsql_sq_owner", 0))
	run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname='sepgsql_sq_owned'", 0))
	run(sepgsqlCount("SELECT count(*) FROM pg_seclabels WHERE objname='sepgsql_sq_owned'", 0))
}

func sepgsqlSequenceOrdinaryDenials(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
	fixture.SchemaReferenceCleanup(t, "sepgsql_sq_private")
	run(fixture.Exec("CREATE SCHEMA sepgsql_sq_private", 0))
	run(fixture.Exec("CREATE SEQUENCE sepgsql_sq_private.existing START 19", 0))
	run(fixture.Exec("CREATE ROLE sepgsql_sq_guest", 0))
	run(fixture.Exec("REVOKE ALL ON SCHEMA sepgsql_sq_private FROM PUBLIC", 0))
	run(fixture.Exec("SET ROLE sepgsql_sq_guest", 0))
	for _, tc := range []struct{ q, code, message string }{
		{"CREATE SEQUENCE sepgsql_sq_private.denied", "42501", "permission denied for schema sepgsql_sq_private"},
		{"CREATE SEQUENCE sepgsql_sq_private.denied INCREMENT 0", "22023", "INCREMENT must not be zero"},
		{"CREATE SEQUENCE IF NOT EXISTS sepgsql_sq_private.existing INCREMENT 0", "42501", "permission denied for schema sepgsql_sq_private"},
	} {
		step := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
		step.ErrorStage = "execute"
		run(step)
		run(fixture.Prepared(step))
	}
	run(fixture.Exec("RESET ROLE", 0))
	sepgsqlSequenceSkip(t, p, b, false, nil)
	run(fixture.ImplicitError("CREATE SEQUENCE sepgsql_sq_missing INCREMENT 0", fixture.Diagnostic{Code: "22023", Message: "INCREMENT must not be zero"}))
	run(fixture.ImplicitError("CREATE SEQUENCE sepgsql_sq_badowner OWNED BY sepgsql_sq_absent.id", fixture.Diagnostic{Code: "42P01", Message: `relation "sepgsql_sq_absent" does not exist`}))
	run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname IN ('denied','sepgsql_sq_missing','sepgsql_sq_badowner')", 0))
}

func sepgsqlSequenceSkip(t *testing.T, p *fixture.Pair, b fixture.Backend, active bool, events func() []stuntgres.SepgsqlAuditEvent) {
	t.Helper()
	var notices []fixture.Diagnostic
	_, c, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, record, s) }
	run(fixture.Exec("CREATE SEQUENCE sepgsql_sq_skip START 41", 0))
	before := 0
	if events != nil {
		before = len(events())
	}
	for _, prepared := range []bool{false, true} {
		s := fixture.Exec("CREATE SEQUENCE IF NOT EXISTS sepgsql_sq_skip INCREMENT 0", 0)
		s.Prepared = prepared
		start := 0
		if events != nil {
			start = len(events())
		}
		run(s)
		if events != nil {
			for _, event := range events()[start:] {
				if event.Class == "db_sequence" {
					t.Fatal("IF NOT EXISTS emitted a sequence MAC event", event)
				}
			}
		}
		want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "sepgsql_sq_skip" already exists, skipping`}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatal("genuine IF NOT EXISTS notice", notices, want)
		}
		notices = nil
		sepgsqlSequenceState(run, "sepgsql_sq_skip", 41, false)
		sepgsqlSequenceAssociation(run, "sepgsql_sq_skip", sepgsqlP, active)
	}
	run(fixture.ImplicitError("CREATE SEQUENCE sepgsql_sq_skip", fixture.Diagnostic{Code: "42P07", Message: `relation "sepgsql_sq_skip" already exists`}))
	if events != nil {
		for _, event := range events()[before:] {
			if event.Class == "db_sequence" && reflect.DeepEqual(event.Required, []string{"create"}) {
				t.Fatal("existing-name skip emitted a sequence create event", event)
			}
		}
	}
}

func sepgsqlSequenceDenials(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun, permissive bool, events func() []stuntgres.SepgsqlAuditEvent) {
	t.Helper()
	run(fixture.Exec("CREATE SCHEMA sepgsql_sq_private", 0))
	run(fixture.Exec("CREATE SEQUENCE sepgsql_sq_private.existing START 19", 0))
	run(fixture.Exec("CREATE ROLE sepgsql_sq_guest", 0))
	run(fixture.Exec("REVOKE ALL ON SCHEMA sepgsql_sq_private FROM PUBLIC", 0))
	beforeACL := len(events())
	run(fixture.Exec("SET ROLE sepgsql_sq_guest", 0))
	for _, q := range []string{"CREATE SEQUENCE sepgsql_sq_private.denied", "CREATE SEQUENCE IF NOT EXISTS sepgsql_sq_private.existing INCREMENT 0"} {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema sepgsql_sq_private"})
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Prepared(s))
	}
	run(fixture.Exec("RESET ROLE", 0))
	for _, event := range events()[beforeACL:] {
		if event.Class == "db_sequence" {
			t.Fatal("ordinary SQL denial reached sequence hook", event)
		}
	}
	sepgsqlSequenceSkip(t, p, b, true, events)
	run(fixture.Exec("CREATE TABLE sepgsql_sq_addfail(id bigint NOT NULL)", 0))
	run(fixture.Exec("CREATE TABLE sepgsql_sq_guard(v integer)", 0))
	run(fixture.Exec("INSERT INTO sepgsql_sq_guard VALUES(71)", 1))
	original := fixture.ExtensionDump(t, p.Ctx, p.Native)
	run(sepgsqlSet(sepgsqlT))
	start := len(events())
	if permissive {
		run(fixture.Exec(`CREATE SEQUENCE "sepgsql_sq_private"."Schema Denied"`, 0))
	} else {
		run(sepgsqlDenied(`CREATE SEQUENCE "sepgsql_sq_private"."Schema Denied"`))
	}
	schemaDenied := false
	for _, event := range events()[start:] {
		if event.Subject == sepgsqlT && event.Class == "db_schema" && reflect.DeepEqual(event.Denied, []string{"add_name"}) {
			schemaDenied = true
			if event.Target != sepgsqlP || event.Permissive != permissive || !strings.Contains(event.Name, "sepgsql_sq_private") {
				t.Fatal("schema denial target/phase", event)
			}
		}
		if !permissive && event.Class == "db_sequence" {
			t.Fatal("schema denial did not precede sequence create", event)
		}
	}
	if !schemaDenied {
		t.Fatal("missing actual schema add_name denial", events()[start:])
	}
	run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
	run(sepgsqlSet(sepgsqlR))
	start = len(events())
	query := "CREATE SEQUENCE sepgsql_sq_denied START 5 RESTART 9 CACHE 3"
	held, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal("Prepare must not create or consume MAC", err)
	}
	defer held.Close()
	run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname='sepgsql_sq_denied'", 0))
	for _, event := range events()[start:] {
		if event.Class == "db_sequence" {
			t.Fatal("Prepare consumed sequence create policy", event)
		}
	}
	for _, executor := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}} {
		step := sepgsqlDenied(query)
		step.ErrorStage = "execute"
		if permissive {
			step = fixture.Exec(query, 0)
		}
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
		if permissive {
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			sepgsqlSequenceAssociation(run, "sepgsql_sq_denied", sepgsqlS, true)
			sepgsqlSequenceState(run, "sepgsql_sq_denied", 9, false)
			run(fixture.Exec("DROP SEQUENCE sepgsql_sq_denied", 0))
			run(sepgsqlSet(sepgsqlR))
		}
	}
	step := sepgsqlDenied(query)
	step.Prepared = true
	if permissive {
		step = fixture.Prepared(fixture.Exec(query, 0))
	}
	run(step)
	denied := 0
	for _, event := range events()[start:] {
		if event.Subject == sepgsqlR && event.Class == "db_sequence" && reflect.DeepEqual(event.Required, []string{"create"}) {
			denied++
			if event.Target != sepgsqlS || !reflect.DeepEqual(event.Denied, []string{"create"}) || event.Permissive != permissive || !strings.Contains(event.Name, "sepgsql_sq_denied") {
				t.Fatal("sequence create concealed actual denied mask/label", event)
			}
		}
	}
	if denied != 3 {
		t.Fatal("direct/prepared/held actual sequence create denials", denied, events()[start:])
	}
	if !permissive {
		start := len(events())
		run(sepgsqlDenied(`CREATE SEQUENCE "sepgsql_sq_private"."Sequence Denied"`))
		quoted := false
		for _, event := range events()[start:] {
			if event.Class == "db_sequence" {
				quoted = true
				if event.Subject != sepgsqlR || event.Target != sepgsqlS || !reflect.DeepEqual(event.Denied, []string{"create"}) || !strings.HasSuffix(event.Name, `.sepgsql_sq_private."Sequence Denied"`) {
					t.Fatal("quoted sequence actual audit identity", event)
				}
			}
		}
		if !quoted {
			t.Fatal("missing quoted sequence denial", events()[start:])
		}
		run(sepgsqlCount("SELECT count(*) FROM pg_namespace WHERE oid=pg_my_temp_schema()", 0))
		run(sepgsqlDenied("CREATE TEMP SEQUENCE sepgsql_sq_temp_denied"))
		run(fixture.Prepared(sepgsqlDenied("CREATE TEMP SEQUENCE sepgsql_sq_temp_denied")))
		run(sepgsqlCount("SELECT count(*) FROM pg_namespace WHERE oid=pg_my_temp_schema()", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname='sepgsql_sq_temp_denied'", 0))
		// Reuse the denied handle under a genuinely different current subject.
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, b.Recorder, fixture.Exec(query, 0))
		sepgsqlSequenceAssociation(run, "sepgsql_sq_denied", sepgsqlS, true)
		run(fixture.Exec("DROP SEQUENCE sepgsql_sq_denied", 0))
		run(sepgsqlSet(sepgsqlR))
		for _, q := range []string{
			"CREATE SEQUENCE sepgsql_sq_badowner OWNED BY sepgsql_sq_absent.id",
			"CREATE TABLE sepgsql_sq_serialfail(id serial)",
			"CREATE TABLE sepgsql_sq_identityfail(id bigint GENERATED ALWAYS AS IDENTITY)",
			"ALTER TABLE sepgsql_sq_addfail ALTER id ADD GENERATED BY DEFAULT AS IDENTITY",
		} {
			run(sepgsqlDenied(q))
			run(fixture.Prepared(sepgsqlDenied(q)))
		}
	}
	run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
	run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlA))
	run(sepgsqlBool("SELECT current_user=session_user AS unchanged", "unchanged", true))
	run(sepgsqlCount("SELECT count(*) FROM pg_roles WHERE rolname='sepgsql_sq_guest' AND NOT rolsuper AND NOT rolcreaterole AND NOT rolcanlogin", 1))
	run(fixture.Select("SELECT v FROM sepgsql_sq_guard", []string{"v"}, []string{"INT4"}, [][]any{{int64(71)}}))
	if !permissive {
		if after := fixture.ExtensionDump(t, p.Ctx, p.Native); after != original {
			t.Fatal("denial published partial descriptors, labels, defaults, links, generators or roles")
		}
		run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname IN ('sepgsql_sq_denied','sepgsql_sq_badowner','sepgsql_sq_serialfail','sepgsql_sq_serialfail_id_seq','sepgsql_sq_identityfail','sepgsql_sq_identityfail_id_seq','sepgsql_sq_addfail_id_seq','Schema Denied')", 0))
		run(sepgsqlText("SELECT attidentity::text AS identity FROM pg_attribute WHERE attrelid='sepgsql_sq_addfail'::regclass AND attname='id'", "identity", ""))
		run(fixture.Select("SELECT pg_get_serial_sequence('sepgsql_sq_addfail','id') AS owned", []string{"owned"}, []string{"TEXT"}, [][]any{{nil}}))
	}
	run(fixture.ImplicitError("CREATE SEQUENCE sepgsql_sq_badowner OWNED BY sepgsql_sq_absent.id", fixture.Diagnostic{Code: "42P01", Message: `relation "sepgsql_sq_absent" does not exist`}))
	run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname='sepgsql_sq_badowner'", 0))
	if !permissive && fixture.ExtensionDump(t, p.Ctx, p.Native) != original {
		t.Fatal("allowed postcreate followed by missing OWNED BY published partial effects")
	}
}
