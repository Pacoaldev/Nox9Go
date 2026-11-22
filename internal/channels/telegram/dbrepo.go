package telegram

import (
	"nox9go/proxy/internal/config"
	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/notify"
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

// EnsureTelegramAPIKey issues the channel's client API key through the shared
// vault when it does not exist yet.
func (r *DBRepo) EnsureTelegramAPIKey() (string, error) {
	return r.Repo.EnsureTelegramAPIKey()
}

// DevRepoRoot reports the checkout the development report reads, from
// DEV_REPO_ROOT in the environment or the gateway's env file. An empty value
// disables the report instead of guessing: a gateway started from anywhere
// must not read an unrelated repository.
func (r *DBRepo) DevRepoRoot() (string, error) {
	return config.DevRepoRoot(), nil
}

// DevFeatureName reports the ODD feature the report follows by default, from
// DEV_FEATURE in the environment or the gateway's env file.
func (r *DBRepo) DevFeatureName() (string, error) {
	return config.DevFeature(), nil
}

// DevSpool returns the development event feed from DEV_SPOOL_PATH. An empty
// value disables the feed, so a gateway with no development session attached
// forwards nothing instead of watching a file that will never exist.
func (r *DBRepo) DevSpool() (*notify.Spool, error) {
	path := config.DevSpoolPath()
	if path == "" {
		return &notify.Spool{}, nil
	}
	return notify.NewSpool(path), nil
}

// DevInbox returns the instruction inbox under the data dir by default, so
// /pi works with no configuration; the env value wins when set.
func (r *DBRepo) DevInbox() (*notify.Spool, error) {
	return notify.NewSpool(config.DevInboxPath()), nil
}

// ResumeTelegramSession reopens an archived session (S5a). The delegation is
// deliberately one line: the transaction and the single-open-session invariant
// belong to the storage layer, which is where StartTelegramSession lives too.
func (r *DBRepo) ResumeTelegramSession(sessionID int64) error {
	return r.Repo.ResumeTelegramSession(sessionID)
}

// compile-time proof the adapter satisfies the interface the supervisor uses.
var _ Repo = (*DBRepo)(nil)
