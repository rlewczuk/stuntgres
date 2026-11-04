package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func TestPGStatSSLDescriptorAndProjection(t *testing.T) {
	e := engine.New()
	owner := enginetest.StartSession(t, e, nil)
	for _, query := range []string{"CREATE ROLE ssl_unit_login", "CREATE ROLE ssl_unit_reader", "CREATE ROLE ssl_unit_other", "GRANT pg_read_all_stats TO ssl_unit_reader"} {
		enginetest.Run(t, e, query)
	}
	read := e.CatalogRead()
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, read)
	login, _ := roles.Lookup("ssl_unit_login")
	stats, _ := roles.Lookup("ssl_unit_reader")
	other, _ := roles.Lookup("ssl_unit_other")
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatSSLOID}
	source, ok := read.Relation(address)
	if !ok {
		t.Fatal("missing registered SSL source")
	}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.StatSSLOID)
	if !ok || produce == nil || pgcatalog.SummaryViewDefinition(pgcatalog.StatSSLOID) == "" || pgcatalog.BuiltinViewEvents(pgcatalog.StatSSLOID, nil) != 0 {
		t.Fatal("missing read-only common producer/view")
	}
	names := []string{"pid", "ssl", "version", "cipher", "bits", "client_dn", "client_serial", "issuer_dn"}
	types := []sqltype.ID{sqltype.Int4, sqltype.Bool, sqltype.Text, sqltype.Text, sqltype.Int4, sqltype.Text, sqltype.Numeric, sqltype.Text}
	count := 0
	for _, spec := range pgcatalog.Specs {
		if spec.Name != "pg_stat_ssl" {
			continue
		}
		count++
		if spec.OID != 12253 || !spec.Virtual || spec.NoPublic || len(spec.Columns) != 8 {
			t.Fatal("SSL registration", spec)
		}
		for i, column := range spec.Columns {
			collation := ids.OID(0)
			if types[i] == sqltype.Text {
				collation = 100
			}
			if column.Name != names[i] || column.Type != types[i] || !column.Nullable || column.Typmod != -1 || column.Collation != uint32(collation) || column.Dimensions != 0 {
				t.Fatal("SSL declaration", i, column)
			}
			// Installed source owns the descriptor and default text collation.
			actual := source.Columns[i]
			if actual.Usage.Type.ID() != types[i] || !actual.Nullable || actual.Usage.Typmod != -1 || actual.DeclaredDimensions != 0 {
				t.Fatal("installed SSL descriptor", i, actual)
			}
			if actual.Usage.Collation.ID != uint32(collation) {
				t.Fatal("SSL installed collation", i, actual.Usage.Collation)
			}
		}
	}
	if count != 1 {
		t.Fatal("one SSL source registration", count)
	}
	token := owner.SessionToken
	fact := e.Statistics.CaptureActivities()[token]
	fact.Principal = login.Ref
	input := func(observer authorization.PrincipalRef) pgcatalog.Input {
		read := e.CatalogRead()
		source, ok := read.Relation(address)
		if !ok {
			t.Fatal("lost SSL source")
		}
		return pgcatalog.Input{Context: t.Context(), Catalog: read, Roles: e.RoleStore.PublishedRead(&e.Catalog.Owner, read), Owner: &e.Catalog.Owner, Source: source, Ref: e.Catalog.Domain.Ref(read.Epoch(), address), ActivityObserver: observer, Activities: map[ids.SessionToken]statistics.Activity{token: fact}}
	}
	check := func(in pgcatalog.Input, present bool) [][]sqltype.Value {
		t.Helper()
		rows, err := produce(in)
		want := 0
		if present {
			want = 1
		}
		if err != nil || len(rows) != want {
			t.Fatal("SSL authorized membership", rows, present, err)
		}
		if !present {
			return rows
		}
		if len(rows[0]) != 8 {
			t.Fatal("eight SSL cells", rows)
		}
		for i, cell := range rows[0] {
			if cell.Type() != in.Source.Columns[i].Usage.Type || cell.IsNull() != (i >= 2) {
				t.Fatal("typed SSL NULL/descriptor", i, cell)
			}
		}
		if pid, err := rows[0][0].Integer(); err != nil || pid != int64(token.ID()) || pid <= 0 {
			t.Fatal("real PID", pid, err)
		}
		if ssl, err := rows[0][1].Boolean(); err != nil || ssl {
			t.Fatal("truthful false transport", ssl, err)
		}
		return rows
	}
	in := input(owner.CountedAuthenticated)
	old := check(in, true)
	check(input(login.Ref), true)
	check(input(stats.Ref), true)
	check(input(other.Ref), false)
	captured, err := catalogrt.CaptureRows(in, produce)
	if err != nil || captured.Ref != in.Ref || &captured.Anonymous[0][0] == &old[0][0] || !reflect.DeepEqual(captured.Anonymous, old) {
		t.Fatal("common owned SSL capture", captured, err)
	}
	foreign := engine.New()
	reject := func(wrong pgcatalog.Input) {
		t.Helper()
		rows, err := produce(wrong)
		if err == nil || rows != nil {
			t.Fatal("invalid authority published SSL rows", rows, err)
		}
		if _, sqlError := err.(interface{ SQLState() string }); sqlError {
			t.Fatal("invariant failure fabricated a SQL diagnostic", err)
		}
	}
	for _, change := range []func(*pgcatalog.Input){
		func(v *pgcatalog.Input) { v.Owner = nil },
		func(v *pgcatalog.Input) { v.Owner = foreign.CatalogRead().Owner() },
		func(v *pgcatalog.Input) { v.Catalog = catalog.Read{} },
		func(v *pgcatalog.Input) { v.Source = nil },
		func(v *pgcatalog.Input) { copied := *v.Source; v.Source = &copied },
		func(v *pgcatalog.Input) { v.Roles = foreign.RoleStore.Published.Root.Read(authorization.PrincipalRef{}) },
		func(v *pgcatalog.Input) { v.ActivityObserver = (&authorization.Domain{}).Principal(login.Ref.ID()) },
		func(v *pgcatalog.Input) { v.ActivityObserver = authorization.PrincipalRef{} },
		func(v *pgcatalog.Input) { v.Ref = foreign.Catalog.Domain.Ref(v.Ref.Epoch, v.Ref.Address) },
		func(v *pgcatalog.Input) { v.Ref.Epoch++ },
		func(v *pgcatalog.Input) { v.Ref.Address.ObjectID++ },
		func(v *pgcatalog.Input) {
			v.Source, _ = read.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatActivityOID})
			v.Ref = e.Catalog.Domain.Ref(read.Epoch(), v.Source.Address())
		},
	} {
		wrong := in
		change(&wrong)
		reject(wrong)
	}
	for _, principal := range []authorization.PrincipalRef{{}, (&authorization.Domain{}).Principal(login.Ref.ID())} {
		wrong := input(owner.CountedAuthenticated)
		bad := fact
		bad.Principal = principal
		wrong.Activities[token] = bad
		reject(wrong)
	}
	// Missing login authority must not adopt the recreated name; all-stats
	// readers still project the retained physical identity without a role join.
	enginetest.Run(t, e, "DROP ROLE ssl_unit_login")
	enginetest.Run(t, e, "CREATE ROLE ssl_unit_login")
	check(input(owner.CountedAuthenticated), true)
	check(input(stats.Ref), true)
	check(input(other.Ref), false)
	recreated, _ := e.RoleStore.PublishedRead(&e.Catalog.Owner, e.CatalogRead()).Lookup("ssl_unit_login")
	check(input(recreated.Ref), false)
	reject(input(login.Ref))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	wrong := in
	wrong.Context, wrong.Owner = ctx, nil
	if rows, err := produce(wrong); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("ordinary SSL cancellation before authority", rows, err)
	}
	if !reflect.DeepEqual(old, captured.Anonymous) {
		t.Fatal("later projection/catalog changes mutated prior SSL values")
	}
}
