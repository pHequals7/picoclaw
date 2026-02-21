package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/caarlos0/env/v11"
)

// FlexibleStringSlice is a []string that also accepts JSON numbers,
// so allow_from can contain both "123" and 123.
type FlexibleStringSlice []string

func (f *FlexibleStringSlice) UnmarshalJSON(data []byte) error {
	// Try []string first
	var ss []string
	if err := json.Unmarshal(data, &ss); err == nil {
		*f = ss
		return nil
	}

	// Try []interface{} to handle mixed types
	var raw []interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	result := make([]string, 0, len(raw))
	for _, v := range raw {
		switch val := v.(type) {
		case string:
			result = append(result, val)
		case float64:
			result = append(result, fmt.Sprintf("%.0f", val))
		default:
			result = append(result, fmt.Sprintf("%v", val))
		}
	}
	*f = result
	return nil
}

type Config struct {
	Agents     AgentsConfig     `json:"agents"`
	Channels   ChannelsConfig   `json:"channels"`
	Providers  ProvidersConfig  `json:"providers"`
	Gateway    GatewayConfig    `json:"gateway"`
	Tools      ToolsConfig      `json:"tools"`
	Heartbeat  HeartbeatConfig  `json:"heartbeat"`
	Devices    DevicesConfig    `json:"devices"`
	Logging    LoggingConfig    `json:"logging"`
	Visibility VisibilityConfig `json:"visibility"`
	mu         sync.RWMutex
}

type AgentsConfig struct {
	Defaults AgentDefaults `json:"defaults"`
	Failover AgentFailover `json:"failover"`
	Planner  AgentPlanner  `json:"planner"`
}

type AgentDefaults struct {
	Workspace           string   `json:"workspace" env:"PICOCLAW_AGENTS_DEFAULTS_WORKSPACE"`
	RestrictToWorkspace bool     `json:"restrict_to_workspace" env:"PICOCLAW_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE"`
	Provider            string   `json:"provider" env:"PICOCLAW_AGENTS_DEFAULTS_PROVIDER"`
	Model               string   `json:"model" env:"PICOCLAW_AGENTS_DEFAULTS_MODEL"`
	MaxTokens           int      `json:"max_tokens" env:"PICOCLAW_AGENTS_DEFAULTS_MAX_TOKENS"`
	Temperature         float64  `json:"temperature" env:"PICOCLAW_AGENTS_DEFAULTS_TEMPERATURE"`
	MaxToolIterations   int      `json:"max_tool_iterations" env:"PICOCLAW_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS"`
	FallbackModel       string   `json:"fallback_model" env:"PICOCLAW_AGENTS_DEFAULTS_FALLBACK_MODEL"`
	FallbackModels      []string `json:"fallback_models" env:"PICOCLAW_AGENTS_DEFAULTS_FALLBACK_MODELS"`
	HTTPTimeout         int      `json:"http_timeout" env:"PICOCLAW_AGENTS_DEFAULTS_HTTP_TIMEOUT"`
}

type AgentFailover struct {
	Enabled                      bool `json:"enabled" env:"PICOCLAW_AGENTS_FAILOVER_ENABLED"`
	HoldMinutes                  int  `json:"hold_minutes" env:"PICOCLAW_AGENTS_FAILOVER_HOLD_MINUTES"`
	ProbeIntervalMinutes         int  `json:"probe_interval_minutes" env:"PICOCLAW_AGENTS_FAILOVER_PROBE_INTERVAL_MINUTES"`
	ProbeSuccessThreshold        int  `json:"probe_success_threshold" env:"PICOCLAW_AGENTS_FAILOVER_PROBE_SUCCESS_THRESHOLD"`
	ProbeFailureBackoffMinutes   int  `json:"probe_failure_backoff_minutes" env:"PICOCLAW_AGENTS_FAILOVER_PROBE_FAILURE_BACKOFF_MINUTES"`
	NotifyOnSwitch               bool `json:"notify_on_switch" env:"PICOCLAW_AGENTS_FAILOVER_NOTIFY_ON_SWITCH"`
	NotifyOnFallbackUse          bool `json:"notify_on_fallback_use" env:"PICOCLAW_AGENTS_FAILOVER_NOTIFY_ON_FALLBACK_USE"`
	SwitchbackRequiresApproval   bool `json:"switchback_requires_approval" env:"PICOCLAW_AGENTS_FAILOVER_SWITCHBACK_REQUIRES_APPROVAL"`
	SwitchbackPromptCooldownMins int  `json:"switchback_prompt_cooldown_minutes" env:"PICOCLAW_AGENTS_FAILOVER_SWITCHBACK_PROMPT_COOLDOWN_MINUTES"`
	SwitchbackPromptTimeoutMins  int  `json:"switchback_prompt_timeout_minutes" env:"PICOCLAW_AGENTS_FAILOVER_SWITCHBACK_PROMPT_TIMEOUT_MINUTES"`
}

