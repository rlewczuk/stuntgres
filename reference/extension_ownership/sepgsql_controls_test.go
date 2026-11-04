package reference_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
)

func TestReferenceSepgsqlSimulationAudit(t *testing.T) {
	var mu sync.Mutex
	var events []stuntgres.SepgsqlAuditEvent
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(fixture.Exec("SET client_min_messages=warning", 0))
		run(sepgsqlText("SELECT current_setting('client_min_messages') AS setting", "setting", "warning"))
		sepgsqlOrdinaryTable(t, p, c, run)
	}, func(config *stuntgres.SepgsqlSimulationConfig) {
		config.Permissive = true
		config.Audit = func(event stuntgres.SepgsqlAuditEvent) { mu.Lock(); events = append(events, event); mu.Unlock() }
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		sepgsqlTable(run)
		run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_items IS '"+sepgsqlS+"'", 0))
		run(fixture.Exec("SET client_min_messages=error", 0))
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlCount("SELECT count(*) FROM sepgsql_items", 2))
		mu.Lock()
		captured := append([]stuntgres.SepgsqlAuditEvent(nil), events...)
		mu.Unlock()
		found := false
		for _, event := range captured {
			if event.Subject == sepgsqlR && event.Target == sepgsqlS && event.Class == "db_table" {
				found = true
				if !event.Permissive || len(event.Denied) != 1 || event.Denied[0] != "select" || !strings.Contains(event.Message, "SELinux: denied { select }") || !strings.HasSuffix(event.Message, "permissive=1") {
					t.Fatal("permissive concealed denial", event)
				}
			}
		}
		if !found {
			t.Fatal("client filtering suppressed detached host audit", captured)
		}
		run(fixture.Exec("SET sepgsql.debug_audit=on", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname='sepgsql_items'", 1))
		mu.Lock()
		captured = append([]stuntgres.SepgsqlAuditEvent(nil), events...)
		mu.Unlock()
		allowed := false
		for _, event := range captured {
			if event.Subject == sepgsqlR && event.Target == sepgsqlP && len(event.Denied) == 0 && len(event.Audited) > 0 {
				allowed = true
			}
		}
		if !allowed {
			t.Fatal("debug omitted allowed audit", captured)
		}
		sepgsqlAuditClient(t, p)
	})
}

type sepgsqlControlReader struct {
	source io.Reader
	before func()
	once   sync.Once
}

func (r *sepgsqlControlReader) Read(buffer []byte) (int, error) {
	r.once.Do(r.before)
	return r.source.Read(buffer)
}

func sepgsqlStatisticsPayload(t *testing.T, script string) string {
	t.Helper()
	const prefix = "-- stuntgres-attribute-statistics-v1 "
	for _, line := range strings.Split(script, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, prefix))
		if err != nil {
			t.Fatal(err)
		}
		var record struct {
			Relation, Column string
			Rows             uint64
			MCV              []struct {
				Value json.RawMessage `json:"value"`
				Count uint64          `json:"count"`
			}
		}
		if err = json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		if record.Relation != "sepgsql_statistics" || record.Column != "value" {
			continue
		}
		counts := map[uint64]int{}
		for _, entry := range record.MCV {
			if len(entry.Value) == 0 {
				t.Fatal("missing actual constructor")
			}
			counts[entry.Count]++
		}
		if record.Rows != 5 || len(record.MCV) != 2 || counts[3] != 1 || counts[2] != 1 {
			t.Fatalf("independent full-sample statistics: %#v", record)
		}
		return string(data)
	}
	t.Fatal("dump omitted actual hstore MCV snapshot")
	return ""
}

