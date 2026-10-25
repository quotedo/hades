package envfile

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"

	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/naming"
	"github.com/quotedo/hades/internal/pg"
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

// Compute returns the worktree environment in a stable order.
func Compute(in Inputs) ([]KV, error) {
	db := in.HostCfg.Postgres
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
		{"DB_HOST", db.Host},
		{"DB_PORT", strconv.Itoa(db.Port)},
		{"DB_USER", db.User},
		{"DB_PASSWORD", db.Password},
		{"DB_SUFFIX", in.Suffix},
	}
	if app := in.Preset.AppName; app != "" {
		kv = append(kv,
			KV{"DATABASE_URL", pg.DSN(db, app+"_dev"+in.Suffix)},
			KV{"TEST_DATABASE_URL", pg.DSN(db, app+"_test"+in.Suffix)},
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
		v, err := naming.Render(in.Extra[k], vars)
		if err != nil {
			return nil, fmt.Errorf("env %s: %w", k, err)
		}
		kv = append(kv, KV{k, v})
	}
	return kv, nil
}
