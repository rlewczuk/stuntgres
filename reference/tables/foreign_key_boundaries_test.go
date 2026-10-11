package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceForeignKeyDefinitionIneligibleKeys(t *testing.T) {
	for _, index := range []string{"CREATE INDEX candidate ON p(id)", "CREATE UNIQUE INDEX candidate ON p(id) WHERE id>0", "CREATE UNIQUE INDEX candidate ON p((id+1))"} {
		t.Run(index, func(t *testing.T) {
			foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(id INT)", 0))
				if strings.Contains(index, "(id+1)") && !b.PG {
					d := foreignKeyError("0A000", "unique expression indexes are unsupported", "", "", "")
					d.Position = fixture.Position(strings.Index(index, "(id+1)") + 1)
					run(foreignKeyFail(index, d, false))
					run(fixture.Select("SELECT indexname FROM pg_indexes WHERE indexname='candidate'", []string{"indexname"}, []string{"NAME"}, nil))
				} else {
					run(fixture.Exec(index, 0))
				}
				for _, prepared := range []bool{false, true} {
					run(foreignKeyFail("CREATE TABLE c(pid INT REFERENCES p(id))", foreignKeyError("42830", `there is no unique constraint matching given keys for referenced table "p"`, "", "", ""), prepared))
				}
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='c'", []string{"relname"}, []string{"NAME"}, nil))
			})
		})
	}
}

