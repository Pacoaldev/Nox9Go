package app

import (
	"context"
	"strings"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/handlers/chat"
	"nox9go/proxy/internal/handlers/media"
	"nox9go/proxy/internal/modelhealth"
)

// startModelHealthLoop starts the opt-in model-health loop (settings key
// "modelHealth", off by default). The configuration is read on every cycle, so
// enabling it needs no restart. It stops with the shutdown context.
func startModelHealthLoop(ctx context.Context, repo *db.Repo, chatH *chat.ChatHandler) {
	if repo == nil || chatH == nil {
		return
	}
	mediaH := media.NewMediaHandler(repo, nil, chatH)
	loop := modelhealth.NewLoop(
		repo,
		func(ctx context.Context, c modelhealth.Candidate) modelhealth.Verdict {
			res := mediaH.ProbeModelForHealth(ctx, c.Provider+"/"+c.Model)
			return modelhealth.Verdict{OK: res.OK, Blocked: res.Blocked, Error: res.Error}
		},
		func() []modelhealth.Candidate { return healthCandidates(repo) },
		func() modelhealth.Config {
			raw, err := repo.GetSettingsRaw()
			if err != nil {
				return modelhealth.ParseConfig(nil)
			}
			return modelhealth.ParseConfig(raw[modelhealth.SettingsKey])
		},
	)
	go loop.Run(ctx)
}

// healthCandidates lists the custom LLM models, addressed by the storage alias
// the disabledModels list is keyed by. Registry models are not watched:
// probing a catalogue nobody asked about is the cost this loop avoids.
func healthCandidates(repo *db.Repo) []modelhealth.Candidate {
	customs, err := repo.GetCustomModels()
	if err != nil {
		return nil
	}
	out := make([]modelhealth.Candidate, 0, len(customs))
	for _, cm := range customs {
		if cm == nil || cm.ID == "" || cm.ProviderAlias == "" {
			continue
		}
		switch strings.TrimSpace(cm.Type) {
		case "", "llm", "chat", "completion":
			out = append(out, modelhealth.Candidate{Provider: cm.ProviderAlias, Model: cm.ID})
		}
	}
	return out
}