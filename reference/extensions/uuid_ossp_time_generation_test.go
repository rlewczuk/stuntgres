package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func uuidOsspTimeGeneration(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, name string) {
	t.Helper()
	mc := name == "uuid_generate_v1mc"
	q := "SELECT " + name + "() AS u"
	seen := map[string]bool{}
	read := func(e fixture.Executor, query string, n, width int) []string {
		v := uuidOsspTimeRead(t, p, e, r, pg, query, n, width, mc)
		uuidOsspTimeUnique(t, seen, v)
		return v
	}
	first := read(c, q, 1, 1)[0]
	uuidOsspTimeExtract(t, p, c, r, first, mc)
	fresh, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	read(fixture.HeldStatement{Executor: c, Statement: fresh, SQL: q}, q, 1, 1)
	fresh.Close()
	s, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	held := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
	var previous *uuidOsspTimeFields
	nodes := map[[6]byte]bool{}
	for range 4 {
		value := read(held, q, 1, 1)[0]
		f := uuidOsspTimeDecode(t, value, mc)
		if previous != nil && (f.ticks < previous.ticks || f.ticks == previous.ticks && f.sequence <= previous.sequence) {
			t.Fatal("sequential timestamp/sequence did not advance", *previous, f)
		}
		if mc && nodes[f.node] {
			t.Fatal("multicast node reused", f.node)
		}
		nodes[f.node] = true
		previous = &f
	}
	read(c, "SELECT "+name+"() AS u,"+name+"() AS u", 1, 2)
	for _, prepared := range []bool{false, true} {
		empty := q + " WHERE FALSE"
		var e fixture.Executor = c
		if prepared {
			s, err := c.PrepareContext(p.Ctx, empty)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			e = fixture.HeldStatement{Executor: c, Statement: s, SQL: empty}
		}
		read(e, empty, 0, 1)
		step := fixture.Select("SELECT CASE WHEN FALSE THEN "+name+"() ELSE NULL END AS u", []string{"u"}, []string{"UUID"}, [][]any{{nil}})
		step.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, r, step)
	}
	for _, dst := range []any{new(string), new([]byte), new(sql.NullString)} {
		if err := c.QueryRowContext(p.Ctx, q).Scan(dst); err != nil {
			t.Fatal(err)
		}
		var value string
		switch v := dst.(type) {
		case *string:
			value = *v
		case *[]byte:
			value = string(*v)
		case *sql.NullString:
			if !v.Valid {
				t.Fatal("NULL generation")
			}
			value = v.String
		}
		uuidOsspTimeDecode(t, value, mc)
		uuidOsspTimeUnique(t, seen, []string{value})
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE "+name+"_ids(id integer)", 0))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO "+name+"_ids VALUES(1),(2),(3)", 3))
	read(c, q+" FROM "+name+"_ids ORDER BY id", 3, 1)
}

func TestReferenceUUIDOsspTimeGeneration(t *testing.T) {
	uuidOsspCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, name := range uuidOsspTimeNames {
			t.Run(name, func(t *testing.T) { uuidOsspTimeGeneration(t, p, c, r, pg, name) })
		}
	})
}