type AgentPlanner struct {
	Enabled bool   `json:"enabled" env:"PICOCLAW_AGENTS_PLANNER_ENABLED"`
	Model   string `json:"model" env:"PICOCLAW_AGENTS_PLANNER_MODEL"`
}

type ChannelsConfig struct {
	WhatsApp WhatsAppConfig `json:"whatsapp"`
	Telegram TelegramConfig `json:"telegram"`
	Feishu   FeishuConfig   `json:"feishu"`
	Discord  DiscordConfig  `json:"discord"`
	MaixCam  MaixCamConfig  `json:"maixcam"`
	QQ       QQConfig       `json:"qq"`
	DingTalk DingTalkConfig `json:"dingtalk"`
	Slack    SlackConfig    `json:"slack"`
	LINE     LINEConfig     `json:"line"`
	OneBot   OneBotConfig   `json:"onebot"`
}

type WhatsAppConfig struct {
	Enabled   bool                `json:"enabled" env:"PICOCLAW_CHANNELS_WHATSAPP_ENABLED"`
	BridgeURL string              `json:"bridge_url" env:"PICOCLAW_CHANNELS_WHATSAPP_BRIDGE_URL"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"PICOCLAW_CHANNELS_WHATSAPP_ALLOW_FROM"`
}

type TelegramConfig struct {
	Enabled   bool                `json:"enabled" env:"PICOCLAW_CHANNELS_TELEGRAM_ENABLED"`
	Token     string              `json:"token" env:"PICOCLAW_CHANNELS_TELEGRAM_TOKEN"`
	Proxy     string              `json:"proxy" env:"PICOCLAW_CHANNELS_TELEGRAM_PROXY"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"PICOCLAW_CHANNELS_TELEGRAM_ALLOW_FROM"`
}

type FeishuConfig struct {
	Enabled           bool                `json:"enabled" env:"PICOCLAW_CHANNELS_FEISHU_ENABLED"`
	AppID             string              `json:"app_id" env:"PICOCLAW_CHANNELS_FEISHU_APP_ID"`
	AppSecret         string              `json:"app_secret" env:"PICOCLAW_CHANNELS_FEISHU_APP_SECRET"`
	EncryptKey        string              `json:"encrypt_key" env:"PICOCLAW_CHANNELS_FEISHU_ENCRYPT_KEY"`
	VerificationToken string              `json:"verification_token" env:"PICOCLAW_CHANNELS_FEISHU_VERIFICATION_TOKEN"`
	AllowFrom         FlexibleStringSlice `json:"allow_from" env:"PICOCLAW_CHANNELS_FEISHU_ALLOW_FROM"`
}

type DiscordConfig struct {
	Enabled   bool                `json:"enabled" env:"PICOCLAW_CHANNELS_DISCORD_ENABLED"`
	Token     string              `json:"token" env:"PICOCLAW_CHANNELS_DISCORD_TOKEN"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"PICOCLAW_CHANNELS_DISCORD_ALLOW_FROM"`
}

