package enginetests_test

import (
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestBackendCurrentProjectionAndAuthority(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE ROLE bc_unit_login", "CREATE ROLE bc_unit_reader", "CREATE ROLE bc_unit_other", "GRANT pg_read_all_stats TO bc_unit_reader"} {
		enginetest.Run(t, e, q)
	}
	view := e.CatalogRead()
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, view)
	login, _ := roles.Lookup("bc_unit_login")
	reader, _ := roles.Lookup("bc_unit_reader")
	other, _ := roles.Lookup("bc_unit_other")
	proc := view.MustProcedure(ids.ObjectAddress{ClassID: 1255, ObjectID: 2022})
	out := proc.OutputDeclaration()
	members := make([]sqltype.CompositeMember, out.Len())
	for i := range members {
		typ, _ := view.LookupType(sqltype.ID(out.Type(i)))
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: out.Name(i + 1), Usage: typ.Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(100, 0)
	a := statistics.Activity{Principal: login.Ref, Database: 123, Application: "unit", State: "idle", QueryAvailable: true, Query: "sensitive", BackendType: "client backend", XID: 17, Xmin: 13, QueryID: 31, BackendStart: at, TransactionStart: at.Add(time.Second), Start: at.Add(2 * time.Second), StateChange: at.Add(3 * time.Second), WaitType: "Lock", Wait: "relation"}
	input := func(observer authorization.PrincipalRef) pgcatalog.Input {
		return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: roles, ActivityObserver: observer, Activities: map[ids.SessionToken]statistics.Activity{s.Token(): a}}
	}
	for _, observer := range []authorization.PrincipalRef{login.Ref, reader.Ref, other.Ref} {
		rows, err := pgcatalog.BackendActivity(input(observer), int64(s.Token().ID()), shape)
		if err != nil || len(rows) != 1 || rows[0].IsNull() || rows[0].CompositeLen() != 31 {
			t.Fatal("typed activity row", rows, err)
		}
		masked := observer == other.Ref
		for i := range 31 {
			v := rows[0].CompositeField(i)
			if v.Type() != members[i].Usage.Type {
				t.Fatal("field generation", i)
			}
			public := i == 0 || i == 1 || i == 2 || i == 3 || i == 15 || i == 16
			if masked && !public && i != 5 && !v.IsNull() {
				t.Fatal("full protected mask", i, v)
			}
			if i == 5 {
				text, err := v.Text()
				want := "sensitive"
				if masked {
					want = "<insufficient privilege>"
				}
				if err != nil || text != want {
					t.Fatal("query", text, err)
				}
			}
			if !masked && (i == 18 || i == 25 || i == 27 || i == 28) {
				value, err := v.Boolean()
				if err != nil || value {
					t.Fatal("absent security", i, value, err)
				}
			}
		}
	}
	for _, kind := range []string{"checkpointer", "background writer", "startup", "autovacuum launcher", "invalid", "dead-end backend", "archiver", "logger", "io worker", ""} {
		if pgcatalog.BackendStatisticsKind(kind) {
			t.Fatal("excluded classifier", kind)
		}
	}
	for _, kind := range []string{"autovacuum worker", "client backend", "background worker", "standalone backend", "slotsync worker", "walreceiver", "walsender", "walsummarizer", "walwriter"} {
		if !pgcatalog.BackendStatisticsKind(kind) {
			t.Fatal("participating classifier", kind)
		}
	}
	ioSource := view.MustRelation(ids.ObjectAddress{ClassID: 1259, ObjectID: pgcatalog.StatIOOID})
	var ioTypes [20]*sqltype.Descriptor
	for i := range ioTypes {
		ioTypes[i] = ioSource.Columns[i].Usage.Type
	}
	rows, err := pgcatalog.PhysicalIORows(t.Context(), ioTypes, "client backend", at)
	if err != nil || len(rows) != 8 {
		t.Fatal("client capabilities", rows, err)
	}
	for _, row := range rows {
		if reset, err := row[19].TimestampTime(); err != nil || !reset.Equal(at) {
			t.Fatal("backend reset authority", row[19], err)
		}
		for i := 3; i < 19; i++ {
			if row[i].Type() != ioTypes[i] {
				t.Fatal("typed physical field", i)
			}
			if !row[i].IsNull() && ioTypes[i].ID() == sqltype.Int8 {
				if n, err := row[i].Integer(); err != nil || n != 0 {
					t.Fatal("physical count", i, n, err)
				}
			}
		}
	}
	walSource := view.MustRelation(ids.ObjectAddress{ClassID: 1259, ObjectID: pgcatalog.StatWALOID})
	var walTypes [5]*sqltype.Descriptor
	for i := range walTypes {
		walTypes[i] = walSource.Columns[i].Usage.Type
	}
	wal, err := pgcatalog.PhysicalWALRow(t.Context(), walTypes, at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if stamp, err := wal[4].TimestampTime(); err != nil || !stamp.Equal(at.Add(time.Second)) {
		t.Fatal("explicit independent reset authority", stamp, err)
	}
}
