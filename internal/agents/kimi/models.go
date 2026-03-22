package kimi

import (
	"context"

	"github.com/beyond5959/ngent/internal/agents"
	"github.com/beyond5959/ngent/internal/agents/acpcli"
)

// DiscoverModels starts one ACP session/new handshake and returns model options.
func DiscoverModels(ctx context.Context, cfg Config) ([]agents.ModelOption, error) {
	return acpcli.DiscoverModelsWithClient(ctx, func() (*Client, error) {
		return New(cfg)
	})
}