type MaixCamConfig struct {
	Enabled   bool                `json:"enabled" env:"PICOCLAW_CHANNELS_MAIXCAM_ENABLED"`
	Host      string              `json:"host" env:"PICOCLAW_CHANNELS_MAIXCAM_HOST"`
	Port      int                 `json:"port" env:"PICOCLAW_CHANNELS_MAIXCAM_PORT"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"PICOCLAW_CHANNELS_MAIXCAM_ALLOW_FROM"`
}

type QQConfig struct {
	Enabled   bool                `json:"enabled" env:"PICOCLAW_CHANNELS_QQ_ENABLED"`
	AppID     string              `json:"app_id" env:"PICOCLAW_CHANNELS_QQ_APP_ID"`
	AppSecret string              `json:"app_secret" env:"PICOCLAW_CHANNELS_QQ_APP_SECRET"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"PICOCLAW_CHANNELS_QQ_ALLOW_FROM"`
}

type DingTalkConfig struct {
	Enabled      bool                `json:"enabled" env:"PICOCLAW_CHANNELS_DINGTALK_ENABLED"`
	ClientID     string              `json:"client_id" env:"PICOCLAW_CHANNELS_DINGTALK_CLIENT_ID"`
	ClientSecret string              `json:"client_secret" env:"PICOCLAW_CHANNELS_DINGTALK_CLIENT_SECRET"`
	AllowFrom    FlexibleStringSlice `json:"allow_from" env:"PICOCLAW_CHANNELS_DINGTALK_ALLOW_FROM"`
}

type SlackConfig struct {
	Enabled   bool                `json:"enabled" env:"PICOCLAW_CHANNELS_SLACK_ENABLED"`
	BotToken  string              `json:"bot_token" env:"PICOCLAW_CHANNELS_SLACK_BOT_TOKEN"`
	AppToken  string              `json:"app_token" env:"PICOCLAW_CHANNELS_SLACK_APP_TOKEN"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"PICOCLAW_CHANNELS_SLACK_ALLOW_FROM"`
}

type LINEConfig struct {
	Enabled            bool                `json:"enabled" env:"PICOCLAW_CHANNELS_LINE_ENABLED"`
	ChannelSecret      string              `json:"channel_secret" env:"PICOCLAW_CHANNELS_LINE_CHANNEL_SECRET"`
	ChannelAccessToken string              `json:"channel_access_token" env:"PICOCLAW_CHANNELS_LINE_CHANNEL_ACCESS_TOKEN"`
	WebhookHost        string              `json:"webhook_host" env:"PICOCLAW_CHANNELS_LINE_WEBHOOK_HOST"`
	WebhookPort        int                 `json:"webhook_port" env:"PICOCLAW_CHANNELS_LINE_WEBHOOK_PORT"`
	WebhookPath        string              `json:"webhook_path" env:"PICOCLAW_CHANNELS_LINE_WEBHOOK_PATH"`
	AllowFrom          FlexibleStringSlice `json:"allow_from" env:"PICOCLAW_CHANNELS_LINE_ALLOW_FROM"`
}

type OneBotConfig struct {
	Enabled            bool                `json:"enabled" env:"PICOCLAW_CHANNELS_ONEBOT_ENABLED"`
	WSUrl              string              `json:"ws_url" env:"PICOCLAW_CHANNELS_ONEBOT_WS_URL"`
	AccessToken        string              `json:"access_token" env:"PICOCLAW_CHANNELS_ONEBOT_ACCESS_TOKEN"`
	ReconnectInterval  int                 `json:"reconnect_interval" env:"PICOCLAW_CHANNELS_ONEBOT_RECONNECT_INTERVAL"`
	GroupTriggerPrefix []string            `json:"group_trigger_prefix" env:"PICOCLAW_CHANNELS_ONEBOT_GROUP_TRIGGER_PREFIX"`
	AllowFrom          FlexibleStringSlice `json:"allow_from" env:"PICOCLAW_CHANNELS_ONEBOT_ALLOW_FROM"`
}

