package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/ngrok/sqlmw"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantDatabaseAdmission(t *testing.T) {
	const login = "og_database_login"
	roles := []string{login, "og_object_reader", "og_object_other"}
	fixture.RoleReferenceCleanup(t, []string{login})
	var pgDatabase string
	var pgPublic []string
	// Register cluster ACL restoration before the shared fixture. Its pools and
	// physical sessions close first; this fallback also covers an early failure.
	t.Cleanup(func() {
		if pgDatabase == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		objectGrantDatabaseCleanup(t, ctx, fixture.Admin, pgDatabase, roles, pgPublic)
	})
	objectGrantScenario(t, []string{"og_database_created", "og_database_delegated", "og_database_all"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		dsn := "reference/" + strings.TrimSuffix(t.Name(), "/"+b.Name)
		database := dsn
		owner := "postgres"
		if b.PG {
			u, err := url.Parse(fixture.DSN)
			if err != nil {
				t.Fatal(err)
			}
			database = strings.TrimPrefix(u.Path, "/")
			owner = "stuntgres"
		}
		// Backend names are independently determined by each public attachment
		// route. PostgreSQL's fixture name is not the native opaque DSN.
		run(fixture.Select("SELECT current_database() AS database_name", []string{"database_name"}, []string{"NAME"}, [][]any{{database}}))
		quotedDB := pq.QuoteIdentifier(database)
		public := []string{"CONNECT", "TEMPORARY"}
		if b.PG {
			public = objectGrantDatabasePublic(t, p.Ctx, c)
			pgDatabase, pgPublic = database, public
		}
		loginCreated := false
		// This cleanup runs before the shared helper attempts DROP ROLE. Database
		// ACLs are cluster-wide on PostgreSQL and survive its public-schema reset.
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			objectGrantDatabaseCleanup(t, ctx, b.DB, database, roles, public)
			for _, schema := range []string{"og_database_created", "og_database_delegated", "og_database_all"} {
				if _, err := b.DB.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+pq.QuoteIdentifier(schema)+" CASCADE"); err != nil {
					t.Error("database schema cleanup", err)
				}
			}
			if loginCreated {
				result, err := b.DB.ExecContext(ctx, "DROP ROLE "+login)
				fixture.DeleteReferenceCount(t, result, err, 0)
			}
		})
		// Native attachments start with an absent ACL. The shared PostgreSQL
		// database retains an explicit default ACL after other grant cases;
		// cleanup restores its logical PUBLIC policy, not its physical NULL.
		if !b.PG {
			run(fixture.Select("SELECT datacl IS NULL AS absent FROM pg_database WHERE datname=current_database()", []string{"absent"}, []string{"BOOL"}, [][]any{{true}}))
		}
		run(fixture.Exec("CREATE USER "+login+" NOINHERIT", 0))
		loginCreated = true
		run(fixture.Exec("GRANT og_object_reader,og_object_other TO "+login, 0))
		if b.PG {
			result, err := fixture.Admin.ExecContext(p.Ctx, "ALTER ROLE "+login+" PASSWORD 'role-core-fixture'")
			fixture.DeleteReferenceCount(t, result, err, 0)
		}
		// Every new pool owns a new physical attachment; never infer admission from
		// a borrower of the existing administrative or authenticated pool.
		connector := func() driver.Connector {
			if b.PG {
				u, err := url.Parse(fixture.DSN)
				if err != nil {
					t.Fatal(err)
				}
				u.User = url.UserPassword(login, "role-core-fixture")
				base, err := pq.NewConnector(u.String())
				if err != nil {
					t.Fatal(err)
				}
				return base
			}
			// Native-only public API phase: exact Driver and opaque DSN identity.
			base, err := p.NativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(dsn, login)
			if err != nil {
				t.Fatal(err)
			}
			return base
		}
		// Match the existing roleAttributesPool fixture route: middleware owns
		// genuine direct/prepared result capture on this authenticated connector.
		recordedConnector := func() driver.Connector {
			wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{connector()}, fixture.Interceptor{})
			configured, err := wrapped.(driver.DriverContext).OpenConnector("")
			if err != nil {
				t.Fatal(err)
			}
			return configured
		}
		fresh := func(allowed bool) {
			t.Helper()
			pool := sql.OpenDB(connector())
			defer pool.Close()
			err := pool.PingContext(p.Ctx)
			if allowed {
				if err != nil {
					t.Fatal("fresh physical admission", err)
				}
				return
			}
			want := fixture.ControlsReferenceDiagnostic("42501", `permission denied for database "`+database+`"`)
			want.Detail = "User does not have CONNECT privilege."
			want.Severity, want.SeverityUnlocalized = "FATAL", "FATAL"
			fixture.AssertDiagnostic(t, err, &want)
		}
		pool := sql.OpenDB(recordedConnector())
		defer pool.Close()
		admitted, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admitted.Close()
		authenticatedRecord := &fixture.Recorder{RawBackend: b.Recorder.RawBackend}
		use := func(s fixture.Step) {
			t.Helper()
			s.Prepared = prepared
			objectGrantStep(t, p.Ctx, admitted, authenticatedRecord, s)
		}
		identity := func(effective string) {
			use(fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{login, effective}}))
		}
		deny := func(execute func(fixture.Step), query, message string) {
			s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: message})
			// CREATE TABLE transformation attaches the target location: root
			// artifact177 observed19; qualified table14 remains source-derived.
			// DefineSequence instead invokes DefineRelation directly without
			// that parser error-position context; sequence positions stay empty.
			for _, prefix := range []string{"CREATE TEMP TABLE ", "CREATE TABLE "} {
				if strings.HasPrefix(query, prefix) {
					s.Diagnostic.Position = strconv.Itoa(len(prefix) + 1)
					break
				}
			}
			s.ErrorStage = "execute"
			execute(s)
		}
		dbDenied := "permission denied for database " + database
		lazyTempDenied := `permission denied to create temporary tables in database "` + database + `"`

		// Default PUBLIC Tc admits a fresh LOGIN and TEMP, but not CREATE.
		identity(login)
		fresh(true)
		deny(use, "CREATE SCHEMA og_database_created", dbDenied)
		use(fixture.Exec("CREATE TEMP TABLE og_database_old(x INT)", 0))
		use(fixture.Exec("INSERT INTO og_database_old VALUES(7)", 1))
		use(fixture.Exec("CREATE TEMP SEQUENCE og_database_old_seq START 10", 0))
		use(fixture.Select("SELECT nextval('og_database_old_seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}}))
		var tempNamespace string
		if err := admitted.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid WHERE c.relname='og_database_old'").Scan(&tempNamespace); err != nil {
			t.Fatal("own TEMP namespace identity", err)
		}
		suffix, ok := strings.CutPrefix(tempNamespace, "pg_temp_")
		number, suffixErr := strconv.ParseUint(suffix, 10, 64)
		if !ok || suffixErr != nil || number == 0 {
			t.Fatal("invalid physical TEMP namespace identity", tempNamespace, suffixErr)
		}
		tempDenied := "permission denied for schema " + tempNamespace

		// Materialized owner-only ACL must not regain PUBLIC Tc via a default.
		run(fixture.Exec("REVOKE ALL PRIVILEGES ON DATABASE "+quotedDB+" FROM PUBLIC", 0))
		run(fixture.Select("SELECT datacl::TEXT AS acl FROM pg_database WHERE datname=current_database()", []string{"acl"}, []string{"TEXT"}, [][]any{{"{" + owner + "=CTc/" + owner + "}"}}))
		fresh(false)
		// A truly fresh physical session has no allocated namespace. Its initial
		// TEMP denial has the database diagnostic, unlike the admitted session.
		run(fixture.Exec("GRANT CONNECT ON DATABASE "+quotedDB+" TO "+login, 0))
		func() {
			emptyPool := sql.OpenDB(recordedConnector())
			defer emptyPool.Close()
			empty, err := emptyPool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer empty.Close()
			emptyRecord := &fixture.Recorder{RawBackend: b.Recorder.RawBackend}
			for _, query := range []string{"CREATE TEMP TABLE og_database_lazy_denied(x INT)", "CREATE TEMP SEQUENCE og_database_lazy_denied_seq"} {
				s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: lazyTempDenied})
				// Root artifact174 observed table target19 on both routes;
				// artifact180 observed no position for the sequence utility.
				if strings.HasPrefix(query, "CREATE TEMP TABLE ") {
					s.Diagnostic.Position = "19"
				}
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, empty, emptyRecord, s)
			}
		}()
		run(fixture.Exec("REVOKE CONNECT ON DATABASE "+quotedDB+" FROM "+login, 0))
		identity(login)
		use(fixture.Select("SELECT x FROM og_database_old", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}))
		use(fixture.Select("SELECT nextval('og_database_old_seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(11)}}))
		deny(use, "CREATE TEMP TABLE og_database_denied(x INT)", tempDenied)
		deny(use, "CREATE TEMP SEQUENCE og_database_denied_seq", tempDenied)
		deny(use, "CREATE TABLE pg_temp.og_database_implicit(x INT)", tempDenied)
		deny(use, "CREATE SEQUENCE pg_temp.og_database_implicit_seq", tempDenied)

		// TEMP uses the current effective role, even in an already allocated own
		// namespace. CONNECT uses the immutable authenticated role only at startup.
		run(fixture.Exec("GRANT TEMPORARY ON DATABASE "+quotedDB+" TO og_object_reader", 0))
		use(fixture.Exec("SET ROLE og_object_reader", 0))
		identity("og_object_reader")
		use(fixture.Exec("CREATE TEMP TABLE og_database_reader(x INT)", 0))
		use(fixture.Exec("INSERT INTO og_database_reader VALUES(9)", 1))
		use(fixture.Exec("CREATE SEQUENCE pg_temp.og_database_reader_seq START 20", 0))
		use(fixture.Exec("SET ROLE og_object_other", 0))
		identity("og_object_other")
		deny(use, "CREATE TEMP TABLE og_database_other(x INT)", tempDenied)
		deny(use, "CREATE TEMP SEQUENCE og_database_other_seq", tempDenied)
		use(fixture.Exec("RESET ROLE", 0))
		identity(login)
		deny(use, "CREATE TEMP TABLE og_database_reset_denied(x INT)", tempDenied)
		run(fixture.Exec("REVOKE TEMPORARY ON DATABASE "+quotedDB+" FROM og_object_reader", 0))
		use(fixture.Exec("SET ROLE og_object_reader", 0))
		deny(use, "CREATE TABLE pg_temp.og_database_reader_denied(x INT)", tempDenied)
		deny(use, "CREATE SEQUENCE pg_temp.og_database_reader_denied_seq", tempDenied)
		use(fixture.Select("SELECT x FROM og_database_reader", []string{"x"}, []string{"INT4"}, [][]any{{int64(9)}}))
		run(fixture.Exec("GRANT TEMP ON DATABASE "+quotedDB+" TO og_object_reader", 0))
		use(fixture.Exec("CREATE TEMP SEQUENCE og_database_regranted_seq", 0))
		use(fixture.Exec("RESET ROLE", 0))

		// CREATE alone grants a real schema operation, not TEMP or CONNECT.
		run(fixture.Exec("GRANT CREATE ON DATABASE "+quotedDB+" TO "+login, 0))
		use(fixture.Exec("CREATE SCHEMA og_database_created", 0))
		run(fixture.Exec("REVOKE CREATE ON DATABASE "+quotedDB+" FROM "+login, 0))
		deny(use, "CREATE SCHEMA og_database_revoke_denied", "permission denied for database "+database)
		run(fixture.Select("SELECT nspname FROM pg_namespace WHERE nspname='og_database_revoke_denied'", []string{"nspname"}, []string{"NAME"}, [][]any{}))
		use(fixture.Select("SELECT x FROM og_database_old", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}))
		// Restore the independently asserted later CTc provenance inventory.
		run(fixture.Exec("GRANT CREATE ON DATABASE "+quotedDB+" TO "+login, 0))
		deny(use, "CREATE TEMP TABLE og_database_create_only(x INT)", tempDenied)
		fresh(false)
		run(fixture.Exec("GRANT CONNECT ON DATABASE "+quotedDB+" TO "+login, 0))
		fresh(true)
		run(fixture.Exec("REVOKE CONNECT ON DATABASE "+quotedDB+" FROM "+login, 0))
		// Neither SET ROLE nor RESET ROLE re-admits or disconnects this session.
		use(fixture.Exec("SET ROLE og_object_reader", 0))
		identity("og_object_reader")
		use(fixture.Exec("RESET ROLE", 0))
		identity(login)
		fresh(false)

		// ALL is CTc. Every bit can be delegated by the reader's grant options.
		run(fixture.Exec("GRANT ALL PRIVILEGES ON DATABASE "+quotedDB+" TO og_object_reader WITH GRANT OPTION", 0))
		run(fixture.Select("SELECT datacl::TEXT AS acl FROM pg_database WHERE datname=current_database()", []string{"acl"}, []string{"TEXT"}, [][]any{{"{" + owner + "=CTc/" + owner + ",og_object_reader=C*T*c*/" + owner + "," + login + "=C/" + owner + "}"}}))
		use(fixture.Exec("SET ROLE og_object_reader", 0))
		use(fixture.Exec("GRANT CREATE,CONNECT,TEMPORARY ON DATABASE "+quotedDB+" TO og_object_other", 0))
		// Delegated CONNECT is proved by a fresh LOGIN attachment, not merely an
		// ACL string or successful borrowing of the already admitted session.
		run(fixture.Exec("ALTER ROLE og_object_other LOGIN", 0))
		if b.PG {
			result, err := fixture.Admin.ExecContext(p.Ctx, "ALTER ROLE og_object_other PASSWORD 'role-core-fixture'")
			fixture.DeleteReferenceCount(t, result, err, 0)
		}
		delegatedFresh := func(allowed bool) {
			t.Helper()
			db := fixture.RoleScenarioLoginPool(t, p.NativeConnector.Driver().(*stuntgres.Driver), b.PG, dsn, "og_object_other")
			defer db.Close()
			err := db.PingContext(p.Ctx)
			if allowed {
				if err != nil {
					t.Fatal("delegated CONNECT admission", err)
				}
				return
			}
			want := fixture.ControlsReferenceDiagnostic("42501", `permission denied for database "`+database+`"`)
			want.Detail = "User does not have CONNECT privilege."
			want.Severity, want.SeverityUnlocalized = "FATAL", "FATAL"
			fixture.AssertDiagnostic(t, err, &want)
		}
		delegatedFresh(true)
		use(fixture.Exec("SET ROLE og_object_other", 0))
		use(fixture.Exec("CREATE SCHEMA og_database_delegated", 0))
		use(fixture.Exec("CREATE TEMP TABLE og_database_delegated_temp(x INT)", 0))
		use(fixture.Exec("CREATE TEMP SEQUENCE og_database_delegated_seq", 0))
		use(fixture.Exec("RESET ROLE", 0))
		// NOINHERIT separates login CONNECT from its members' CTc.
		fresh(false)
		run(fixture.Exec("GRANT ALL ON DATABASE "+quotedDB+" TO "+login, 0))
		fresh(true)
		use(fixture.Exec("CREATE SCHEMA og_database_all", 0))
		use(fixture.Exec("CREATE TEMP TABLE og_database_all_temp(x INT)", 0))
		use(fixture.Exec("CREATE TEMP SEQUENCE og_database_all_seq", 0))
		run(fixture.Exec("REVOKE GRANT OPTION FOR ALL PRIVILEGES ON DATABASE "+quotedDB+" FROM og_object_reader CASCADE", 0))
		delegatedFresh(false)
		use(fixture.Exec("SET ROLE og_object_other", 0))
		deny(use, "CREATE SCHEMA og_database_cascade_denied", dbDenied)
		deny(use, "CREATE TEMP SEQUENCE og_database_cascade_denied_seq", tempDenied)
		use(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("REVOKE ALL ON DATABASE "+quotedDB+" FROM "+login, 0))
		fresh(false)
		identity(login)
		deny(use, "CREATE TEMP TABLE og_database_all_revoked(x INT)", tempDenied)
	})
}

