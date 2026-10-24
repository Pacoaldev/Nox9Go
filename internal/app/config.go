package app

import (
	"go.uber.org/fx"

	"nox9go/proxy/internal/config"
)

// ConfigModule loads config.Config, provides *viper.Viper, and provides CLI/environment parameters.
var ConfigModule = fx.Module("config",
	fx.Provide(
		config.ProvideViper,
		config.ProvideConfig,
		ProvideConfigValue,
		ProvideCLIParams,
		ProvideCLIParamsPtr,
	),
)

// ProvideConfigValue provides config.Config value from *config.Config.
func ProvideConfigValue(cfg *config.Config) config.Config {
	if cfg == nil {
		return config.Config{}
	}
	return *cfg
}

// ProvideCLIParams provides default CLI/environment parameters.
func ProvideCLIParams() CLIParams {
	return DefaultCLIParams()
}

// ProvideCLIParamsPtr provides a pointer to CLIParams.
func ProvideCLIParamsPtr(params CLIParams) *CLIParams {
	return &params
}
