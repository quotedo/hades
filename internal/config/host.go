// Package config loads hades' host configuration (server mode), the laptop
// defaults used when no host config exists, and the per-repository hades.toml.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/quotedo/hades/internal/naming"
)

// Mode says whether hades runs against a shared server or a laptop.
type Mode int

const (
	Server Mode = iota
	Laptop
)

// ErrNoHost reports that the host config file does not exist.
var ErrNoHost = fmt.Errorf("no host config: %w", fs.ErrNotExist)

const defaultHostPath = "/etc/hades/host.toml"

type Caddy struct {
	Config string `toml:"config"`
	// LiveDir is the root-owned directory Caddy imports; caddy-reload mirrors
	// the validated sites into it.
	LiveDir string   `toml:"live_dir"`
	Reload  []string `toml:"reload"`
}

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

// Host is what the machine offers: domain, registries, and shared services.
type Host struct {
	Domain        string
	LabelTemplate string   `toml:"label_template"`
	PortRange     string   `toml:"port_range"`
	PortsDir      string   `toml:"ports_dir"`
	SitesDir      string   `toml:"sites_dir"`
	ManifestsDir  string   `toml:"manifests_dir"`
	ToolPaths     []string `toml:"tool_paths"`
	ChromePath    string   `toml:"chrome_path"`
	Caddy         Caddy
	Postgres      Postgres
	RabbitMQ      *RabbitMQ
}

// HostPath returns HADES_HOST_CONFIG or /etc/hades/host.toml.
func HostPath(getenv func(string) string) string {
	if p := getenv("HADES_HOST_CONFIG"); p != "" {
		return p
	}
	return defaultHostPath
}

// Range returns the parsed port range. It panics only if LoadHost validation
// was bypassed.
func (h *Host) Range() PortRange {
	r, err := ParsePortRange(h.PortRange)
	if err != nil {
		panic(err)
	}
	return r
}

// LoadHost reads and validates the host config at path. It returns ErrNoHost
// when the file does not exist.
func LoadHost(path string) (*Host, error) {
	var h Host
	if _, err := toml.DecodeFile(path, &h); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNoHost, path)
		}
		return nil, err
	}
	if h.LabelTemplate == "" {
		h.LabelTemplate = naming.DefaultLabelTemplate
	}
	var missing []string
	for _, k := range []struct {
		key string
		val string
	}{
		{"domain", h.Domain},
		{"port_range", h.PortRange},
		{"ports_dir", h.PortsDir},
		{"sites_dir", h.SitesDir},
		{"manifests_dir", h.ManifestsDir},
		{"caddy.config", h.Caddy.Config},
		{"caddy.live_dir", h.Caddy.LiveDir},
		{"postgres.host", h.Postgres.Host},
		{"postgres.user", h.Postgres.User},
		{"postgres.password", h.Postgres.Password},
	} {
		if k.val == "" {
			missing = append(missing, k.key)
		}
	}
	if len(h.Caddy.Reload) == 0 {
		missing = append(missing, "caddy.reload")
	}
	if h.Postgres.Port == 0 {
		missing = append(missing, "postgres.port")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%s: missing required keys: %s", path, strings.Join(missing, ", "))
	}
	if _, err := ParsePortRange(h.PortRange); err != nil {
		return nil, fmt.Errorf("%s: port_range: %w", path, err)
	}
	return &h, nil
}

// LaptopHost returns the defaults used when no host config exists. It fails
// on an unparseable HADES_DB_PORT or an invalid HADES_PORT_RANGE.
func LaptopHost(home string, getenv func(string) string) (*Host, error) {
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
	if _, err := ParsePortRange(portRange); err != nil {
		return nil, fmt.Errorf("HADES_PORT_RANGE: %w", err)
	}
	state := filepath.Join(home, ".local", "state", "hades")
	return &Host{
		LabelTemplate: naming.DefaultLabelTemplate,
		PortRange:     portRange,
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

// LaptopOverride is the HADES_HOST_CONFIG value that forces laptop mode.
const LaptopOverride = "laptop"

// Load reads the host config. Laptop defaults apply when
// HADES_HOST_CONFIG=laptop, or when HADES_HOST_CONFIG is unset and the default
// host config is absent. An explicit path that does not exist is an error.
func Load(getenv func(string) string, home string) (*Host, Mode, error) {
	explicit := getenv("HADES_HOST_CONFIG")
	if explicit != LaptopOverride {
		h, err := LoadHost(HostPath(getenv))
		if err == nil {
			return h, Server, nil
		}
		if explicit != "" || !errors.Is(err, ErrNoHost) {
			return nil, Server, err
		}
	}
	h, err := LaptopHost(home, getenv)
	if err != nil {
		return nil, Laptop, err
	}
	return h, Laptop, nil
}
