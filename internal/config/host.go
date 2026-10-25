// Package config loads hades' host configuration (laptop defaults overlaid by
// an optional host file) and the per-repository hades.toml.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"

	"github.com/BurntSushi/toml"

	"github.com/quotedo/hades/internal/naming"
)

// DefaultHostPath is the host file Load reads when HADES_HOST_CONFIG is unset.
var DefaultHostPath = "/etc/hades/host.toml"

type Postgres struct {
	Host     string
	Port     int
	User     string
	Password string
}

type RabbitMQ struct {
	ManagementURL string `toml:"management_url"`
	AMQPURL       string `toml:"amqp_url"`
	User          string
	Password      string
}

// Host is what the machine offers. The host file sets Domain, ChromePath and
// RabbitMQ; Load sets the rest.
type Host struct {
	Domain     string    `toml:"domain"`
	ChromePath string    `toml:"chrome_path"`
	RabbitMQ   *RabbitMQ `toml:"rabbitmq"`

	Path          string    `toml:"-"` // the host file read; empty without one
	LabelTemplate string    `toml:"-"`
	PortRange     string    `toml:"-"`
	Ports         PortRange `toml:"-"` // PortRange parsed
	PortsDir      string    `toml:"-"`
	SitesDir      string    `toml:"-"`
	ManifestsDir  string    `toml:"-"`
	Postgres      Postgres  `toml:"-"`
}

// Load returns the laptop defaults overlaid by the host file at
// HADES_HOST_CONFIG, else at DefaultHostPath when that file exists. An
// explicit path that does not exist is an error, and so are an unknown key,
// an unparseable HADES_DB_PORT and an invalid HADES_PORT_RANGE.
func Load(getenv func(string) string, home string) (*Host, error) {
	h, err := laptop(home, getenv)
	if err != nil {
		return nil, err
	}
	path := getenv("HADES_HOST_CONFIG")
	explicit := path != ""
	if !explicit {
		path = DefaultHostPath
	}
	md, err := toml.DecodeFile(path, h)
	switch {
	case errors.Is(err, fs.ErrNotExist) && !explicit:
		return h, nil
	case err != nil:
		return nil, fmt.Errorf("host config %s: %w", path, err)
	}
	if err := unknownKeys(path, md); err != nil {
		return nil, err
	}
	h.Path = path
	if h.Domain != "" {
		h.LabelTemplate = naming.DomainLabelTemplate
	}
	return h, nil
}

// laptop returns the defaults with the HADES_DB_* and HADES_PORT_RANGE
// overrides applied.
func laptop(home string, getenv func(string) string) (*Host, error) {
	or := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}
	port := 5432
	if v := getenv("HADES_DB_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("HADES_DB_PORT: invalid port %q", v)
		}
		port = n
	}
	portRange := or("HADES_PORT_RANGE", "4100-4999")
	ports, err := ParsePortRange(portRange)
	if err != nil {
		return nil, fmt.Errorf("HADES_PORT_RANGE: %w", err)
	}
	state := filepath.Join(home, ".local", "state", "hades")
	return &Host{
		LabelTemplate: naming.DefaultLabelTemplate,
		PortRange:     portRange,
		Ports:         ports,
		PortsDir:      filepath.Join(state, "ports"),
		SitesDir:      filepath.Join(state, "sites"),
		ManifestsDir:  filepath.Join(state, "manifests"),
		Postgres: Postgres{
			Host:     or("HADES_DB_HOST", "127.0.0.1"),
			Port:     port,
			User:     or("HADES_DB_USER", "postgres"),
			Password: or("HADES_DB_PASSWORD", "postgres"),
		},
	}, nil
}
