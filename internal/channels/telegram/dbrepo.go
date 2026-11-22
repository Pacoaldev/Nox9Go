package telegram

import (
	"nox9go/proxy/internal/db"
)

// DBRepo adapts *db.Repo to the Repo interface this package needs.
//
// The adapter exists because the storage read model and the channel's config are
// two different types on purpose: the repository must not depend on the channel
// package, and the channel must not expose the repository's shape to the
// dashboard. Converting here keeps both directions independent.
type DBRepo struct {
	*db.Repo
}

// NewDBRepo wraps a repository for the channel.
func NewDBRepo(repo *db.Repo) *DBRepo { return &DBRepo{Repo: repo} }

// GetTelegramChannelConfig converts the stored read model into the channel
// config. The model and bot username are not stored on this path, so they are
// left for the caller to fill.
func (r *DBRepo) GetTelegramChannelConfig() (*Config, error) {
	stored, err := r.Repo.GetTelegramChannelConfig()
	if err != nil {
		return nil, err
	}
	return &Config{
		Enabled:        stored.Enabled,
		TokenPresent:   stored.TokenPresent,
		TokenMasked:    stored.TokenMasked,
		AllowedUserIDs: stored.AllowedUserIDs,
	}, nil
}

// compile-time proof the adapter satisfies the interface the supervisor uses.
var _ Repo = (*DBRepo)(nil)
