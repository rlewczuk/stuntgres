package fixture

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"stuntgres"
)

// ExternalSQLTrustAuthentication installs a role-scoped real HBA trust rule in
// the disposable oracle. It does not infer authentication from configuration:
// callers must additionally observe the genuine successful handshake. Existing
// auth-file fixture ownership restores the original file before cleanup reloads.
func ExternalSQLTrustAuthentication(t *testing.T, p *Pair, role string) {
	t.Helper()
	externalSQLAuthentication(t, p, role, "trust")
}

// ExternalSQLPasswordAuthentication installs a real role-scoped SCRAM rule on
// both loopback and mapped TCP endpoints. Observe an actual successful SCRAM
// handshake before returning; a supplied password or reloaded HBA is not proof.
func ExternalSQLPasswordAuthentication(t *testing.T, p *Pair, role, password string) {
	t.Helper()
	externalSQLAuthentication(t, p, role, "scram-sha-256")
	provider := &ExternalSQLProvider{}
	for {
		connection, err := provider.Open(p.Ctx, stuntgres.ExternalSQLConnectRequest{
			MappingOptions: []stuntgres.ForeignOption{{Name: "user", Value: role}, {Name: "password", Value: password}},
		})
		if err != nil {
			if connection != nil {
				_ = connection.Close(context.WithoutCancel(p.Ctx))
			}
			t.Fatal("password HBA handshake:", err)
		}
		mechanism := connection.Info().CredentialMechanism
		if err := connection.Close(context.WithoutCancel(p.Ctx)); err != nil {
			t.Fatal(err)
		}
		if mechanism == "scram-sha-256" {
			return
		}
		if mechanism != "none" {
			t.Fatal("password HBA handshake mechanism:", mechanism)
		}
		if err := p.Ctx.Err(); err != nil {
			t.Fatal("SCRAM HBA was not activated:", err)
		}
		runtime.Gosched()
	}
}

func externalSQLAuthentication(t *testing.T, p *Pair, role, method string) {
	t.Helper()
	if strings.ContainsAny(role, " \t\r\n\"\\") || role == "" {
		t.Fatal("authentication fixture requires a simple role name")
	}
	if p.Postgres == nil {
		t.Fatal("authentication fixture requires real PostgreSQL")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), Liveness)
		defer cancel()
		if _, err := Admin.ExecContext(ctx, "SELECT pg_reload_conf()"); err != nil {
			t.Error("restored HBA reload", err)
		}
	})
	f := &authFiles{nativeRoot: t.TempDir()}
	preparePostgresAuth(t, p, f, "hba")
	original := authExec(t, p.Ctx, "cat", f.postgres)
	f.Write(t, p.Ctx, Backend{PG: true}, "", "host all "+role+" all "+method+"\n"+string(original))
	if _, err := Admin.ExecContext(p.Ctx, "SELECT pg_reload_conf()"); err != nil {
		t.Fatal(err)
	}
}
