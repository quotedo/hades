package envfile

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/preset"
	"github.com/quotedo/hades/internal/rabbit"
)

// Inputs is everything Compute needs to derive a worktree's environment.
type Inputs struct {
	Port                                    int
	Label, Host, URL, Suffix, Name, Project string
	Preset                                  preset.Preset
	HostCfg                                 config.Host
	Vhost                                   string
	Extra                                   map[string]string
	ExistingSecret                          string
}

var placeholderRe = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// Compute returns the worktree environment in a stable order.
func Compute(in Inputs) ([]KV, error) {
	pg := in.HostCfg.Postgres
	port := strconv.Itoa(in.Port)

	secret := in.ExistingSecret
	if secret == "" {
		buf := make([]byte, 64)
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("generate SECRET_KEY_BASE: %w", err)
		}
		secret = base64.StdEncoding.EncodeToString(buf)
	}

	phxHost, hadesURL := in.Host, in.URL
	if in.HostCfg.Domain == "" {
		phxHost, hadesURL = "localhost", "http://localhost:"+port
	}

	kv := []KV{
		{"PORT", port},
		{"DB_HOST", pg.Host},
		{"DB_PORT", strconv.Itoa(pg.Port)},
		{"DB_USER", pg.User},
		{"DB_PASSWORD", pg.Password},
		{"DB_SUFFIX", in.Suffix},
	}
	if app := in.Preset.AppName; app != "" {
		base := (&url.URL{
			Scheme: "postgres",
			User:   url.UserPassword(pg.User, pg.Password),
			Host:   pg.Host + ":" + strconv.Itoa(pg.Port),
		}).String()
		kv = append(kv,
			KV{"DATABASE_URL", base + "/" + app + "_dev" + in.Suffix},
			KV{"TEST_DATABASE_URL", base + "/" + app + "_test" + in.Suffix},
		)
	}
	kv = append(kv,
		KV{"SECRET_KEY_BASE", secret},
		KV{"PHX_HOST", phxHost},
		KV{"HADES_URL", hadesURL},
		KV{"HADES_LABEL", in.Label},
	)

	if in.Vhost != "" {
		rmq := in.HostCfg.RabbitMQ
		if rmq == nil {
			return nil, fmt.Errorf("vhost %q requested but host config has no RabbitMQ", in.Vhost)
		}
		amqp, err := rabbit.AMQPURL(rmq.AMQPURL, in.Vhost)
		if err != nil {
			return nil, fmt.Errorf("RabbitMQ amqp_url: %w", err)
		}
		kv = append(kv, KV{"RABBITMQ_VHOST", in.Vhost}, KV{"AMQP_URL", amqp})
	}

	vars := map[string]string{
		"host": phxHost, "port": port, "url": hadesURL, "suffix": in.Suffix,
		"name": in.Name, "project": in.Project, "label": in.Label,
	}
	keys := make([]string, 0, len(in.Extra))
	for k := range in.Extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v, err := render(in.Extra[k], vars)
		if err != nil {
			return nil, fmt.Errorf("env %s: %w", k, err)
		}
		kv = append(kv, KV{k, v})
	}
	return kv, nil
}

func render(tmpl string, vars map[string]string) (string, error) {
	var bad string
	out := placeholderRe.ReplaceAllStringFunc(tmpl, func(m string) string {
		name := strings.TrimSpace(m[2 : len(m)-2])
		v, ok := vars[name]
		if !ok && bad == "" {
			bad = m
		}
		return v
	})
	if bad != "" {
		return "", fmt.Errorf("unknown placeholder %s", bad)
	}
	return out, nil
}