type HeartbeatConfig struct {
	Enabled  bool `json:"enabled" env:"PICOCLAW_HEARTBEAT_ENABLED"`
	Interval int  `json:"interval" env:"PICOCLAW_HEARTBEAT_INTERVAL"` // minutes, min 5
}

type DevicesConfig struct {
	Enabled    bool `json:"enabled" env:"PICOCLAW_DEVICES_ENABLED"`
	MonitorUSB bool `json:"monitor_usb" env:"PICOCLAW_DEVICES_MONITOR_USB"`
}

type LoggingConfig struct {
	FileEnabled     bool   `json:"file_enabled" env:"PICOCLAW_LOGGING_FILE_ENABLED"`
	FilePath        string `json:"file_path" env:"PICOCLAW_LOGGING_FILE_PATH"`
	RotationEnabled bool   `json:"rotation_enabled" env:"PICOCLAW_LOGGING_ROTATION_ENABLED"`
	MaxAgeDays      int    `json:"max_age_days" env:"PICOCLAW_LOGGING_MAX_AGE_DAYS"`
	MaxSizeMB       int    `json:"max_size_mb" env:"PICOCLAW_LOGGING_MAX_SIZE_MB"`
}

type VisibilityConfig struct {
	Enabled          bool `json:"enabled" env:"PICOCLAW_VISIBILITY_ENABLED"`
	VerboseMode      bool `json:"verbose_mode" env:"PICOCLAW_VISIBILITY_VERBOSE_MODE"`
	UpdateIntervalMS int  `json:"update_interval_ms" env:"PICOCLAW_VISIBILITY_UPDATE_INTERVAL_MS"`
	ShowDuration     bool `json:"show_duration" env:"PICOCLAW_VISIBILITY_SHOW_DURATION"`
}

type ProvidersConfig struct {
	Anthropic     ProviderConfig `json:"anthropic"`
	OpenAI        ProviderConfig `json:"openai"`
	OpenRouter    ProviderConfig `json:"openrouter"`
	Groq          ProviderConfig `json:"groq"`
	Zhipu         ProviderConfig `json:"zhipu"`
	VLLM          ProviderConfig `json:"vllm"`
	Gemini        ProviderConfig `json:"gemini"`
	Nvidia        ProviderConfig `json:"nvidia"`
	Moonshot      ProviderConfig `json:"moonshot"`
	ShengSuanYun  ProviderConfig `json:"shengsuanyun"`
	DeepSeek      ProviderConfig `json:"deepseek"`
	GitHubCopilot ProviderConfig `json:"github_copilot"`
}

type ProviderConfig struct {
	APIKey      string `json:"api_key" env:"PICOCLAW_PROVIDERS_{{.Name}}_API_KEY"`
	APIBase     string `json:"api_base" env:"PICOCLAW_PROVIDERS_{{.Name}}_API_BASE"`
	Proxy       string `json:"proxy,omitempty" env:"PICOCLAW_PROVIDERS_{{.Name}}_PROXY"`
	AuthMethod  string `json:"auth_method,omitempty" env:"PICOCLAW_PROVIDERS_{{.Name}}_AUTH_METHOD"`
	ConnectMode string `json:"connect_mode,omitempty" env:"PICOCLAW_PROVIDERS_{{.Name}}_CONNECT_MODE"` //only for Github Copilot, `stdio` or `grpc`
}

type GatewayConfig struct {
	Host string `json:"host" env:"PICOCLAW_GATEWAY_HOST"`
	Port int    `json:"port" env:"PICOCLAW_GATEWAY_PORT"`
}

type BraveConfig struct {
	Enabled    bool   `json:"enabled" env:"PICOCLAW_TOOLS_WEB_BRAVE_ENABLED"`
	APIKey     string `json:"api_key" env:"PICOCLAW_TOOLS_WEB_BRAVE_API_KEY"`
	MaxResults int    `json:"max_results" env:"PICOCLAW_TOOLS_WEB_BRAVE_MAX_RESULTS"`
}

