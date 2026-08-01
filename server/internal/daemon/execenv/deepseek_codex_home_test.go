package execenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		`wire_api = "chat"`,
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
