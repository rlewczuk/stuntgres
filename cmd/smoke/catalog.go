package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"stuntgres"
)

// verifyCatalogOwners exercises native instance attachment with live physical
// connections. It deliberately uses opaque DSNs, not a new DSN syntax.
func verifyCatalogOwners() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d := stuntgres.NewDriver()
	open := func(driver *stuntgres.Driver, dsn string) (*sql.DB, error) {
		connector, err := driver.OpenConnector(dsn)
		if err != nil {
			return nil, err
		}
		return sql.OpenDB(connector), nil
	}
	var pools []*sql.DB
	defer func() {
		for _, pool := range pools {
			pool.Close()
		}
	}()
	for _, dsn := range []string{"instance-a", "instance-a", "instance-b", "instance-b"} {
		pool, err := open(d, dsn)
		if err != nil {
			return err
		}
		pools = append(pools, pool)
	}
	var connections []*sql.Conn
	defer func() {
		for _, conn := range connections {
			conn.Close()
		}
	}()
	for _, pool := range pools {
		conn, err := pool.Conn(ctx)
		if err != nil {
			return err
		}
		connections = append(connections, conn)
	}
	missing := func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, "SELECT id FROM public.t")
		if rows != nil {
			rows.Close()
		}
		var state interface{ SQLState() string }
		if !errors.As(err, &state) || state.SQLState() != "42P01" {
			return fmt.Errorf("isolated instance: want 42P01, got %v", err)
		}
		return nil
	}
	checkRows := func(rows *sql.Rows, want int64) error {
		defer rows.Close()
		names, err := rows.Columns()
		if err != nil {
			return err
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(names, []string{"id"}) || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
			return fmt.Errorf("instance metadata: %v / %v", names, types)
		}
		var got []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return err
			}
			got = append(got, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if !reflect.DeepEqual(got, []int64{want}) {
			return fmt.Errorf("instance rows: got %v, want [%d]", got, want)
		}
		return nil
	}
	check := func(conn *sql.Conn, want int64) error {
		for _, query := range []string{"SELECT id FROM public.t", "SELECT id FROM t"} {
			rows, err := conn.QueryContext(ctx, query)
			if err != nil {
				return err
			}
			if err := checkRows(rows, want); err != nil {
				return err
			}
		}
		return nil
	}
	for i, value := range []int64{11, 22} {
		conn := connections[i*2]
		if i == 1 {
			if err := missing(conn); err != nil {
				return err
			}
		}
		result, err := conn.ExecContext(ctx, "CREATE TABLE public.t (id INTEGER)")
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n != 0 {
			return fmt.Errorf("instance create count: %d, %v", n, err)
		}
		stmt, err := conn.PrepareContext(ctx, "INSERT INTO public.t VALUES ($1)")
		if err != nil {
			return err
		}
		result, err = stmt.ExecContext(ctx, value)
		stmt.Close()
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return fmt.Errorf("instance insert count: %d, %v", n, err)
		}
	}
	for i, conn := range connections {
		if err := check(conn, int64((i/2+1)*11)); err != nil {
			return err
		}
	}
	cursor, err := connections[0].QueryContext(ctx, "SELECT id FROM public.t")
	if err != nil {
		return err
	}
	defer cursor.Close()
	if err := stuntgres.Reset(ctx, pools[2]); err != nil {
		return err
	}
	for _, conn := range connections[2:] {
		if err := missing(conn); err != nil {
			return err
		}
	}
	if err := check(connections[1], 11); err != nil {
		return err
	}
	if err := stuntgres.Restore(ctx, pools[2], strings.NewReader("CREATE TABLE public.t (id INTEGER); INSERT INTO public.t VALUES (99);")); err != nil {
		return err
	}
	for _, conn := range connections[2:] {
		if err := check(conn, 99); err != nil {
			return err
		}
	}
	if err := checkRows(cursor, 11); err != nil {
		return err
	}
	for _, conn := range connections[:2] {
		if err := check(conn, 11); err != nil {
			return err
		}
		if err := conn.Close(); err != nil {
			return err
		}
	}
	for _, pool := range pools[:2] {
		if err := pool.Close(); err != nil {
			return err
		}
	}
	reopened, err := open(d, "instance-a")
	if err != nil {
		return err
	}
	pools = append(pools, reopened)
	conn, err := reopened.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := check(conn, 11); err != nil {
		return err
	}
	independent, err := open(stuntgres.NewDriver(), "instance-a")
	if err != nil {
		return err
	}
	pools = append(pools, independent)
	other, err := independent.Conn(ctx)
	if err != nil {
		return err
	}
	defer other.Close()
	return missing(other)
}
