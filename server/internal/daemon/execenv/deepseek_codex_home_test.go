package execenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestEnsureDeepSeekCodexProviderConfigPinsDeepSeek(t *testing.T) {
	t.Setenv(deepseekCodexBaseURLEnv, "https://router.example/deepseek")

	configPath := filepath.Join(t.TempDir(), "config.toml")
	initial := strings.Join([]string{
		`model_provider = "openai"`,
		``,
		deepseekCodexProviderBeginMark,
		`model_provider = "deepseek"`,
		`[model_providers.deepseek]`,
		`base_url = "https://stale.example"`,
		deepseekCodexProviderEndMark,
		``,
		`[model_providers.deepseek]`,
		`base_url = "https://user.example"`,
		``,
		`[model_providers.other]`,
		`base_url = "https://other.example"`,
		``,
	}, "\n")
	if err := os.WriteFile(configPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := ensureDeepSeekCodexProviderConfig(configPath); err != nil {
		t.Fatalf("ensure deepseek provider config: %v", err)
	}
	first, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(first)

	for _, want := range []string{
		deepseekCodexProviderBeginMark,
		`model_provider = "deepseek"`,
		`[model_providers.deepseek]`,
		`name = "DeepSeek"`,
		`base_url = "https://router.example/deepseek"`,
		`env_key = "DEEPSEEK_API_KEY"`,
		`wire_api = "responses"`,
		deepseekCodexProviderEndMark,
		`[model_providers.other]`,
		`base_url = "https://other.example"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("config missing %q:\n%s", want, content)
		}
	}
	for _, notWant := range []string{
		`model_provider = "openai"`,
		`https://stale.example`,
		`https://user.example`,
	} {
		if strings.Contains(content, notWant) {
			t.Fatalf("config still contains %q:\n%s", notWant, content)
		}
	}
	if count := strings.Count(content, deepseekCodexProviderBeginMark); count != 1 {
		t.Fatalf("managed block count = %d, want 1:\n%s", count, content)
	}

	if err := ensureDeepSeekCodexProviderConfig(configPath); err != nil {
		t.Fatalf("ensure deepseek provider config second call: %v", err)
	}
	second, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config second call: %v", err)
	}
	if string(second) != content {
		t.Fatalf("second ensure was not idempotent:\nfirst:\n%s\nsecond:\n%s", content, string(second))
	}
}

func TestEnsureDeepSeekCodexProviderConfigRequiresResponsesAdapterBaseURL(t *testing.T) {
	t.Setenv(deepseekCodexBaseURLEnv, "")

	configPath := filepath.Join(t.TempDir(), "config.toml")
	err := ensureDeepSeekCodexProviderConfig(configPath)
	if err == nil {
		t.Fatal("ensure deepseek provider config succeeded without adapter base URL")
	}
	for _, want := range []string{deepseekCodexBaseURLEnv, "Responses-compatible adapter/router", "official DeepSeek Chat Completions endpoint"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}

func TestEnsureDeepSeekCodexProviderConfigMatchesSupportedCodexResponsesSchema(t *testing.T) {
	t.Setenv(deepseekCodexBaseURLEnv, "https://router.example/v1")

	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := ensureDeepSeekCodexProviderConfig(configPath); err != nil {
		t.Fatalf("ensure deepseek provider config: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var cfg struct {
		ModelProvider  string `toml:"model_provider"`
		ModelProviders map[string]struct {
			Name    string `toml:"name"`
			BaseURL string `toml:"base_url"`
			EnvKey  string `toml:"env_key"`
			WireAPI string `toml:"wire_api"`
		} `toml:"model_providers"`
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse generated config as TOML: %v\n%s", err, string(data))
	}
	if cfg.ModelProvider != deepseekCodexProviderName {
		t.Fatalf("model_provider = %q, want %q", cfg.ModelProvider, deepseekCodexProviderName)
	}
	provider, ok := cfg.ModelProviders[deepseekCodexProviderName]
	if !ok {
		t.Fatalf("generated config missing model_providers.%s: %+v", deepseekCodexProviderName, cfg.ModelProviders)
	}
	if provider.Name != "DeepSeek" {
		t.Fatalf("provider name = %q, want DeepSeek", provider.Name)
	}
	if provider.BaseURL != "https://router.example/v1" {
		t.Fatalf("provider base_url = %q, want router URL", provider.BaseURL)
	}
	if provider.EnvKey != deepseekCodexAPIKeyEnv {
		t.Fatalf("provider env_key = %q, want %q", provider.EnvKey, deepseekCodexAPIKeyEnv)
	}
	if provider.WireAPI != "responses" {
		t.Fatalf("provider wire_api = %q, want responses", provider.WireAPI)
	}
}