// Capture the pre-scenario PUBLIC policy, including an explicit-empty policy;
// cleanup must restore it rather than blindly adding defaults to explicit ACLs.
func objectGrantDatabasePublic(t *testing.T, ctx context.Context, c *sql.Conn) []string {
	t.Helper()
	rows, err := c.QueryContext(ctx, "SELECT a.privilege_type FROM pg_database d CROSS JOIN LATERAL aclexplode(COALESCE(d.datacl,acldefault('d',d.datdba))) a WHERE d.datname=current_database() AND a.grantee=0 ORDER BY a.privilege_type")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var privileges []string
	for rows.Next() {
		var privilege string
		if err := rows.Scan(&privilege); err != nil {
			t.Fatal(err)
		}
		privileges = append(privileges, privilege)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return privileges
}

func objectGrantDatabaseCleanup(t *testing.T, ctx context.Context, db *sql.DB, database string, roles, public []string) {
	t.Helper()
	quoted := pq.QuoteIdentifier(database)
	queries := []string{"REVOKE ALL PRIVILEGES ON DATABASE " + quoted + " FROM PUBLIC"}
	if len(public) != 0 {
		queries = append(queries, "GRANT "+strings.Join(public, ",")+" ON DATABASE "+quoted+" TO PUBLIC")
	}
	for _, role := range roles {
		var exists bool
		if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); err != nil {
			t.Error("database ACL cleanup role lookup", err)
			continue
		}
		if exists {
			queries = append(queries, "REVOKE ALL PRIVILEGES ON DATABASE "+quoted+" FROM "+pq.QuoteIdentifier(role)+" CASCADE")
		}
	}
	for _, query := range queries {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Errorf("database ACL cleanup %s: %v", query, err)
		}
	}
}
