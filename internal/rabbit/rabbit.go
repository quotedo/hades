// Package rabbit manages the per-worktree RabbitMQ vhost through the management API.
package rabbit

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Vhosts is the subset of RabbitMQ administration hades needs.
type Vhosts interface {
	Ensure(ctx context.Context, vhost string) error
	Delete(ctx context.Context, vhost string) error
}

// Client talks to the RabbitMQ management HTTP API with basic auth.
type Client struct {
	ManagementURL string
	User          string
	Password      string
	HTTP          *http.Client
}

const fullPermissions = `{"configure":".*","write":".*","read":".*"}`

// Ensure creates the vhost and grants User full permissions on it.
func (c *Client) Ensure(ctx context.Context, vhost string) error {
	v := url.PathEscape(vhost)
	if err := c.do(ctx, http.MethodPut, "/api/vhosts/"+v, "{}"); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, "/api/permissions/"+v+"/"+url.PathEscape(c.User), fullPermissions)
}

// Delete removes the vhost; a vhost that is already gone is not an error.
func (c *Client) Delete(ctx context.Context, vhost string) error {
	err := c.do(ctx, http.MethodDelete, "/api/vhosts/"+url.PathEscape(vhost), "")
	if se, ok := err.(*statusError); ok && se.status == http.StatusNotFound {
		return nil
	}
	return err
}

type statusError struct {
	method, path string
	status       int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("rabbitmq %s %s: status %d", e.method, e.path, e.status)
}

func (c *Client) do(ctx context.Context, method, path, body string) error {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.ManagementURL, "/")+path, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("rabbitmq %s %s: invalid request", method, path)
	}
	req.SetBasicAuth(c.User, c.Password)
	req.Header.Set("Content-Type", "application/json")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// url.Error redacts passwords, but report only the cause to be safe.
		if ue, ok := err.(*url.Error); ok {
			err = ue.Err
		}
		return fmt.Errorf("rabbitmq %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &statusError{method, path, resp.StatusCode}
	}
	return nil
}

// AMQPURL returns base with its path set to the (escaped) vhost.
func AMQPURL(base, vhost string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		// url.Parse errors echo the input, which may contain a password.
		return "", fmt.Errorf("invalid amqp base url")
	}
	u.Path = "/" + vhost
	u.RawPath = "/" + url.PathEscape(vhost)
	return u.String(), nil
}
