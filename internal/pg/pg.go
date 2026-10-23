// Package pg lists and drops the Postgres databases that belong to a worktree.
package pg

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/quotedo/hades/internal/config"
)

// Databases is the subset of Postgres administration hades needs.
type Databases interface {
	List(ctx context.Context) ([]string, error)
	Drop(ctx context.Context, name string) error
}

// Client implements Databases with one short-lived pgx connection per call.
type Client struct {
	DSN string
}

// DSN builds a connection string for database db with user and password escaped.
func DSN(p config.Postgres, db string) string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(p.User, p.Password),
		Host:   p.Host + ":" + strconv.Itoa(p.Port),
		Path:   "/" + db,
	}
	return u.String()
}

func (c *Client) connect(ctx context.Context) (*pgx.Conn, error) {
	conn, err := pgx.Connect(ctx, c.DSN)
	if err != nil {
		// pgx redacts the password in its errors; keep the message as is.
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	return conn, nil
}

// List returns the names of all non-template databases.
func (c *Client) List(ctx context.Context) ([]string, error) {
	conn, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, "SELECT datname FROM pg_database WHERE NOT datistemplate")
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("list databases: %w", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	return names, nil
}

// Drop force-drops the named database, terminating its connections.
func (c *Client) Drop(ctx context.Context, name string) error {
	conn, err := c.connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("drop database %q: %w", name, err)
	}
	return nil
}

// Match returns, in input order, the names that start with any prefix and end
// with suffix followed by optional digits. It is the only place that decides
// what gets dropped: an empty suffix or an empty prefix list matches nothing.
func Match(names, prefixes []string, suffix string) []string {
	if suffix == "" || len(prefixes) == 0 {
		return nil
	}
	res := make([]*regexp.Regexp, 0, len(prefixes))
	for _, p := range prefixes {
		res = append(res, regexp.MustCompile("^"+regexp.QuoteMeta(p)+".*"+regexp.QuoteMeta(suffix)+"[0-9]*$"))
	}
	var out []string
	for _, n := range names {
		for _, re := range res {
			if re.MatchString(n) {
				out = append(out, n)
				break
			}
		}
	}
	return out
}
