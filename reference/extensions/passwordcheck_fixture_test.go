package reference_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/base64"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
)

const passwordCheckSetting = "passwordcheck.min_password_length"

func passwordCheckScenario(t *testing.T, roles []string, body func(*fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, *[]fixture.Diagnostic)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, roles)
	p := fixture.NewScenarioWithDriverConfig(t, (*stuntgres.Driver).RegisterPasswordCheck)
	for _, b := range p.Backends() {
		var notices []fixture.Diagnostic
		_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		body(p, b, c, recorder, &notices)
	}
}

func passwordCheckRun(t *testing.T, p *fixture.Pair, e fixture.Executor, r *fixture.Recorder, prepared bool) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) {
		s.Prepared = prepared || s.Prepared
		if s.State != "" && s.ErrorStage == "" {
			s.ErrorStage = "execute"
		}
		fixture.RunStep(t, p.Ctx, e, r, s)
	}
}

func passwordCheckError(query, message, detail string) fixture.Step {
	return fixture.ImplicitError(query, fixture.Diagnostic{Code: "22023", Message: message, Detail: detail})
}

func passwordCheckShort(query string, minimum int) fixture.Step {
	return passwordCheckError(query, "password is too short", fmt.Sprintf(`password must be at least "passwordcheck.min_password_length" (%d) bytes long`, minimum))
}

func passwordCheckShow(value string) fixture.Step {
	return fixture.Select("SHOW "+passwordCheckSetting, []string{passwordCheckSetting}, []string{"TEXT"}, [][]any{{value}})
}

func passwordCheckMD5(role, password string) string {
	return fmt.Sprintf("md5%x", md5.Sum([]byte(password+role)))
}

func passwordCheckSCRAM(t *testing.T, password string) string {
	t.Helper()
	return fixture.RolePasswordEncoded(t, password, []byte("0123456789abcdef"), 4096)
}

func passwordCheckMD5Notice() fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01P01", Message: "setting an MD5-encrypted password", Detail: "MD5 password support is deprecated and will be removed in a future release of PostgreSQL.", Hint: "Refer to the PostgreSQL documentation for details about migrating to another password type."}
}

func passwordCheckNotices(t *testing.T, got []fixture.Diagnostic, want ...fixture.Diagnostic) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("password notices = %#v, want %#v", got, want)
	}
}

// PostgreSQL exposes the credential through pg_authid. Native deliberately has
// no password inspection export/catalog projection: shared public role identity,
// login/limit/authority witnesses complement the owning private unit assertions.
func passwordCheckReadCredential(t *testing.T, p *fixture.Pair, b fixture.Backend, role string) sql.NullString {
	t.Helper()
	if !b.PG {
		t.Fatal("credential projection is an oracle-only observation")
	}
	var got sql.NullString
	if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT rolpassword FROM pg_authid WHERE rolname="+pq.QuoteLiteral(role)).Scan(&got); err != nil {
		t.Fatal(err)
	}
	return got
}

func passwordCheckCredential(t *testing.T, p *fixture.Pair, b fixture.Backend, role string, want sql.NullString) {
	t.Helper()
	if b.PG {
		if got := passwordCheckReadCredential(t, p, b, role); got != want {
			t.Fatalf("credential for %q = %#v, want %#v", role, got, want)
		}
	}
}

// Independent RFC5802 equations verify both stored keys against the expected
// SASLprep bytes, rather than accepting an arbitrary generated verifier.
func passwordCheckPlainCredential(t *testing.T, p *fixture.Pair, b fixture.Backend, role, password string) {
	t.Helper()
	if !b.PG {
		return
	}
	v := passwordCheckReadCredential(t, p, b, role)
	parts := strings.Split(v.String, "$")
	if !v.Valid || len(parts) != 3 || parts[0] != "SCRAM-SHA-256" {
		t.Fatal("expected SCRAM credential", v)
	}
	middle, keys := strings.Split(parts[1], ":"), strings.Split(parts[2], ":")
	if len(middle) != 2 || len(keys) != 2 {
		t.Fatal("malformed generated SCRAM", v)
	}
	iterations, err := strconv.Atoi(middle[0])
	if err != nil || iterations != 4096 {
		t.Fatal("default iterations", middle[0], err)
	}
	salt, err := base64.StdEncoding.DecodeString(middle[1])
	if err != nil || len(salt) != 16 {
		t.Fatal("default salt", salt, err)
	}
	stored, server := fixture.RolePasswordKeys(t, password, salt, iterations)
	for i, want := range [][]byte{stored, server} {
		got, err := base64.StdEncoding.DecodeString(keys[i])
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("independent credential key %d for %q: %v", i, role, err)
		}
	}
}

func passwordCheckOID(t *testing.T, p *fixture.Pair, e interface{ QueryRowContext(context.Context, string, ...any) *sql.Row }, role string) int64 {
	t.Helper()
	var oid int64
	if err := e.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname="+pq.QuoteLiteral(role)).Scan(&oid); err != nil || oid <= 0 || oid > 4294967295 {
		t.Fatal("valid role OID", role, oid, err)
	}
	return oid
}