type DuckDuckGoConfig struct {
	Enabled    bool `json:"enabled" env:"PICOCLAW_TOOLS_WEB_DUCKDUCKGO_ENABLED"`
	MaxResults int  `json:"max_results" env:"PICOCLAW_TOOLS_WEB_DUCKDUCKGO_MAX_RESULTS"`
}

type WebToolsConfig struct {
	Brave      BraveConfig      `json:"brave"`
	DuckDuckGo DuckDuckGoConfig `json:"duckduckgo"`
}

type MCPServerConfig struct {
	Name               string            `json:"name"`
	Enabled            bool              `json:"enabled"`
	Transport          string            `json:"transport"` // command|streamable_http|sse
	Command            string            `json:"command,omitempty"`
	Args               []string          `json:"args,omitempty"`
	Env                map[string]string `json:"env,omitempty"`
	WorkingDir         string            `json:"working_dir,omitempty"`
	URL                string            `json:"url,omitempty"`
	Headers            map[string]string `json:"headers,omitempty"`
	ToolPrefix         string            `json:"tool_prefix,omitempty"`
	StartupTimeoutMS   int               `json:"startup_timeout_ms,omitempty"`
	CallTimeoutMS      int               `json:"call_timeout_ms,omitempty"`
	TerminateTimeoutMS int               `json:"terminate_timeout_ms,omitempty"`
}

type MCPToolsConfig struct {
	Enabled bool              `json:"enabled"`
	Servers []MCPServerConfig `json:"servers"`
}

type ToolsConfig struct {
	Web WebToolsConfig `json:"web"`
	MCP MCPToolsConfig `json:"mcp"`
}

func DefaultConfig() *Config {
	return &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Workspace:           "~/.picoclaw/workspace",
				RestrictToWorkspace: true,
				Provider:            "",
				Model:               "glm-4.7",
				MaxTokens:           8192,
				Temperature:         0.7,
				MaxToolIterations:   20,
			},
			Failover: AgentFailover{
				Enabled:                      true,
				HoldMinutes:                  300,
				ProbeIntervalMinutes:         60,
				ProbeSuccessThreshold:        2,
				ProbeFailureBackoffMinutes:   10,
				NotifyOnSwitch:               true,
				NotifyOnFallbackUse:          true,
				SwitchbackRequiresApproval:   true,
				SwitchbackPromptCooldownMins: 60,
				SwitchbackPromptTimeoutMins:  0,
			},
			Planner: AgentPlanner{
				Enabled: true,
				Model:   "gpt-5.1-mini",
			},
		},
		Channels: ChannelsConfig{
			WhatsApp: WhatsAppConfig{
				Enabled:   false,
				BridgeURL: "ws://localhost:3001",
				AllowFrom: FlexibleStringSlice{},
			},
			Telegram: TelegramConfig{
				Enabled:   false,
				Token:     "",
				AllowFrom: FlexibleStringSlice{},
			},
			Feishu: FeishuConfig{
				Enabled:           false,
				AppID:             "",
				AppSecret:         "",
				EncryptKey:        "",
				VerificationToken: "",
				AllowFrom:         FlexibleStringSlice{},
			},
			Discord: DiscordConfig{
				Enabled:   false,
				Token:     "",
				AllowFrom: FlexibleStringSlice{},
			},
			MaixCam: MaixCamConfig{
				Enabled:   false,
				Host:      "0.0.0.0",
				Port:      18790,
				AllowFrom: FlexibleStringSlice{},
			},
			QQ: QQConfig{
				Enabled:   false,
				AppID:     "",
				AppSecret: "",
				AllowFrom: FlexibleStringSlice{},
			},
			DingTalk: DingTalkConfig{
				Enabled:      false,
				ClientID:     "",
				ClientSecret: "",
				AllowFrom:    FlexibleStringSlice{},
			},
			Slack: SlackConfig{
				Enabled:   false,
				BotToken:  "",
				AppToken:  "",
				AllowFrom: FlexibleStringSlice{},
			},
			LINE: LINEConfig{
				Enabled:            false,
				ChannelSecret:      "",
				ChannelAccessToken: "",
				WebhookHost:        "0.0.0.0",
				WebhookPort:        18791,
				WebhookPath:        "/webhook/line",
				AllowFrom:          FlexibleStringSlice{},
			},
			OneBot: OneBotConfig{
				Enabled:            false,
				WSUrl:              "ws://127.0.0.1:3001",
				AccessToken:        "",
				ReconnectInterval:  5,
				GroupTriggerPrefix: []string{},
				AllowFrom:          FlexibleStringSlice{},
			},
		},
		Providers: ProvidersConfig{
			Anthropic:    ProviderConfig{},
			OpenAI:       ProviderConfig{},
			OpenRouter:   ProviderConfig{},
			Groq:         ProviderConfig{},
			Zhipu:        ProviderConfig{},
			VLLM:         ProviderConfig{},
			Gemini:       ProviderConfig{},
			Nvidia:       ProviderConfig{},
			Moonshot:     ProviderConfig{},
			ShengSuanYun: ProviderConfig{},
		},
		Gateway: GatewayConfig{
			Host: "0.0.0.0",
			Port: 18790,
		},
		Tools: ToolsConfig{
			Web: WebToolsConfig{
				Brave: BraveConfig{
					Enabled:    false,
					APIKey:     "",
					MaxResults: 5,
				},
				DuckDuckGo: DuckDuckGoConfig{
					Enabled:    true,
					MaxResults: 5,
				},
			},
			MCP: MCPToolsConfig{
				Enabled: false,
				Servers: []MCPServerConfig{},
			},
		},
		Heartbeat: HeartbeatConfig{
			Enabled:  true,
			Interval: 30, // default 30 minutes
		},
		Devices: DevicesConfig{
			Enabled:    false,
			MonitorUSB: true,
		},
		Logging: LoggingConfig{
			FileEnabled:     true,
			FilePath:        "~/.picoclaw/workspace/picoclaw.log",
			RotationEnabled: true,
			MaxAgeDays:      7,
			MaxSizeMB:       50,
		},
		Visibility: VisibilityConfig{
			Enabled:          true,
			VerboseMode:      false,
			UpdateIntervalMS: 1000,
			ShowDuration:     true,
		},
	}
}

