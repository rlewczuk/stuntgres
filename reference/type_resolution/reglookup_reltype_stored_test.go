package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func regLookupRelTypeStoredSelect(b fixture.Backend, query string, names, types []string, values [][]any) fixture.Step {
	s := regLookupStoredSelect(b, query, names, types, values)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if types[column] != "REGTYPE" {
			return false
		}
		fixture.RegtypeRaw(t, b.PG, value)
		return true
	}
	prior := s.CheckMetadata
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		prior(t, rows)
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, column := range columns {
			var scan reflect.Type
			switch types[i] {
			case "REGCLASS", "REGNAMESPACE", "REGTYPE":
				scan = reflect.TypeFor[string]()
				if b.PG {
					scan = reflect.TypeFor[any]()
				}
			case "INT4":
				scan = reflect.TypeFor[int64]()
				if b.PG {
					scan = reflect.TypeFor[int32]()
				}
			default:
				continue
			}
			if column.ScanType() != scan {
				t.Errorf("%s stored ScanType=%v want %v", names[i], column.ScanType(), scan)
			}
			n, nk := column.Nullable()
			l, lk := column.Length()
			precision, scale, dk := column.DecimalSize()
			if n != !b.PG || nk != !b.PG || l != 0 || lk || precision != 0 || scale != 0 || dk {
				t.Errorf("%s stored flags nullable=%t/%t length=%d/%t decimal=%d,%d/%t", names[i], n, nk, l, lk, precision, scale, dk)
			}
		}
	}
	return s
}

func regLookupRelTypeCreateStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	regObjectExec(t, p, b, c, "CREATE SCHEMA rl_store", "CREATE TABLE rl_store.target(v integer)", "CREATE DOMAIN rl_store.d AS varchar(12)",
		`CREATE TABLE public.rl_store_data(id integer,c regclass DEFAULT to_regclass('rl_store.target'),n regnamespace DEFAULT to_regnamespace('rl_store'),t regtype DEFAULT to_regtype('rl_store.d'),m integer DEFAULT to_regtypemod('varchar(12)'))`,
		`CREATE VIEW public.rl_store_view AS SELECT to_regclass('rl_store.target') AS c,to_regnamespace('rl_store') AS n,to_regtype('rl_store.d') AS t,to_regtypemod('varchar(12)') AS m,to_regclass('rl_missing') AS cm,to_regnamespace('rl_missing') AS nm,to_regtype('rl_missing') AS tm,to_regtypemod('rl_missing') AS mm,to_regclass(NULL::text) AS cn,to_regnamespace(NULL::text) AS nn,to_regtype(NULL::text) AS tn,to_regtypemod(NULL::text) AS mn`)
	regObjectRunStep(t, p, b, c, fixture.Exec("INSERT INTO public.rl_store_data(id) VALUES(1)", 1))
}

func regLookupRelTypeStoredChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	values := [][]any{{"rl_store.target", "rl_store", "rl_store.d", int64(16)}}
	regObjectBoth(t, p, b, c, regLookupRelTypeStoredSelect(b, "SELECT c,n,t,m FROM public.rl_store_data ORDER BY id", []string{"c", "n", "t", "m"}, []string{"REGCLASS", "REGNAMESPACE", "REGTYPE", "INT4"}, values))
	regObjectBoth(t, p, b, c, regLookupRelTypeStoredSelect(b, "SELECT c,n,t,m,cm,nm,tm,mm,cn,nn,tn,mn FROM public.rl_store_view", []string{"c", "n", "t", "m", "cm", "nm", "tm", "mm", "cn", "nn", "tn", "mn"}, []string{"REGCLASS", "REGNAMESPACE", "REGTYPE", "INT4", "REGCLASS", "REGNAMESPACE", "REGTYPE", "INT4", "REGCLASS", "REGNAMESPACE", "REGTYPE", "INT4"}, [][]any{{"rl_store.target", "rl_store", "rl_store.d", int64(16), nil, nil, nil, nil, nil, nil, nil, nil}}))
	regObjectBoth(t, p, b, c, fixture.Select(`SELECT c::oid='rl_store.target'::regclass::oid AS c,n::oid='rl_store'::regnamespace::oid AS n,t::oid='rl_store.d'::regtype::oid AS t FROM public.rl_store_data WHERE id=1`, []string{"c", "n", "t"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
	regObjectRunStep(t, p, b, c, fixture.Exec("INSERT INTO public.rl_store_data(id) VALUES(2)", 1))
	regObjectBoth(t, p, b, c, regLookupRelTypeStoredSelect(b, "SELECT c,n,t,m FROM public.rl_store_data WHERE id=2", []string{"c", "n", "t", "m"}, []string{"REGCLASS", "REGNAMESPACE", "REGTYPE", "INT4"}, values))
	regObjectBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM public.rl_store_data", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
	regObjectRunStep(t, p, b, c, fixture.Exec("DELETE FROM public.rl_store_data WHERE id=2", 1))
}

func regLookupRelTypeStoredControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, emitted *[]byte, foreign bool) {
	t.Helper()
	if b.PG {
		t.Run("actual-native-stream-replay", func(t *testing.T) {
			if len(*emitted) == 0 {
				t.Fatal("actual native Dump unavailable; ordinary PostgreSQL checks ran independently")
			}
			regObjectExec(t, p, b, c, "DROP VIEW public.rl_store_view", "DROP TABLE public.rl_store_data", "DROP SCHEMA rl_store CASCADE")
			if _, err := c.ExecContext(p.Ctx, string(*emitted)); err != nil {
				t.Fatal("PostgreSQL rejected actual native Dump bytes", err)
			}
			regObjectExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
			regLookupRelTypeStoredChecks(t, p, b, c)
		})
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if dump.Len() == 0 {
		t.Fatal("empty actual Dump")
	}
	*emitted = bytes.Clone(dump.Bytes())
	held, err := c.QueryContext(p.Ctx, "SELECT c,n,t,m FROM public.rl_store_data ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	metadata := regLookupRelTypeStoredSelect(b, "", []string{"c", "n", "t", "m"}, []string{"REGCLASS", "REGNAMESPACE", "REGTYPE", "INT4"}, nil)
	metadata.CheckMetadata(t, held)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(*emitted)); err != nil {
		t.Fatal(err)
	}
	fixture.ArrayReferenceConsumeHeld(t, held, [][]any{{"rl_store.target", "rl_store", "rl_store.d", int64(16)}}, []string{"REGCLASS", "REGNAMESPACE", "REGTYPE", "INT4"})
	regLookupRelTypeStoredChecks(t, p, b, c)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(*emitted, redump.Bytes()) {
		t.Fatalf("unchanged Dump/Restore altered stream\nsource:\n%s\nrestored:\n%s", *emitted, redump.Bytes())
	}
	if foreign {
		regLookupRelTypeForeignControls(t, *emitted)
	}
}

func regLookupRelTypeForeignControls(t *testing.T, emitted []byte) {
	t.Helper()
	target := fixture.NewFixture(t, false, true)
	b := fixture.Backend{Name: "memory", DB: target.Memory, Recorder: target.MemoryRecord}
	c, err := target.Memory.Conn(target.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	regObjectExec(t, target, b, c, "CREATE SCHEMA rl_spacer", "CREATE TABLE rl_spacer.t(v integer)", "CREATE DOMAIN rl_spacer.d AS integer")
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(emitted)); err != nil {
		t.Fatal("foreign Restore", err)
	}
	regLookupRelTypeStoredChecks(t, target, b, c)
	regObjectExec(t, target, b, c, "CREATE TABLE public.rl_sentinel(v integer)")
	regObjectRunStep(t, target, b, c, fixture.Exec("INSERT INTO public.rl_sentinel VALUES(42)", 1))
	sentinel := fixture.Select("SELECT v FROM public.rl_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
	for _, bad := range []string{
		string(emitted) + " SELECT (",
		`CREATE SCHEMA rl_staged; CREATE TABLE rl_staged.t(v integer DEFAULT to_regtypemod('varchar(0)')); INSERT INTO rl_staged.t DEFAULT VALUES;`,
	} {
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("invalid staged Restore succeeded")
		}
		regObjectBoth(t, target, b, c, sentinel)
		regLookupRelTypeStoredChecks(t, target, b, c)
		regObjectBoth(t, target, b, c, fixture.Select(`SELECT to_regnamespace('rl_staged') AS n`, []string{"n"}, []string{"REGNAMESPACE"}, [][]any{{nil}}))
	}
	ctx, cancel := context.WithCancel(target.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, target.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled Restore", err)
	}
	regObjectBoth(t, target, b, c, sentinel)
	regLookupRelTypeStoredChecks(t, target, b, c)
}

func TestReferenceRegLookupRelTypeStored(t *testing.T) {
	var emitted []byte
	regLookupScenario(t, []string{"rl_store"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regLookupRelTypeCreateStored(t, p, b, c)
		regLookupRelTypeStoredChecks(t, p, b, c)
		regLookupRelTypeStoredControls(t, p, b, c, &emitted, true)
	})
}