func TestReferenceSepgsqlSimulationControls(t *testing.T) {
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		sepgsqlOrdinaryTable(t, p, c, run)
		held, err := c.PrepareContext(p.Ctx, "SELECT count(*) FROM sepgsql_items")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		var count int64
		for range 2 {
			if err = held.QueryRowContext(p.Ctx).Scan(&count); err != nil || count != 2 {
				t.Fatal("held ordinary query", count, err)
			}
		}
	}, nil, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		sepgsqlTable(run)
		sepgsqlSequenceControlSetup(run)
		run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_items IS '"+sepgsqlS+"'", 0))
		run(fixture.Exec("CREATE TEMP TABLE sepgsql_temporary(id integer)", 0))
		run(fixture.Exec("INSERT INTO sepgsql_temporary VALUES(9)", 1))
		run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_temporary IS '"+sepgsqlS+"'", 0))
		run(fixture.Exec("CREATE EXTENSION hstore", 0))
		run(fixture.Exec("CREATE TABLE sepgsql_statistics(id integer,value hstore)", 0))
		run(fixture.Exec("INSERT INTO sepgsql_statistics VALUES(1,hstore('k','v')),(2,hstore('k','v')),(3,hstore('k','v')),(4,hstore('k','w')),(5,hstore('k','w'))", 5))
		run(fixture.Exec("ANALYZE sepgsql_statistics", 0))
		for _, signature := range []string{"hstore(text,text)", "hstore(text[])", "hstore(text[],text[])"} {
			run(fixture.Exec("SECURITY LABEL ON FUNCTION "+signature+" IS '"+sepgsqlU+"'", 0))
		}
		original := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if !strings.Contains(original, "stuntgres-sepgsql-simulation-v1") || !strings.Contains(original, "SECURITY LABEL") || !strings.Contains(original, sepgsqlS) {
			t.Fatal("dump omitted actual provider state")
		}
		if strings.Contains(original, "sepgsql_control_temp") {
			t.Fatal("dump serialized a physical-session TEMP sequence")
		}
		statisticsPayload := sepgsqlStatisticsPayload(t, original)
		old, err := c.QueryContext(p.Ctx, "SELECT id FROM sepgsql_items ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		types, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		for _, want := range []int{1, 2} {
			var id int
			if !old.Next() {
				t.Fatal("lost retained row", old.Err())
			}
			if err = old.Scan(&id); err != nil || id != want {
				t.Fatal("old snapshot", id, err)
			}
		}
		if old.Next() || old.Err() != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
			t.Fatal("retained metadata/tail", old.Err())
		}
		old.Close()
		run(sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname='sepgsql_getcon'", 0))
		run(sepgsqlCount("SELECT count(*) FROM sepgsql_temporary WHERE id=9", 1))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='sepgsql_temporary'::regclass AND objsubid=0 AND label='"+sepgsqlS+"'", 1))
		run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname IN ('sepgsql_control_default','sepgsql_control_relabel','sepgsql_control_absent','sepgsql_control_serial_id_seq','sepgsql_control_identity_id_seq','sepgsql_control_add_id_seq')", 0))
		sepgsqlSequenceState(run, "sepgsql_control_temp", 29, false)
		if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(original)); err != nil {
			t.Fatal(err)
		}
		run(sepgsqlCount("SELECT count(*) FROM sepgsql_items", 2))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlA))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='sepgsql_items'::regclass AND objsubid=0 AND label='"+sepgsqlS+"'", 1))
		run(sepgsqlCount("SELECT count(*) FROM sepgsql_statistics", 5))
		sepgsqlSequenceControlState(run)
		if restored := sepgsqlStatisticsPayload(t, fixture.ExtensionDump(t, p.Ctx, p.Native)); restored != statisticsPayload {
			t.Fatal("private canonical statistics reownership changed retained distribution")
		}
		// Canonical statistics constructors use their genuine private bootstrap
		// codec origin. The same real C constructor as ordinary SQL still uses R.
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlDenied("SELECT hstore('k','v')"))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		for _, bad := range []string{original + "\nINVALID SQL;", strings.Replace(original, "stuntgres-sepgsql-simulation-v1", "stuntgres-sepgsql-simulation-v999", 1), strings.Replace(original, sepgsqlS, "invalid-label", 1)} {
			if bad == original {
				t.Fatal("malformed fixture unchanged")
			}
			if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
				t.Fatal("malformed restore accepted")
			}
			if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
				t.Fatal("failed restore changed live catalog")
			}
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) || fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("canceled restore atomicity", err)
		}
		ctx, cancel = context.WithCancel(p.Ctx)
		cancel()
		err = stuntgres.Reset(ctx, p.Native)
		if !errors.Is(err, context.Canceled) || fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("canceled reset changed sequences or other durable state", err)
		}
		// The same single pooled physical session is borrowed for admission, released,
		// then reused by reader I/O. The admitted subject must remain A, not this R.
		control := sql.OpenDB(p.NativeConnector)
		defer control.Close()
		control.SetMaxOpenConns(1)
		callbackError := make(chan error, 1)
		reader := &sepgsqlControlReader{source: strings.NewReader(original), before: func() {
			var changed bool
			callbackError <- control.QueryRowContext(p.Ctx, "SELECT sepgsql_setcon('"+sepgsqlR+"')").Scan(&changed)
		}}
		if err = stuntgres.Restore(p.Ctx, control, reader); err != nil {
			t.Fatal("detached admission subject changed during reader I/O", err)
		}
		if err = <-callbackError; err != nil {
			t.Fatal(err)
		}
		var subject string
		if err = control.QueryRowContext(p.Ctx, "SELECT sepgsql_getcon()").Scan(&subject); err != nil || subject != sepgsqlR {
			t.Fatal("control replaced physical subject", subject, err)
		}
		if err = stuntgres.Reset(p.Ctx, control); err == nil {
			t.Fatal("reader subject borrowed admin control authority")
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("denied control changed live catalog")
		}
		sepgsqlSequenceControlState(run)
		run(fixture.Exec("INSERT INTO sepgsql_control_serial DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO sepgsql_control_identity DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO sepgsql_control_add DEFAULT VALUES", 1))
		run(fixture.Select("SELECT id FROM sepgsql_control_serial ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(fixture.Select("SELECT id FROM sepgsql_control_identity ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(17)}, {int64(18)}}))
		run(fixture.Select("SELECT id FROM sepgsql_control_add ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(23)}, {int64(24)}}))
		run(fixture.Select("SELECT nextval('sepgsql_control_default'),nextval('sepgsql_control_relabel'),nextval('sepgsql_control_absent')", []string{"nextval", "nextval", "nextval"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(9), int64(41), int64(14)}}))
	})
	sepgsqlSequenceControlFailures(t)
}
func TestReferenceSepgsqlSimulationFeatureSmoke(t *testing.T) {
	sepgsqlSmokePermissive(t)
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		sepgsqlOrdinaryTable(t, p, c, run)
		run(fixture.Exec("CREATE VIEW sepgsql_smoke AS SELECT id,v FROM sepgsql_items", 0))
		run(sepgsqlCount("SELECT count(*) FROM sepgsql_smoke", 2))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='sepgsql_items'::regclass", 0))
		run(fixture.Exec("CREATE SEQUENCE sepgsql_smoke_sequence_p START 5 RESTART 9 CACHE 3", 0))
		sepgsqlSequenceState(run, "sepgsql_smoke_sequence_p", 9, false)
		sepgsqlSequenceAssociation(run, "sepgsql_smoke_sequence_p", sepgsqlP, false)
		run(fixture.ImplicitError("SECURITY LABEL FOR selinux ON TABLE sepgsql_items IS NULL", fixture.Diagnostic{Code: "22023", Message: "security label provider \"selinux\" is not loaded"}))
	}, sepgsqlSmokeConfig, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		sepgsqlSmokeActive(t, p, c, run)
	})
}