func TestReferenceForeignKeyDefinitionTypes(t *testing.T) {
	for i, tc := range []struct{ parent, child, value, missing, output string }{
		{"INT", "BIGINT", "1", "2147483648", "2147483648"}, {"BIGINT", "INT", "1", "9", "9"}, {"SMALLINT", "SMALLINT", "1", "9", "9"},
		{"NUMERIC", "NUMERIC", "1.00", "9.50", "9.50"}, {"TEXT", "TEXT", "'x'", "'absent'", "absent"},
		{"VARCHAR(8)", "VARCHAR(8)", "'x'", "'absent'", "absent"}, {"CHAR(8)", "CHAR(8)", "'x'", "'absent'", "absent  "},
		{"TEXT", "VARCHAR(8)", "'x'", "'absent'", "absent"},
		{"BOOLEAN", "BOOLEAN", "TRUE", "FALSE", "f"}, {"DOUBLE PRECISION", "DOUBLE PRECISION", "'NaN'", "9", "9"},
		{"REAL", "REAL", "'-0'", "9", "9"}, {"DOUBLE PRECISION", "REAL", "'NaN'", "9", "9"},
		{"DATE", "DATE", "'2026-01-01'", "'2026-01-02'", "2026-01-02"},
		{"DATE", "TIMESTAMP", "'2026-01-01'", "'2026-01-02 00:00:00'", "2026-01-02 00:00:00"},
		{"TIME", "TIME", "'12:00'", "'13:00'", "13:00:00"},
		{"TIMESTAMP", "TIMESTAMP", "'2026-01-01 12:00'", "'2026-01-02 12:00'", "2026-01-02 12:00:00"},
		{"TIMESTAMPTZ", "TIMESTAMPTZ", "'2026-01-01 12:00+00'", "'2026-01-02 12:00+00'", "2026-01-02 12:00:00+00"},
		{"TIMETZ", "TIMETZ", "'12:00+00'", "'13:00+00'", "13:00:00+00"},
		{"INTERVAL", "INTERVAL", "'1 day'", "'2 days'", "2 days"},
		{"MONEY", "MONEY", "'1.00'", "'9.00'", "$9.00"},
		{"UUID", "UUID", "'00000000-0000-0000-0000-000000000001'", "'00000000-0000-0000-0000-000000000002'", "00000000-0000-0000-0000-000000000002"},
		{"INET", "INET", "'192.0.2.1'", "'192.0.2.2'", "192.0.2.2"}, {"CIDR", "CIDR", "'192.0.2.0/24'", "'198.51.100.0/24'", "198.51.100.0/24"},
		{"INET", "CIDR", "'192.0.2.0/24'", "'198.51.100.0/24'", "198.51.100.0/24"},
		{"JSONB", "JSONB", `'{"a":1}'`, `'{"a":9}'`, `{"a": 9}`},
		{"BYTEA", "BYTEA", `'\x01'`, `'\x09'`, `\x09`},
		{`"char"`, `"char"`, "'x'", "'z'", "z"}, {"NAME", "NAME", "'x'", "'absent'", "absent"},
		{"OID", "OID", "1", "9", "9"}, {"REGCLASS", "REGCLASS", "'1'", "'9'", "9"}, {"REGTYPE", "REGTYPE", "'1'", "'9'", "9"},
		{"CIDR", "INET", "'192.0.2.0/24'", "'192.0.2.1/24'", "192.0.2.1/24"},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				if tc.parent == `"char"` && !b.PG {
					q := "CREATE TABLE p(id " + tc.parent + " PRIMARY KEY)"
					d := foreignKeyError("0A000", "unsupported column type", "", "", "")
					d.Position = fixture.Position(strings.Index(q, tc.parent) + 1)
					for _, prepared := range []bool{false, true} {
						run(foreignKeyFail(q, d, prepared))
					}
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='p' OR relname='c'", []string{"relname"}, []string{"NAME"}, nil))
					return
				}
				run(fixture.Exec("CREATE TABLE p(id "+tc.parent+" PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE c(pid "+tc.child+" CONSTRAINT fk REFERENCES p)", 0))
				run(fixture.Exec("INSERT INTO p VALUES("+tc.value+")", 1))
				run(fixture.Exec("INSERT INTO c VALUES("+tc.value+")", 1))
				for _, prepared := range []bool{false, true} {
					run(foreignKeyFail("INSERT INTO c VALUES("+tc.missing+")", foreignKeyChild("c", "fk", "Key (pid)=("+tc.output+") is not present in table \"p\"."), prepared))
				}
				run(fixture.Select("SELECT count(*)::int FROM c", []string{"count"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
}

func TestReferenceForeignKeyBoundariesModes(t *testing.T) {
	for _, clause := range []string{"ON DELETE CASCADE", "ON UPDATE CASCADE", "ON DELETE RESTRICT", "ON DELETE SET NULL", "ON DELETE SET DEFAULT", "DEFERRABLE", "INITIALLY DEFERRED"} {
		t.Run(clause, func(t *testing.T) {
			foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				q := "CREATE TABLE c(pid INT DEFAULT 1 REFERENCES p " + clause + ")"
				if b.PG {
					run(fixture.Exec(q, 0))
					run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
					run(fixture.Exec("INSERT INTO c VALUES(2)", 1))
					switch clause {
					case "ON DELETE CASCADE":
						run(fixture.Exec("DELETE FROM p WHERE id=2", 1))
						run(foreignKeyIntRows("SELECT pid FROM c", "pid"))
					case "ON UPDATE CASCADE":
						run(fixture.Exec("UPDATE p SET id=3 WHERE id=2", 1))
						run(foreignKeyIntRows("SELECT pid FROM c", "pid", 3))
					case "ON DELETE SET NULL":
						run(fixture.Exec("DELETE FROM p WHERE id=2", 1))
						run(fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"INT4"}, [][]any{{nil}}))
					case "ON DELETE SET DEFAULT":
						run(fixture.Exec("DELETE FROM p WHERE id=2", 1))
						run(foreignKeyIntRows("SELECT pid FROM c", "pid", 1))
					case "ON DELETE RESTRICT":
						d := foreignKeyParent("p", "c", "c_pid_fkey", `Key (id)=(2) is referenced from table "c".`)
						d.Code = "23001"
						d.Message = `update or delete on table "p" violates RESTRICT setting of foreign key constraint "c_pid_fkey" on table "c"`
						run(foreignKeyFail("DELETE FROM p WHERE id=2", d, false))
						run(foreignKeyIntRows("SELECT pid FROM c", "pid", 2))
					default:
						tx, err := b.DB.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SET CONSTRAINTS ALL DEFERRED", 0))
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO c VALUES(9)", 1))
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO p VALUES(9)", 1))
						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
						run(foreignKeyIntRows("SELECT pid FROM c ORDER BY pid", "pid", 2, 9))
					}
					return
				}
				message, token := "foreign key referential actions are unsupported", ""
				if clause == "DEFERRABLE" {
					message, token = "foreign key deferrability is unsupported", "DEFERRABLE"
				} else if clause == "INITIALLY DEFERRED" {
					message, token = "foreign key deferrability is unsupported", "DEFERRED"
				} else {
					token = strings.Fields(clause)[2]
				}
				d := foreignKeyError("0A000", message, "", "", "")
				d.Position = fixture.Position(strings.Index(q, token) + 1)
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, d)
					s.Prepared = prepared
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
				}
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='c'", []string{"relname"}, []string{"NAME"}, nil))
			})
		})
	}
}

