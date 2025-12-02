// Package resolver implements GraphQL resolvers for Rafale.
package resolver

import (
	"github.com/0xredeth/Rafale/pkg/config"
	"github.com/0xredeth/Rafale/internal/rpc"
	"github.com/0xredeth/Rafale/internal/store"
)

// Resolver provides dependencies for GraphQL resolvers.
type Resolver struct {
	Config *config.Config
	Store  *store.Store
	RPC    *rpc.Client
}

// NewResolver creates a new resolver with dependencies.
//
// Parameters:
//   - cfg (*config.Config): application configuration
//   - store (*store.Store): database store
//   - rpc (*rpc.Client): RPC client
//
// Returns:
//   - *Resolver: initialized resolver
func NewResolver(cfg *config.Config, store *store.Store, rpc *rpc.Client) *Resolver {
	return &Resolver{
		Config: cfg,
		Store:  store,
		RPC:    rpc,
	}
}