// Extend the actual control runner: genuine persistent generators and owned
// dependencies accompany explicit, relabeled and deliberately absent labels.
func sepgsqlSequenceControlSetup(run sepgsqlRun) {
	for _, q := range []string{
		"CREATE SEQUENCE sepgsql_control_default START 5 RESTART 9 CACHE 3 MAXVALUE 99",
		"CREATE SEQUENCE sepgsql_control_relabel START 7 INCREMENT 2 CACHE 1",
		"CREATE SEQUENCE sepgsql_control_absent START 13 CACHE 1",
		"CREATE TABLE sepgsql_control_serial(id serial)",
		"CREATE TABLE sepgsql_control_identity(id bigint GENERATED BY DEFAULT AS IDENTITY (START WITH 17))",
		"CREATE TABLE sepgsql_control_add(id bigint NOT NULL)",
		"ALTER TABLE sepgsql_control_add ALTER id ADD GENERATED BY DEFAULT AS IDENTITY (START WITH 23)",
		"CREATE TEMP SEQUENCE sepgsql_control_temp START 29",
		"SECURITY LABEL ON SEQUENCE sepgsql_control_relabel IS '" + sepgsqlS + "'",
		"SECURITY LABEL ON SEQUENCE sepgsql_control_absent IS NULL",
		"GRANT USAGE,SELECT ON SEQUENCE sepgsql_control_relabel TO PUBLIC",
		"INSERT INTO sepgsql_control_serial DEFAULT VALUES",
		"INSERT INTO sepgsql_control_identity DEFAULT VALUES",
		"INSERT INTO sepgsql_control_add DEFAULT VALUES",
		"ALTER SEQUENCE sepgsql_control_relabel RESTART WITH 41",
	} {
		count := int64(0)
		if strings.HasPrefix(q, "INSERT") {
			count = 1
		}
		run(fixture.Exec(q, count))
	}
	run(fixture.Select("SELECT nextval('sepgsql_control_absent') AS value", []string{"value"}, []string{"INT8"}, [][]any{{int64(13)}}))
	sepgsqlSequenceControlState(run)
}

