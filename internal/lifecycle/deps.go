// Package lifecycle composes the building blocks into the commands that bring
// a worktree's environment up and tear it down.
package lifecycle

import (
	"io"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"time"

	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/execx"
	"github.com/quotedo/hades/internal/pg"
	"github.com/quotedo/hades/internal/rabbit"
	"github.com/quotedo/hades/internal/registry"
)

// UserInfo identifies the user hades acts for.
type UserInfo struct {
	Name string
	UID  int
	Home string
}

// CurrentUser returns the user running this process.
func CurrentUser() (UserInfo, error) {
	u, err := user.Current()
	if err != nil {
		return UserInfo{}, err
	}
	return UserInfo{Name: u.Username, UID: os.Getuid(), Home: u.HomeDir}, nil
}

// Deps is everything the lifecycle commands touch outside their arguments.
// PG and Rabbit may be nil (a laptop without services, a host without a
// RabbitMQ block).
type Deps struct {
	Host    *config.Host
	Mode    config.Mode
	Reg     *registry.Registry
	Run     execx.Runner
	PG      pg.Databases
	Rabbit  rabbit.Vhosts
	User    UserInfo
	Version string
	Now     func() time.Time
	Log     io.Writer
	Bin     string // the hades binary sudo runs for caddy-reload
	// LookPath reports whether a tool is installed; nil means exec.LookPath.
	LookPath func(string) (string, error)
	// LoginOf returns the login of a uid; nil means the user database.
	LoginOf func(uid int) (string, error)
	// UIDOf returns the uid of a login; nil means the user database.
	UIDOf func(login string) (int, error)
}

func (d *Deps) lookPath(name string) (string, error) {
	if d.LookPath != nil {
		return d.LookPath(name)
	}
	return exec.LookPath(name)
}

func (d *Deps) loginOf(uid int) (string, error) {
	if d.LoginOf != nil {
		return d.LoginOf(uid)
	}
	u, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return "", err
	}
	return u.Username, nil
}

func (d *Deps) uidOf(login string) (int, error) {
	if d.UIDOf != nil {
		return d.UIDOf(login)
	}
	u, err := user.Lookup(login)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(u.Uid)
}