func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	applyProviderEnvOverrides(cfg)
	resolveProviderEnvRefs(cfg)

	return cfg, nil
}

func applyProviderEnvOverrides(cfg *Config) {
	type providerEnvBinding struct {
		target *ProviderConfig
		apiKey string
	}
	bindings := []providerEnvBinding{
		{target: &cfg.Providers.Anthropic, apiKey: "PICOCLAW_PROVIDERS_ANTHROPIC_API_KEY"},
		{target: &cfg.Providers.OpenAI, apiKey: "PICOCLAW_PROVIDERS_OPENAI_API_KEY"},
		{target: &cfg.Providers.OpenRouter, apiKey: "PICOCLAW_PROVIDERS_OPENROUTER_API_KEY"},
		{target: &cfg.Providers.Groq, apiKey: "PICOCLAW_PROVIDERS_GROQ_API_KEY"},
		{target: &cfg.Providers.Zhipu, apiKey: "PICOCLAW_PROVIDERS_ZHIPU_API_KEY"},
		{target: &cfg.Providers.VLLM, apiKey: "PICOCLAW_PROVIDERS_VLLM_API_KEY"},
		{target: &cfg.Providers.Gemini, apiKey: "PICOCLAW_PROVIDERS_GEMINI_API_KEY"},
		{target: &cfg.Providers.Nvidia, apiKey: "PICOCLAW_PROVIDERS_NVIDIA_API_KEY"},
		{target: &cfg.Providers.Moonshot, apiKey: "PICOCLAW_PROVIDERS_MOONSHOT_API_KEY"},
		{target: &cfg.Providers.ShengSuanYun, apiKey: "PICOCLAW_PROVIDERS_SHENGSUANYUN_API_KEY"},
		{target: &cfg.Providers.DeepSeek, apiKey: "PICOCLAW_PROVIDERS_DEEPSEEK_API_KEY"},
		{target: &cfg.Providers.GitHubCopilot, apiKey: "PICOCLAW_PROVIDERS_GITHUB_COPILOT_API_KEY"},
	}

	for _, b := range bindings {
		if b.target == nil {
			continue
		}
		if v := strings.TrimSpace(os.Getenv(b.apiKey)); v != "" {
			b.target.APIKey = v
		}
	}
}