func sepgsqlSequenceControlState(run sepgsqlRun) {
	sepgsqlSequenceAssociation(run, "sepgsql_control_default", sepgsqlP, true)
	sepgsqlSequenceAssociation(run, "sepgsql_control_relabel", sepgsqlS, true)
	run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='sepgsql_control_absent'::regclass", 0))
	sepgsqlSequenceState(run, "sepgsql_control_default", 9, false)
	sepgsqlSequenceState(run, "sepgsql_control_relabel", 41, false)
	// log_cnt is a PostgreSQL physical counter, not a portable generator value.
	run(fixture.Select("SELECT last_value,is_called FROM sepgsql_control_absent", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(13), true}}))
	run(sepgsqlCount("SELECT count(*) FROM pg_sequence WHERE seqrelid='sepgsql_control_default'::regclass AND seqstart=5 AND seqcache=3 AND seqmax=99 AND seqincrement=1", 1))
	run(sepgsqlCount("SELECT count(*) FROM pg_sequence WHERE seqrelid='sepgsql_control_relabel'::regclass AND seqstart=7 AND seqcache=1 AND seqincrement=2", 1))
	run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE oid IN ('sepgsql_control_default'::regclass,'sepgsql_control_relabel'::regclass,'sepgsql_control_absent'::regclass) AND relkind='S' AND relnamespace='public'::regnamespace AND relowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)", 3))
	run(sepgsqlText("SELECT relacl::text AS acl FROM pg_class WHERE oid='sepgsql_control_relabel'::regclass", "acl", "{postgres=rwU/postgres,=rU/postgres}"))
	for _, table := range []string{"sepgsql_control_serial", "sepgsql_control_identity", "sepgsql_control_add"} {
		name := table + "_id_seq"
		sepgsqlSequenceAssociation(run, name, sepgsqlP, true)
		run(sepgsqlText("SELECT pg_get_serial_sequence('"+table+"','id') AS owned", "owned", "public."+name))
		run(sepgsqlCount("SELECT count(*) FROM pg_depend WHERE classid='pg_class'::regclass AND objid='"+name+"'::regclass AND refclassid='pg_class'::regclass AND refobjid='"+table+"'::regclass AND refobjsubid=1 AND deptype IN ('a','i')", 1))
		want := int64(1)
		if table == "sepgsql_control_identity" {
			want = 17
		}
		if table == "sepgsql_control_add" {
			want = 23
		}
		run(fixture.Select("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{want, true}}))
	}
	sepgsqlSequenceState(run, "sepgsql_control_temp", 29, false)
	run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='sepgsql_control_temp'::regclass AND objsubid=0 AND label='"+sepgsqlP+"'", 1))
}

func sepgsqlSequenceControlFailures(t *testing.T) {
	t.Run("sequence_destination_consent", func(t *testing.T) {
		var mu sync.Mutex
		var events []stuntgres.SepgsqlAuditEvent
		sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			run(fixture.Exec("CREATE SEQUENCE sepgsql_control_consent START 43", 0))
			sepgsqlSequenceState(run, "sepgsql_control_consent", 43, false)
		}, func(config *stuntgres.SepgsqlSimulationConfig) {
			config.Policy.Rules = append(config.Policy.Rules,
				stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "*", Allow: []string{"*"}},
				stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_sequence", Deny: []string{"relabelto"}},
				stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlS, Class: "db_sequence", Allow: []string{"setattr", "relabelfrom"}})
			config.Audit = func(event stuntgres.SepgsqlAuditEvent) { mu.Lock(); events = append(events, event); mu.Unlock() }
		}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			run(fixture.Exec("CREATE SEQUENCE sepgsql_control_consent START 43", 0))
			sepgsqlSequenceAssociation(run, "sepgsql_control_consent", sepgsqlP, true)
			original := fixture.ExtensionDump(t, p.Ctx, p.Native)
			run(fixture.Exec("SECURITY LABEL ON SEQUENCE sepgsql_control_consent IS '"+sepgsqlS+"'", 0))
			sepgsqlSequenceAssociation(run, "sepgsql_control_consent", sepgsqlS, true)
			before := fixture.ExtensionDump(t, p.Ctx, p.Native)
			const identityQuery = "SELECT oid::bigint,relowner::bigint,relnamespace::bigint,relacl::text FROM pg_class WHERE oid='sepgsql_control_consent'::regclass"
			var oid, owner, namespace int64
			var acl sql.NullString
			if err := c.QueryRowContext(p.Ctx, identityQuery).Scan(&oid, &owner, &namespace, &acl); err != nil || oid <= 0 || owner <= 0 || namespace <= 0 {
				t.Fatal("actual destination sequence identity", oid, owner, namespace, err)
			}
			var aclValue any
			if acl.Valid {
				aclValue = acl.String
			}
			identity := fixture.Select(identityQuery, []string{"oid", "relowner", "relnamespace", "relacl"}, []string{"INT8", "INT8", "INT8", "TEXT"}, [][]any{{oid, owner, namespace, aclValue}})
			var auditName string
			if err := c.QueryRowContext(p.Ctx, "SELECT quote_ident(current_database())||'.public.sepgsql_control_consent'").Scan(&auditName); err != nil {
				t.Fatal(err)
			}
			reader := sql.OpenDB(p.NativeConnector.WithSepgsqlContext(sepgsqlR))
			defer reader.Close()
			err := stuntgres.Restore(p.Ctx, reader, strings.NewReader(original))
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "SELinux: security policy violation"})
			if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
				t.Fatal("destination sequence transfer denial changed target")
			}
			sepgsqlSequenceState(run, "sepgsql_control_consent", 43, false)
			sepgsqlSequenceAssociation(run, "sepgsql_control_consent", sepgsqlS, true)
			run(identity)
			run(fixture.Prepared(identity))
			run(sepgsqlCount("SELECT count(*) FROM pg_sequence WHERE seqrelid='sepgsql_control_consent'::regclass AND seqtypid='bigint'::regtype AND seqstart=43 AND seqincrement=1 AND seqcache=1 AND seqmin=1 AND seqmax=9223372036854775807 AND NOT seqcycle", 1))
			mu.Lock()
			captured := append([]stuntgres.SepgsqlAuditEvent(nil), events...)
			mu.Unlock()
			found := false
			for _, event := range captured {
				if event.Subject == sepgsqlR && event.Target == sepgsqlP && event.Class == "db_sequence" && event.Name == auditName &&
					len(event.Required) == 1 && event.Required[0] == "relabelto" && len(event.Denied) == 1 && event.Denied[0] == "relabelto" && !event.Permissive {
					found = true
				}
			}
			if !found {
				t.Fatal("sequence transfer denial omitted its real detached audit", captured)
			}
			t.Run("policy_mismatch", func(t *testing.T) {
				target := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
					config := sepgsqlConfig()
					config.Policy.Version = "different-sequence-policy"
					return d.RegisterSepgsqlSimulation(config)
				})
				fixture.CatalogControlExec(t, target.Native, "CREATE SEQUENCE mismatch_survivor START 47")
				before := fixture.ExtensionDump(t, target.Ctx, target.Native)
				err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(original))
				fixture.TransactionOrdinary(t, err, "stuntgres: incompatible sepgsql simulation registration")
				if fixture.ExtensionDump(t, target.Ctx, target.Native) != before {
					t.Fatal("policy mismatch changed destination")
				}
			})
			t.Run("trusted_import_not_public_sql", func(t *testing.T) {
				target := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
					config := sepgsqlConfig()
					config.Policy.Rules = append(config.Policy.Rules,
						stuntgres.SepgsqlRule{Source: sepgsqlA, Target: sepgsqlP, Class: "db_sequence", Deny: []string{"create"}},
						stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_sequence", Deny: []string{"create"}},
						stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "*", Allow: []string{"*"}})
					return d.RegisterSepgsqlSimulation(config)
				})
				before := fixture.ExtensionDump(t, target.Ctx, target.Native)
				_, err := target.Native.ExecContext(target.Ctx, "CREATE SEQUENCE public_sql_must_not_import")
				fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "SELinux: security policy violation"})
				if fixture.ExtensionDump(t, target.Ctx, target.Native) != before {
					t.Fatal("public CREATE minted import authority")
				}
				control := sql.OpenDB(target.NativeConnector.WithSepgsqlContext(sepgsqlR))
				defer control.Close()
				_, err = control.ExecContext(target.Ctx, "CREATE SEQUENCE reader_public_sql_must_not_import")
				fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "SELinux: security policy violation"})
				if fixture.ExtensionDump(t, target.Ctx, target.Native) != before {
					t.Fatal("reader public CREATE minted import authority")
				}
				if err = stuntgres.Restore(target.Ctx, control, strings.NewReader(original)); err != nil {
					t.Fatal("trusted isolated import borrowed SQL postcreate denial", err)
				}
				targetRun := func(s fixture.Step) { fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s) }
				sepgsqlSequenceState(targetRun, "sepgsql_control_consent", 43, false)
				sepgsqlSequenceAssociation(targetRun, "sepgsql_control_consent", sepgsqlP, true)
				targetRun(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname IN ('public_sql_must_not_import','reader_public_sql_must_not_import')", 0))
			})
		})
	})
}
