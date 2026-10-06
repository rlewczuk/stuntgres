package stuntgres

import (
	"context"
	"strings"
	"testing"
)

func TestControlTransactionsHostAuthorization(t *testing.T) {
	ctx := context.Background()
	d := NewDriver()
	adminPool := rolePool(t, d, t.Name(), "postgres")
	admin, err := adminPool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	for _, q := range []string{"CREATE USER owner", "CREATE USER reader", "SET SESSION AUTHORIZATION owner", "CREATE TABLE secret(id INT)", "INSERT INTO secret VALUES(7)"} {
		n := int64(0)
		if strings.HasPrefix(q, "INSERT") {
			n = 1
		}
		rolePublicCommand(t, admin, q, n, false)
	}
	pool := rolePool(t, d, t.Name(), "reader")
	pool.SetMaxOpenConns(2)
	a, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	rolePublicCommand(t, a, "SET application_name TO retained", 0, false)
	for _, q := range []string{"SELECT id FROM secret", "INSERT INTO secret VALUES(8)"} {
		s, err := a.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal("valid Prepare must succeed", err)
		}
		_, err = s.ExecContext(ctx)
		s.Close()
		rolePublicDiagnostic(t, err, "42501", "permission denied for table secret")
	}
	if err = Reset(ctx, pool); err != nil {
		t.Fatal("host reset depends on SQL role", err)
	}
	if err = Restore(ctx, pool, strings.NewReader("SET SESSION AUTHORIZATION owner; CREATE TABLE secret(id INT); INSERT INTO secret VALUES(99);")); err != nil {
		t.Fatal("host restore depends on SQL role", err)
	}
	rolePublicIdentity(t, a, "reader", nil)
	var setting string
	if err = a.QueryRowContext(ctx, "SHOW application_name").Scan(&setting); err != nil || setting != "retained" {
		t.Fatal(setting, err)
	}
	_, err = a.ExecContext(ctx, "INSERT INTO secret VALUES(8)")
	rolePublicDiagnostic(t, err, "42501", "permission denied for table secret")
}
