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

func conversionScenario(t *testing.T, schemas []string, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	for _, schema := range schemas {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func conversionStep(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	for _, typ := range s.Types {
		if s.SelectRows && typ == "OID" {
			fixture.OidReferenceRows(t, p.Ctx, b, b.DB, s)
			return
		}
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
}

func conversionExec(t *testing.T, p *fixture.Pair, b fixture.Backend, queries ...string) {
	t.Helper()
	for _, q := range queries {
		conversionStep(t, p, b, fixture.Exec(q, 0))
	}
}

func conversionCreate(name string, def bool) string {
	marker := ""
	if def {
		marker = "DEFAULT "
	}
	return "CREATE " + marker + "CONVERSION " + name + " FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1"
}

func conversionDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

// Both semantic routes must successfully Prepare before Execute fails. Syntax
// errors are independently positioned and fail Prepare on the prepared route.
func conversionError(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, d fixture.Diagnostic, syntax bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, d)
		s.Prepared, s.ErrorStage = prepared, "execute"
		if syntax && prepared {
			s.ErrorStage = "prepare"
		}
		s.SelectRows = strings.HasPrefix(q, "SELECT")
		conversionStep(t, p, b, s)
	}
}

func conversionOID(t *testing.T, p *fixture.Pair, b fixture.Backend, name string) int64 {
	t.Helper()
	var oid int64
	if err := b.DB.QueryRowContext(p.Ctx, `SELECT oid FROM pg_catalog.pg_conversion WHERE conname=$1`, name).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || oid > 4294967295 {
		t.Fatal("invalid conversion identity", oid)
	}
	return oid
}

func conversionCount(name string, count int64) fixture.Step {
	return fixture.Select(`SELECT count(*) FROM pg_catalog.pg_conversion WHERE conname=$1`, []string{"count"}, []string{"INT8"}, [][]any{{count}}, name)
}

func conversionValue() fixture.Step {
	return fixture.Select(`SELECT convert_to('é','LATIN1') AS v`, []string{"v"}, []string{"BYTEA"}, [][]any{{"\xe9"}})
}

func conversionMissingDefault() fixture.Diagnostic {
	return conversionDiagnostic("42883", `default conversion function for encoding "UTF8" to "LATIN1" does not exist`)
}

func conversionEuro() fixture.Diagnostic {
	return conversionDiagnostic("22P05", `character with byte sequence 0xe2 0x82 0xac in encoding "UTF8" has no equivalent in encoding "LATIN1"`)
}

// A qualified ordinary relation supplies independent namespace authority through
// supported REGCLASS/pg_class seams. Retire it before dependency/owner assertions.
func conversionNamespaceOID(t *testing.T, p *fixture.Pair, b fixture.Backend, schema string) int64 {
	t.Helper()
	witness := conversionQuote(schema) + "." + conversionQuote(fmt.Sprintf("__conv_namespace_%d", time.Now().UnixNano()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := b.DB.ExecContext(ctx, "DROP TABLE IF EXISTS "+witness); err != nil {
			t.Error("retiring namespace witness", err)
		}
	})
	conversionExec(t, p, b, "CREATE TABLE "+witness+"(v INTEGER)")
	var oid int64
	if err := b.DB.QueryRowContext(p.Ctx, `SELECT relnamespace::bigint FROM pg_catalog.pg_class WHERE oid=$1::regclass::oid`, witness).Scan(&oid); err != nil {
		t.Fatal("capturing independent namespace identity", err)
	}
	conversionExec(t, p, b, "DROP TABLE "+witness)
	if oid == 0 {
		t.Fatal("zero namespace identity", schema)
	}
	return oid
}

// Register BEFORE moving the pinned seed. Cleanup uses a fresh bounded context,
// not the possibly canceled scenario context, and runs before schema teardown.
// Metadata lookup makes repair independent of which assertion/DDL phase failed.
func conversionHideSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, schema string) {
	t.Helper()
	conversionHideNamedSeed(t, p, b, schema, "utf8_to_iso_8859_1")
}

func conversionHideNamedSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, schema, seed string) {
	t.Helper()
	hiddenOID := conversionNamespaceOID(t, p, b, schema)
	var oid int64
	var name, owner string
	if err := b.DB.QueryRowContext(p.Ctx, `SELECT oid,conname FROM pg_catalog.pg_conversion WHERE conname=$1`, seed).Scan(&oid, &name); err != nil {
		t.Fatal(err)
	}
	if err := b.DB.QueryRowContext(p.Ctx, `SELECT current_user`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var currentName string
		var currentNamespace int64
		err := b.DB.QueryRowContext(ctx, `SELECT conname,connamespace::bigint FROM pg_catalog.pg_conversion WHERE oid=$1`, oid).Scan(&currentName, &currentNamespace)
		if err != nil {
			t.Error("locating pinned conversion for repair", err)
			return
		}
		if currentNamespace != 11 && currentNamespace != hiddenOID {
			// Native Restore may remint this user namespace. Resolve its known
			// qualified name independently, never infer it from the conversion.
			witness := conversionQuote(schema) + "." + conversionQuote(fmt.Sprintf("__conv_namespace_repair_%d", time.Now().UnixNano()))
			defer func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cleanupCancel()
				if _, err := b.DB.ExecContext(cleanupCtx, "DROP TABLE IF EXISTS "+witness); err != nil {
					t.Error("retiring repair namespace witness", err)
				}
			}()
			if _, err = b.DB.ExecContext(ctx, "CREATE TABLE "+witness+"(v INTEGER)"); err != nil {
				t.Error("resolving restored hidden namespace", err)
				return
			}
			if err = b.DB.QueryRowContext(ctx, `SELECT relnamespace::bigint FROM pg_catalog.pg_class WHERE oid=$1::regclass::oid`, witness).Scan(&hiddenOID); err != nil {
				t.Error("capturing restored hidden namespace", err)
				return
			}
			if _, err = b.DB.ExecContext(ctx, "DROP TABLE "+witness); err != nil {
				t.Error("retiring restored namespace witness", err)
				return
			}
		}
		currentSchema := "pg_catalog"
		if currentNamespace == hiddenOID {
			currentSchema = schema
		} else if currentNamespace != 11 {
			t.Error("pinned conversion moved to unexpected namespace", currentNamespace, hiddenOID)
			return
		}
		qualified := conversionQuote(currentSchema) + "." + conversionQuote(currentName)
		if _, err = b.DB.ExecContext(ctx, "ALTER CONVERSION "+qualified+" OWNER TO "+conversionQuote(owner)); err != nil {
			t.Error("repairing pinned owner", err)
		}
		if currentSchema != "pg_catalog" {
			if _, err = b.DB.ExecContext(ctx, "ALTER CONVERSION "+qualified+" SET SCHEMA pg_catalog"); err != nil {
				t.Error("repairing pinned namespace", err)
				return
			}
		}
		if currentName != name {
			if _, err = b.DB.ExecContext(ctx, "ALTER CONVERSION pg_catalog."+conversionQuote(currentName)+" RENAME TO "+conversionQuote(name)); err != nil {
				t.Error("repairing pinned name", err)
			}
		}
	})
	conversionExec(t, p, b, "ALTER CONVERSION pg_catalog."+conversionQuote(seed)+" SET SCHEMA "+conversionQuote(schema))
}

func conversionQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func conversionHeldExec(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, q string) {
	t.Helper()
	stmt, err := conn.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal("DDL Prepare", q, err)
	}
	defer stmt.Close()
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
}

func conversionNotice(t *testing.T, p *fixture.Pair, b fixture.Backend, q, message string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		var notices []fixture.Diagnostic
		_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		s := fixture.Exec(q, 0)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, conn, recorder, s)
		want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("notice record got %#v want %#v", notices, want)
		}
	}
}

func conversionMetadata(oid int64, name string, namespace int64, def bool) fixture.Step {
	return fixture.Select(`SELECT c.oid::bigint AS oid,c.conname,c.connamespace::bigint AS namespace,c.conforencoding,c.contoencoding,c.conproc,c.conproc::oid AS proc_oid,c.conproc::text AS proc_text,c.condefault FROM pg_catalog.pg_conversion c WHERE c.oid=$1`,
		[]string{"oid", "conname", "namespace", "conforencoding", "contoencoding", "conproc", "proc_oid", "proc_text", "condefault"},
		[]string{"INT8", "NAME", "INT8", "INT4", "INT4", "REGPROC", "OID", "TEXT", "BOOL"},
		[][]any{{oid, name, namespace, int64(6), int64(8), "utf8_to_iso8859_1", int64(4375), "utf8_to_iso8859_1", def}}, oid)
}

func conversionSchemaNames(prefix string) []string {
	return []string{fmt.Sprintf("%s_old", prefix), fmt.Sprintf("%s_new", prefix)}
}
