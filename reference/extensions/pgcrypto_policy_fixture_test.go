package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const pgcryptoPolicyName = "pgcrypto.builtin_crypto_enabled"
const pgcryptoPolicyOff = "use of built-in crypto functions is disabled"
const pgcryptoPolicyFIPS = "use of non-FIPS validated crypto not allowed when OpenSSL is in FIPS mode"
const pgcryptoPolicyDescription = "Sets if builtin crypto functions are enabled."
const pgcryptoPolicyExtra = `"on" enables builtin crypto, "off" unconditionally disables and "fips" will disable builtin crypto if OpenSSL is in FIPS mode`
const pgcryptoPolicyCreateDigestContext = `SQL statement "CREATE FUNCTION digest(text, text)
RETURNS bytea
AS '$libdir/pgcrypto', 'pg_digest'
LANGUAGE C IMMUTABLE STRICT PARALLEL SAFE"
extension script file "pgcrypto--1.3.sql", near line 6`
const pgcryptoPolicyCreateCryptContext = `SQL statement "CREATE FUNCTION crypt(text, text)
RETURNS text
AS '$libdir/pgcrypto', 'pg_crypt'
LANGUAGE C IMMUTABLE STRICT PARALLEL SAFE"
extension script file "pgcrypto--1.3.sql", near line 26`

// Every callback starts on a new physical, notice-enabled connection. The
// package's ordinary pgcryptoCase connection can already have loaded the module.
func pgcryptoPolicyCase(t *testing.T, install bool, body func(*testing.T, *fixture.Pair, *sql.Conn, *fixture.Recorder, bool, *[]fixture.Diagnostic)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "pgcrypto_part")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			if _, err := backend.DB.ExecContext(p.Ctx, "DROP EXTENSION IF EXISTS pgcrypto CASCADE"); err != nil {
				t.Fatal(err)
			}
			var notices []fixture.Diagnostic
			_, c, r := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			t.Cleanup(func() {
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET ALL", "DROP EXTENSION IF EXISTS pgcrypto CASCADE"} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil {
						t.Error("policy cleanup", q, err)
					}
				}
			})
			if install {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
			}
			notices = nil
			body(t, p, c, r, backend.PG, &notices)
		})
	}
}

// Parameter SET grants are cluster-level dependencies: schema teardown and
// DROP OWNED do not revoke them. Register immediately after role creation,
// before granting authority, so failed assertions cannot poison later cases.
func pgcryptoPolicyRoleGrantCleanup(t *testing.T, c *sql.Conn, role, database string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		queries := []string{
			"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION",
			"REVOKE SET ON PARAMETER pgcrypto.builtin_crypto_enabled FROM " + role,
		}
		if database != "" {
			queries = append(queries, "REVOKE CREATE ON DATABASE "+database+" FROM "+role)
		}
		for _, query := range queries {
			if _, err := c.ExecContext(ctx, query); err != nil {
				t.Error("policy grant cleanup", query, err)
			}
		}
	})
}

func pgcryptoPolicyText(r *fixture.Recorder, query string, want any, args ...any) fixture.Step {
	s := fixture.Select(query, []string{"value"}, []string{"TEXT"}, [][]any{{want}}, args...)
	// Native setting expressions expose known nullable TEXT; installed crypto
	// and the other computed recipes keep unknown nullability. pq exposes none.
	settingResult := r.RawBackend != fixture.RawPostgres && (strings.Contains(query, "current_setting(") || strings.Contains(query, "set_config("))
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgcryptoPolicyTextMetadata(t, rows, settingResult) }
	return s
}

func pgcryptoPolicyTextMetadata(t *testing.T, rows *sql.Rows, settingResult bool) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 1 || columns[0].DatabaseTypeName() != "TEXT" || columns[0].ScanType() != reflect.TypeOf("") {
		t.Fatal("TEXT descriptors", columns)
	}
	if nullable, known := columns[0].Nullable(); nullable != settingResult || known != settingResult {
		t.Fatal("computed TEXT nullability", nullable, known, "want", settingResult, settingResult)
	}
	if _, _, known := columns[0].DecimalSize(); known {
		t.Fatal("TEXT has no decimal size")
	}
}

func pgcryptoPolicyTextLength(t *testing.T, rows *sql.Rows) {
	t.Helper()
	pgcryptoPolicyTextMetadata(t, rows, false)
	columns, err := rows.ColumnTypes()
	if err != nil { t.Fatal(err) }
	length, known := columns[0].Length()
	if !known || length != 9223372036854775807 { t.Fatal("unbounded TEXT length", length, known) }
}

