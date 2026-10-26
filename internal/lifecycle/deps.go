// Package lifecycle composes the building blocks into the commands that bring
// a worktree's environment up and tear it down.
package lifecycle

import (
	"io"
	"os"
	"os/exec"
	"os/user"
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
	Reg     *registry.Registry
	Run     execx.Runner
	PG      pg.Databases
	Rabbit  rabbit.Vhosts
	User    UserInfo
	Version string
	Now     func() time.Time
	Log     io.Writer
	// LookPath reports whether a tool is installed; nil means exec.LookPath.
	LookPath func(string) (string, error)
}

func (d *Deps) lookPath(name string) (string, error) {
	if d.LookPath != nil {
		return d.LookPath(name)
	}
	return exec.LookPath(name)
}
