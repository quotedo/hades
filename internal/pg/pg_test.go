package pg_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/pg"
)

func mustExec(t *testing.T, dsn, stmt string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, stmt); err != nil {
		t.Fatal(err)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestMatch(t *testing.T) {
	names := []string{"gym_nation_dev_roman_foo", "gym_nation_test_roman_foo", "gym_nation_test_roman_foo2", "gym_nation_dev_roman_foobar", "other_dev_roman_foo", "gym_nation_dev"}
	got := pg.Match(names, []string{"gym_nation"}, "_roman_foo")
	want := []string{"gym_nation_dev_roman_foo", "gym_nation_test_roman_foo", "gym_nation_test_roman_foo2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	if got := pg.Match(names, []string{"gym_nation"}, ""); len(got) != 0 {
		t.Fatal(got)
	}
	if got := pg.Match(names, nil, "_roman_foo"); len(got) != 0 {
		t.Fatal(got)
	}
	if got := pg.Match(names, []string{"", "gym_nation"}, "_roman_foo"); !reflect.DeepEqual(got, want) {
		t.Fatal("empty prefix must be skipped", got)
	}
	if got := pg.Match(names, []string{""}, "_roman_foo"); len(got) != 0 {
		t.Fatal(got)
	}
	if got := pg.Match([]string{"a.b_x"}, []string{"a.b"}, "_x"); len(got) != 1 {
		t.Fatal("quote meta")
	}
}

func TestDSN(t *testing.T) {
	got := pg.DSN(config.Postgres{Host: "h", Port: 5433, User: "u@x", Password: "p:/w"}, "db")
	if want := "postgres://u%40x:p%3A%2Fw@h:5433/db"; got != want {
		t.Fatal(got)
	}
}

func TestClientListAndDrop(t *testing.T) {
	dsn := os.Getenv("HADES_TEST_PG")
	if dsn == "" {
		t.Skip("HADES_TEST_PG not set")
	}
	c := &pg.Client{DSN: dsn}
	ctx := context.Background()
	name := fmt.Sprintf("hades_test_%d", time.Now().UnixNano())
	mustExec(t, dsn, "CREATE DATABASE "+name)
	names, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(names, name) {
		t.Fatal(names)
	}
	if err := c.Drop(ctx, name); err != nil {
		t.Fatal(err)
	}
	names, err = c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if contains(names, name) {
		t.Fatal("still there")
	}
}