func TestReferenceForeignKeyBoundariesPartition(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		q := "CREATE TABLE c(pid INT REFERENCES p) PARTITION BY RANGE(pid)"
		if !b.PG {
			run(foreignKeyFail(q, foreignKeyError("0A000", "foreign keys involving partition hierarchies are unsupported", "", "", ""), false))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='c'", []string{"relname"}, []string{"NAME"}, nil))
			return
		}
		run(fixture.Exec(q, 0))
		run(fixture.Exec("CREATE TABLE c1 PARTITION OF c FOR VALUES FROM(0) TO(10)", 0))
		run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
		run(foreignKeyFail("INSERT INTO c VALUES(9)", foreignKeyChild("c1", "c_pid_fkey", `Key (pid)=(9) is not present in table "p".`), false))
		run(foreignKeyIntRows("SELECT pid FROM c", "pid", 1))
	})
}

func TestReferenceForeignKeyBoundariesTemporal(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		q := "CREATE TABLE c(id INT,valid daterange,FOREIGN KEY(id,PERIOD valid) REFERENCES p(id,PERIOD valid))"
		if !b.PG {
			d := foreignKeyError("0A000", "temporal foreign keys are unsupported", "", "", "")
			d.Position = fixture.Position(strings.Index(q, "PERIOD") + 1)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, d)
				s.Prepared = prepared
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
			return
		}
		// Real PostgreSQL18 temporal key setup, not a fake PG syntax exclusion.
		run(fixture.Exec("CREATE EXTENSION btree_gist", 0))
		defer func() {
			if _, err := b.DB.ExecContext(p.Ctx, "DROP TABLE IF EXISTS c,p CASCADE; DROP EXTENSION btree_gist CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		run(fixture.Exec("CREATE TABLE p(id INT,valid daterange,PRIMARY KEY(id,valid WITHOUT OVERLAPS))", 0))
		run(fixture.Exec(q, 0))
		run(fixture.Exec("INSERT INTO p VALUES(1,'[2026-01-01,2026-02-01)')", 1))
		run(fixture.Exec("INSERT INTO c VALUES(1,'[2026-01-05,2026-01-10)')", 1))
		run(fixture.Select("SELECT count(*)::int FROM c", []string{"count"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}

func TestReferenceForeignKeyBoundariesTemporary(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		step(fixture.Exec("CREATE TEMP TABLE tp(id INT PRIMARY KEY)", 0))
		step(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		step(foreignKeyFail("CREATE TABLE bad(pid INT REFERENCES tp)", foreignKeyError("42P16", "constraints on permanent tables may reference only permanent tables", "", "", ""), false))
		step(foreignKeyFail("CREATE TEMP TABLE bad(pid INT REFERENCES p)", foreignKeyError("42P16", "constraints on temporary tables may reference only temporary tables", "", "", ""), false))
		step(fixture.Exec("CREATE TEMP TABLE tc(pid INT CONSTRAINT fk REFERENCES tp)", 0))
		step(fixture.Exec("INSERT INTO tp VALUES(1)", 1))
		step(fixture.Exec("INSERT INTO tc VALUES(1)", 1))
		d := foreignKeyChild("tc", "fk", `Key (pid)=(9) is not present in table "tp".`)
		// Physical temp namespace names are backend-owned and intentionally not
		// compared as portable names. The remaining diagnostic stays exact.
		_, err = conn.ExecContext(p.Ctx, "INSERT INTO tc VALUES(9)")
		got := fixture.ExtractDiagnostic(t, err)
		if !strings.HasPrefix(got.Schema, "pg_temp_") {
			t.Fatal(got)
		}
		got.Schema = "public"
		if got != d {
			t.Fatal(got, d)
		}
		step(foreignKeyIntRows("SELECT pid FROM tc", "pid", 1))
		step(fixture.Exec("DROP TABLE tc,tp", 0))
	})
}

func TestReferenceForeignKeyDefinitionProviders(t *testing.T) {
	for _, provider := range []string{"enum", "domain", "text_pattern", "bpchar_pattern", "oid_ops"} {
		t.Run(provider, func(t *testing.T) {
			foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				typ, value, missing, output, index := "TEXT", "'x'", "'absent'", "absent", ""
				switch provider {
				case "enum":
					run(fixture.Exec("CREATE TYPE fk_enum AS ENUM('x','absent')", 0))
					typ = "fk_enum"
				case "domain":
					run(fixture.Exec("CREATE DOMAIN fk_domain AS INT", 0))
					typ, value, missing, output = "fk_domain", "1", "9", "9"
				case "text_pattern":
					index = "CREATE UNIQUE INDEX key_index ON p(id text_pattern_ops)"
				case "bpchar_pattern":
					typ, value, missing, output, index = "CHAR(8)", "'x'", "'absent'", "absent  ", "CREATE UNIQUE INDEX key_index ON p(id bpchar_pattern_ops)"
				case "oid_ops":
					typ, value, missing, output, index = "INT", "-1", "9", "9", "CREATE UNIQUE INDEX key_index ON p(id oid_ops)"
				}
				if index == "" {
					run(fixture.Exec("CREATE TABLE p(id "+typ+" PRIMARY KEY)", 0))
				} else {
					run(fixture.Exec("CREATE TABLE p(id "+typ+")", 0))
					run(fixture.Exec(index, 0))
				}
				run(fixture.Exec("CREATE TABLE c(pid "+typ+" CONSTRAINT fk REFERENCES p(id))", 0))
				run(fixture.Exec("INSERT INTO p VALUES("+value+")", 1))
				run(fixture.Exec("INSERT INTO c VALUES("+value+")", 1))
				for _, prepared := range []bool{false, true} {
					run(foreignKeyFail("INSERT INTO c VALUES("+missing+")", foreignKeyChild("c", "fk", "Key (pid)=("+output+") is not present in table \"p\"."), prepared))
				}
				run(fixture.Select("SELECT count(*)::int FROM c", []string{"count"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
}

func TestReferenceForeignKeyNetworkSupportingKeys(t *testing.T) {
	for _, tc := range []struct{ typ, first, equivalent, missing, output, absent string }{
		{"INET", "'2001:db8::1/64'", "'2001:0db8:0:0:0:0:0:1/64'", "'2001:db8::2/64'", "2001:db8::1/64", "2001:db8::2/64"},
		{"CIDR", "'2001:db8::/64'", "'2001:0db8:0000:0000::/64'", "'2001:db9::/64'", "2001:db8::/64", "2001:db9::/64"},
	} {
		for _, standalone := range []bool{false, true} {
			t.Run(tc.typ+fmt.Sprint("/standalone=", standalone), func(t *testing.T) {
				foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					key := "p_pkey"
					if standalone {
						key = "network_key"
						run(fixture.Exec("CREATE TABLE p(id "+tc.typ+")", 0))
						run(fixture.Exec("CREATE UNIQUE INDEX network_key ON p(id inet_ops)", 0))
					} else {
						run(fixture.Exec("CREATE TABLE p(id "+tc.typ+" PRIMARY KEY)", 0))
					}
					run(fixture.Exec("CREATE TABLE c(pid "+tc.typ+" CONSTRAINT fk REFERENCES p(id))", 0))
					run(fixture.Exec("INSERT INTO p VALUES("+tc.first+")", 1))
					run(fixture.Exec("INSERT INTO c VALUES("+tc.equivalent+")", 1))
					for _, prepared := range []bool{false, true} {
						run(foreignKeyFail("INSERT INTO p VALUES("+tc.equivalent+")", fixture.UniqueReferenceDuplicate("p", key, "Key (id)=("+tc.output+") already exists."), prepared))
						run(foreignKeyFail("INSERT INTO c VALUES("+tc.missing+")", foreignKeyChild("c", "fk", "Key (pid)=("+tc.absent+") is not present in table \"p\"."), prepared))
					}
					run(fixture.Select("SELECT id::text AS key_text FROM p", []string{"key_text"}, []string{"TEXT"}, [][]any{{tc.output}}))
					run(fixture.Select("SELECT pid::text AS key_text FROM c", []string{"key_text"}, []string{"TEXT"}, [][]any{{tc.output}}))
				})
			})
		}
	}
}