func resolveProviderEnvRefs(cfg *Config) {
	providers := []*ProviderConfig{
		&cfg.Providers.Anthropic,
		&cfg.Providers.OpenAI,
		&cfg.Providers.OpenRouter,
		&cfg.Providers.Groq,
		&cfg.Providers.Zhipu,
		&cfg.Providers.VLLM,
		&cfg.Providers.Gemini,
		&cfg.Providers.Nvidia,
		&cfg.Providers.Moonshot,
		&cfg.Providers.ShengSuanYun,
		&cfg.Providers.DeepSeek,
		&cfg.Providers.GitHubCopilot,
	}
	for _, p := range providers {
		if p == nil {
			continue
		}
		p.APIKey = resolveEnvRef(p.APIKey)
		p.APIBase = resolveEnvRef(p.APIBase)
		p.Proxy = resolveEnvRef(p.Proxy)
	}
}

func resolveEnvRef(v string) string {
	s := strings.TrimSpace(v)
	if s == "" {
		return v
	}
	if strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") {
		key := strings.TrimSpace(s[2 : len(s)-1])
		if key == "" {
			return v
		}
		if val, ok := os.LookupEnv(key); ok {
			return val
		}
		return v
	}
	if strings.HasPrefix(s, "$") && len(s) > 1 {
		key := strings.TrimSpace(s[1:])
		if key == "" {
			return v
		}
		if val, ok := os.LookupEnv(key); ok {
			return val
		}
	}
	return v
}

func SaveConfig(path string, cfg *Config) error {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

func (c *Config) WorkspacePath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return expandHome(c.Agents.Defaults.Workspace)
}

func (c *Config) GetAPIKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Providers.OpenRouter.APIKey != "" {
		return c.Providers.OpenRouter.APIKey
	}
	if c.Providers.Anthropic.APIKey != "" {
		return c.Providers.Anthropic.APIKey
	}
	if c.Providers.OpenAI.APIKey != "" {
		return c.Providers.OpenAI.APIKey
	}
	if c.Providers.Gemini.APIKey != "" {
		return c.Providers.Gemini.APIKey
	}
	if c.Providers.Zhipu.APIKey != "" {
		return c.Providers.Zhipu.APIKey
	}
	if c.Providers.Groq.APIKey != "" {
		return c.Providers.Groq.APIKey
	}
	if c.Providers.VLLM.APIKey != "" {
		return c.Providers.VLLM.APIKey
	}
	if c.Providers.ShengSuanYun.APIKey != "" {
		return c.Providers.ShengSuanYun.APIKey
	}
	return ""
}

func (c *Config) GetAPIBase() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Providers.OpenRouter.APIKey != "" {
		if c.Providers.OpenRouter.APIBase != "" {
			return c.Providers.OpenRouter.APIBase
		}
		return "https://openrouter.ai/api/v1"
	}
	if c.Providers.Zhipu.APIKey != "" {
		return c.Providers.Zhipu.APIBase
	}
	if c.Providers.VLLM.APIKey != "" && c.Providers.VLLM.APIBase != "" {
		return c.Providers.VLLM.APIBase
	}
	return ""
}

func expandHome(path string) string {
	if path == "" {
		return path
	}
	if path[0] == '~' {
		home, _ := os.UserHomeDir()
		if len(path) > 1 && path[1] == '/' {
			return home + path[1:]
		}
		return home
	}
	return path
}
