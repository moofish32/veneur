package veneur

import "time"

type ComputationRoutingConfig struct {
	Name           string         `yaml:"name"`
	FlushGroup     string         `yaml:"flush_group"`
	MatcherConfigs MatcherConfigs `yaml:"match"`
	WorkerInterval time.Duration  `yaml:"worker_interval"`
	WorkerCount    int            `yaml:"worker_count"`
	ForwardMetrics bool           `yaml:"forward_metrics"`
}

type SinkRoutingConfig struct {
	Name                    string           `yaml:"name"`
	FlushGroupSubscriptions []string         `yaml:"flush_group_subscriptions"`
	Sinks                   SinkRoutingSinks `yaml:"sinks"`
}

type SinkRoutingSinks struct {
	Matched    []string `yaml:"matched"`
	NotMatched []string `yaml:"not_matched"`
}
