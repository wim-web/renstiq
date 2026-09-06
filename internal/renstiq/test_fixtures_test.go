package renstiq

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Concrete adapters belong to integration fixtures, not to application services.
type engineFixture struct {
	*Engine
	GitHub   *GitHub
	Store    *Store
	Executor *PostExecutor
}

func testRunSession(s *Store) RunSession {
	return RunSession{State: &s.State, Journal: s, Now: time.Now, NewID: func() string { return fmt.Sprint(time.Now().UnixNano()) }}
}

func runTestCLI(ctx context.Context, args []string, in io.Reader, out, log io.Writer, factory func(context.Context, Config, io.Writer) (*GitHub, error)) int {
	app := newApplication(log)
	app.Remotes = func(ctx context.Context, c Config) (remotePorts, error) {
		g, err := factory(ctx, c, log)
		if err != nil {
			return remotePorts{}, err
		}
		return githubPorts(g), nil
	}
	return newCLI(app, selfUpdate).Run(ctx, args, in, out, log)
}

func runTestUpdateCLI(ctx context.Context, args []string, out, log io.Writer, updater func(context.Context) (UpdateResult, error)) int {
	return newCLI(&Application{}, updater).Run(ctx, append([]string{"update"}, args...), nil, out, log)
}
