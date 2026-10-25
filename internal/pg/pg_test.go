package pg_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/naming"
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

func TestMatch(t *testing.T) {
	bases := []string{"shop_dev", "shop_test"}
	names := []string{"shop_dev_alice_foo", "shop_test_alice_foo", "shop_test_alice_foo2", "shop_dev_alice_foobar", "other_dev_alice_foo", "shop_dev"}
	got := pg.Match(names, bases, "_alice_foo")
	want := []string{"shop_dev_alice_foo", "shop_test_alice_foo", "shop_test_alice_foo2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	if got := pg.Match(names, bases, ""); len(got) != 0 {
		t.Fatal(got)
	}
	if got := pg.Match(names, nil, "_alice_foo"); len(got) != 0 {
		t.Fatal(got)
	}
	if got := pg.Match(names, append([]string{""}, bases...), "_alice_foo"); !reflect.DeepEqual(got, want) {
		t.Fatal("empty base must be skipped", got)
	}
	if got := pg.Match(names, []string{""}, "_alice_foo"); len(got) != 0 {
		t.Fatal(got)
	}
	if got := pg.Match([]string{"a.b_x", "aXb_x"}, []string{"a.b"}, "_x"); !reflect.DeepEqual(got, []string{"a.b_x"}) {
		t.Fatal("quote meta", got)
	}
}

// Worktree feature/x of user alice in project demo must never drop what it
// does not own.
func TestMatchOnlyThisWorktree(t *testing.T) {
	bases, suffix := []string{"demo_dev", "demo_test"}, naming.Suffix("alice", "feature-x")
	for _, other := range []string{
		// alice's worktree feature/x1. Its suffix ends in "_", because
		// demo_dev_alice_feature_x1 is indistinguishable from partition 1 of
		// feature/x (see the last check).
		"demo_dev" + naming.Suffix("alice", "feature-x1"),
		"demo_dev" + naming.Suffix("bob", "alice-feature-x"), // bob's worktree named alice-feature-x
		"demoapp_dev_alice_feature_x",                        // another project
	} {
		if got := pg.Match([]string{other}, []string{"demo"}, suffix); len(got) != 0 {
			t.Errorf("base demo matched %s", other)
		}
		if got := pg.Match([]string{other}, bases, suffix); len(got) != 0 {
			t.Errorf("bases %v matched %s", bases, other)
		}
	}
	if got := pg.Match([]string{"demo_test_alice_feature_x1"}, bases, suffix); len(got) != 1 {
		t.Fatal("test partition not matched")
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
	if !slices.Contains(names, name) {
		t.Fatal(names)
	}
	if err := c.Drop(ctx, name); err != nil {
		t.Fatal(err)
	}
	names, err = c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(names, name) {
		t.Fatal("still there")
	}
}