func pgcryptoPolicyFailure(query, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "XX000", Message: message}, args...)
	s.SelectRows = true
	s.ErrorStage = "execute"
	return s
}

func pgcryptoPolicyInvalid(query, value string, args ...any) fixture.Step {
	return fixture.ImplicitError(query, fixture.Diagnostic{
		Code: "22023", Message: fmt.Sprintf("invalid value for parameter %q: %q", pgcryptoPolicyName, value),
		Hint: "Available values: on, off, fips.",
	}, args...)
}

func pgcryptoPolicyPermissionError(query string, args ...any) fixture.Step {
	return fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: `permission denied to set parameter "pgcrypto.builtin_crypto_enabled"`}, args...)
}

func pgcryptoPolicyShow(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder, want string) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Select("SHOW "+pgcryptoPolicyName, []string{pgcryptoPolicyName}, []string{"TEXT"}, [][]any{{want}}),
		pgcryptoPolicyText(r, "SELECT current_setting($1) AS value", want, pgcryptoPolicyName),
	} {
		fixture.RunStep(t, p.Ctx, c, r, s)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
	}
}

func pgcryptoPolicyMetadata(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder, setting, source string) {
	t.Helper()
	query := "SELECT name,setting,unit,category,short_desc,extra_desc,context,vartype,source,min_val,max_val,enumvals,boot_val,reset_val,sourcefile,sourceline,pending_restart FROM pg_settings WHERE name='" + pgcryptoPolicyName + "'"
	s := fixture.Select(query,
		[]string{"name", "setting", "unit", "category", "short_desc", "extra_desc", "context", "vartype", "source", "min_val", "max_val", "enumvals", "boot_val", "reset_val", "sourcefile", "sourceline", "pending_restart"},
		[]string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "_TEXT", "TEXT", "TEXT", "TEXT", "INT4", "BOOL"},
		[][]any{{pgcryptoPolicyName, setting, nil, "Customized Options", pgcryptoPolicyDescription, pgcryptoPolicyExtra, "superuser", "enum", source, nil, nil, "{on,off,fips}", "on", "on", nil, nil, false}})
	fixture.RunStep(t, p.Ctx, c, r, s)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT pg_settings_get_flags($1) AS flags", []string{"flags"}, []string{"_TEXT"}, [][]any{{"{}"}}, pgcryptoPolicyName))
	rows, err := c.QueryContext(p.Ctx, "SHOW ALL")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := 0
	for rows.Next() {
		var name, value, description string
		if err := rows.Scan(&name, &value, &description); err != nil {
			t.Fatal(err)
		}
		if name == pgcryptoPolicyName {
			found++
			if value != setting || description != pgcryptoPolicyDescription {
				t.Fatal("SHOW ALL policy", name, value, description)
			}
		}
	}
	if err := rows.Err(); err != nil || found != 1 {
		t.Fatal("SHOW ALL policy cardinality", found, err)
	}
}

func pgcryptoPolicyNotices(t *testing.T, records *[]fixture.Diagnostic, want ...fixture.Diagnostic) {
	t.Helper()
	if len(*records) != len(want) {
		t.Fatalf("notices got %#v, want %#v", *records, want)
	}
	for i := range want {
		if (*records)[i] != want[i] {
			t.Errorf("notice %d got %#v, want %#v", i, (*records)[i], want[i])
		}
	}
	*records = nil
}

func pgcryptoPolicyHidden(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_settings WHERE name LIKE 'pgcrypto.%'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	rows, err := c.QueryContext(p.Ctx, "SHOW ALL")
	if err != nil { t.Fatal(err) }
	defer rows.Close()
	for rows.Next() {
		var name, value, description string
		if err := rows.Scan(&name, &value, &description); err != nil { t.Fatal(err) }
		if name == pgcryptoPolicyName || name == "pgcrypto.typo" { t.Fatal("placeholder exposed in SHOW ALL", name, value, description) }
	}
	if err := rows.Err(); err != nil { t.Fatal(err) }
}

func pgcryptoPolicyUnseen(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, fixture.ImplicitError("SHOW "+pgcryptoPolicyName, fixture.Diagnostic{Code: "42704", Message: `unrecognized configuration parameter "pgcrypto.builtin_crypto_enabled"`}))
	fixture.RunStep(t, p.Ctx, c, r, pgcryptoPolicyText(r, "SELECT current_setting($1,true) AS value", nil, pgcryptoPolicyName))
	pgcryptoPolicyHidden(t, p, c, r)
}

func pgcryptoPolicyActivate(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT encode(pgcrypto_part.digest('abc','sha256'),'hex') AS value", []string{"value"}, []string{"TEXT"}, [][]any{{"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}}))
}
