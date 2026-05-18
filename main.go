package main

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/app"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/cli"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func main() {
	cli.Main()
}

func usage() {
	cli.Usage()
}

func run(ctx context.Context, args []string) error {
	return cli.RunWithTUI(ctx, args, runTUIFn)
}

func newService(st *store.Store) *app.Service {
	return cli.NewService(st)
}

func runDispatch(ctx context.Context, svc *app.Service, args []string) error {
	return cli.RunDispatch(ctx, svc, args)
}

var runTUIFn = func(ctx context.Context, model tea.Model) error {
	return cli.RunTUI(ctx, model)
}
