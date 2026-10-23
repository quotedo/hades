package rabbit_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/quotedo/hades/internal/rabbit"
)

func TestVhostEnsureAndDelete(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == "DELETE" && strings.HasSuffix(r.URL.Path, "/gone") {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(201)
	}))
	defer srv.Close()
	c := &rabbit.Client{ManagementURL: srv.URL, User: "guest", Password: "guest", HTTP: srv.Client()}
	if err := c.Ensure(context.Background(), "jarl-x"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(context.Background(), "gone"); err != nil {
		t.Fatal(err)
	}
	want := []string{"PUT /api/vhosts/jarl-x", "PUT /api/permissions/jarl-x/guest", "DELETE /api/vhosts/gone"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatal(calls)
	}
	u, _ := rabbit.AMQPURL("amqp://guest:guest@127.0.0.1:5672", "jarl-x")
	if u != "amqp://guest:guest@127.0.0.1:5672/jarl-x" {
		t.Fatal(u)
	}
}

func TestErrorsHideCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	c := &rabbit.Client{ManagementURL: srv.URL, User: "guest", Password: "s3cret", HTTP: srv.Client()}
	err := c.Ensure(context.Background(), "v")
	if err == nil || strings.Contains(err.Error(), "s3cret") || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "PUT /api/vhosts/v") {
		t.Fatal(err)
	}
	if _, err := rabbit.AMQPURL("://bad:s3cret@", "v"); err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Fatal(err)
	}
}

func TestAMQPURLEscapesSlashVhost(t *testing.T) {
	u, err := rabbit.AMQPURL("amqp://g:g@h:5672", "/")
	if err != nil || u != "amqp://g:g@h:5672/%2F" {
		t.Fatal(u, err)
	}
}

func TestEnsureWireDetails(t *testing.T) {
	type rec struct{ method, path, body, ctype, user, pass string }
	var recs []rec
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		u, p, _ := r.BasicAuth()
		recs = append(recs, rec{r.Method, r.URL.EscapedPath(), string(b), r.Header.Get("Content-Type"), u, p})
		w.WriteHeader(204)
	}))
	defer srv.Close()
	c := &rabbit.Client{ManagementURL: srv.URL, User: "guest", Password: "pw", HTTP: srv.Client()}
	if err := c.Ensure(context.Background(), "/"); err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatal(recs)
	}
	if recs[0].method != "PUT" || recs[0].path != "/api/vhosts/%2F" {
		t.Fatal(recs[0])
	}
	if recs[1].method != "PUT" || recs[1].path != "/api/permissions/%2F/guest" {
		t.Fatal(recs[1])
	}
	if want := `{"configure":".*","write":".*","read":".*"}`; recs[1].body != want {
		t.Fatal(recs[1].body)
	}
	for _, r := range recs {
		if r.ctype != "application/json" || r.user != "guest" || r.pass != "pw" {
			t.Fatal(r)
		}
	}
}
