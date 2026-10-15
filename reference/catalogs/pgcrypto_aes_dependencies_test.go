package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func pgcryptoAESDependencies(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	ids := pgcryptoProcedureIDs(t, p, c)
	type consumer struct {
		query     string
		class     int64
		functions []string
	}
	consumers := []consumer{
		{"SELECT oid FROM pg_attrdef WHERE adrelid='pgcrypto_aes_stored'::regclass::oid AND adnum=3", 2604, []string{"encrypt(bytea,bytea,text)"}},
		{"SELECT oid FROM pg_attrdef WHERE adrelid='pgcrypto_aes_stored'::regclass::oid AND adnum=4", 2604, []string{"encrypt_iv(bytea,bytea,bytea,text)"}},
		{"SELECT oid FROM pg_constraint WHERE conrelid='pgcrypto_aes_atomic'::regclass::oid AND conname='pgcrypto_aes_valid'", 2606, []string{"encrypt(bytea,bytea,text)"}},
		{"SELECT oid FROM pg_class WHERE oid='pgcrypto_aes_idx'::regclass::oid", 1259, []string{"encrypt_iv(bytea,bytea,bytea,text)"}},
		{"SELECT oid FROM pg_rewrite WHERE ev_class='pgcrypto_aes_view'::regclass::oid AND rulename='_RETURN'", 2618, []string{"decrypt(bytea,bytea,text)", "decrypt_iv(bytea,bytea,bytea,text)"}},
		{"SELECT oid FROM pg_rewrite WHERE ev_class='pgcrypto_aes_nested'::regclass::oid AND rulename='_RETURN'", 2618, []string{"decrypt_iv(bytea,bytea,bytea,text)"}},
	}
	var addresses []int64
	for _, consumer := range consumers {
		var id int64
		if err := c.QueryRowContext(p.Ctx, consumer.query).Scan(&id); err != nil || id <= 0 {
			t.Fatal("AES consumer identity", consumer.query, id, err)
		}
		addresses = append(addresses, id)
		want := [][]any{}
		for _, signature := range consumer.functions {
			proc := ids["pgcrypto_part."+signature]
			if proc <= 0 {
				t.Fatal("missing actual AES procedure", signature)
			}
			want = append(want, fixture.PgDependEdge(consumer.class, id, 0, 1255, proc, 0, "n"))
		}
		step := fixture.Select("SELECT classid::bigint AS classid,objid::bigint AS objid,objsubid,refclassid::bigint AS refclassid,refobjid::bigint AS refobjid,refobjsubid,deptype FROM pg_depend WHERE classid=$1 AND objid=$2 AND refclassid=1255 AND deptype='n'", []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"}, want, consumer.class, id)
		step.Bag = true
		fixture.RunStep(t, p.Ctx, c, r, step)
	}
	var stored int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE oid='pgcrypto_aes_stored'::regclass::oid").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	for i, kind := range []string{"a", "i"} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=2604 AND objid=$1 AND objsubid=0 AND refclassid=1259 AND refobjid=$2 AND refobjsubid=$3 AND deptype=$4", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, addresses[i], stored, i+3, kind))
	}
	for i, name := range []string{"pgcrypto_aes_view", "pgcrypto_aes_nested"} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=2618 AND objid=$1 AND objsubid=0 AND refclassid=1259 AND refobjid=$2::regclass::oid AND refobjsubid=0 AND deptype='i'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, addresses[4+i], name))
	}
}
