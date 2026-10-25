package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func pgcryptoSHASaltShape(t *testing.T, salt, id string, rounds int) {
	t.Helper()
	prefix := fmt.Sprintf("$%s$rounds=%d$", id, rounds)
	if len(salt) != len(prefix)+16 || !strings.HasPrefix(salt, prefix) {
		t.Fatalf("SHA salt=%q want prefix=%q and 16 salt characters", salt, prefix)
	}
	for _, ch := range salt[len(prefix):] {
		if !strings.ContainsRune("./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz", ch) {
			t.Fatalf("SHA salt alphabet=%q", salt)
		}
	}
}

func TestReferencePgcryptoPasswordSHASaltGeneration(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		q := "SELECT gen_salt($1,$2) AS value"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for i, name := range []string{"sha256crypt", "sha512crypt"} {
			id := fmt.Sprint(i + 5)
			for _, count := range []int{0, 1000, 5000, 999999999} {
				for _, mode := range []string{"direct", "prepared", "held"} {
					t.Run(fmt.Sprintf("%s_%d_%s", name, count, mode), func(t *testing.T) {
						var salt string
						var err error
						switch mode {
						case "direct":
							err = c.QueryRowContext(p.Ctx, q, name, count).Scan(&salt)
						case "prepared":
							fresh, e := c.PrepareContext(p.Ctx, q)
							if e != nil {
								t.Fatal(e)
							}
							defer fresh.Close()
							err = fresh.QueryRowContext(p.Ctx, name, count).Scan(&salt)
						case "held":
							err = stmt.QueryRowContext(p.Ctx, name, count).Scan(&salt)
						}
						if err != nil {
							t.Fatal(err)
						}
						rounds := count
						if rounds == 0 {
							rounds = 5000
						}
						pgcryptoSHASaltShape(t, salt, id, rounds)
						// Maximum is generation-only, NEVER a successful hashing input.
						if count == 999999999 {
							return
						}
						size := int64(75)
						if id == "6" {
							size = 118
						}
						s := fixture.Select("SELECT length(crypt('password',$1)) AS size,crypt('password',crypt('password',$1))=crypt('password',$1) AS correct,crypt('wrong',crypt('password',$1))=crypt('password',$1) AS wrong", []string{"size", "correct", "wrong"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{size, true, false}}, salt)
						fixture.RunStep(t, p.Ctx, c, r, s)
					})
				}
			}
			for _, spelling := range []string{name, strings.ToUpper(name)} {
				var salt string
				if err := c.QueryRowContext(p.Ctx, "SELECT gen_salt($1)", spelling).Scan(&salt); err != nil {
					t.Fatal(err)
				}
				pgcryptoSHASaltShape(t, salt, id, 5000)
			}
			for _, count := range []int64{-2147483648, -1, 1, 999, 1000000000, 2147483647} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: "gen_salt: Incorrect number of rounds"}, name, count)
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, r, s)
				fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, r, s)
			}
			for _, args := range [][]any{{nil, -1}, {name, nil}, {nil, nil}} {
				pgcryptoPasswordText(t, p, c, r, q, nil, args...)
			}
			for _, bad := range []string{" " + name, name + " ", name + "x"} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: "gen_salt: Unknown salt algorithm"}, bad, -1)
				fixture.RunStep(t, p.Ctx, c, r, s)
				fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
			}
			for _, cast := range []string{"1000::smallint", "'1000'"} {
				var salt string
				if err := c.QueryRowContext(p.Ctx, "SELECT gen_salt('"+name+"',"+cast+")").Scan(&salt); err != nil {
					t.Fatal(err)
				}
				pgcryptoSHASaltShape(t, salt, id, 1000)
			}
			for _, typ := range []string{"bigint", "numeric"} {
				s := extensionMissing("SELECT gen_salt('"+name+"',1000::"+typ+")", "gen_salt", "unknown, "+typ)
				fixture.RunStep(t, p.Ctx, c, r, s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, c, r, s)
			}
		}
		pgcryptoPasswordText(t, p, c, r, "SELECT gen_salt(NULL::text) AS value", nil)
	})
}
