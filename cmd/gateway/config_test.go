package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SanjanaMShetty/distributed-rate-limiter/internal/engine"
)

func TestLoadRulesIncludesEachAlgorithmAndWindow(t *testing.T) {
	rulesFile, err := filepath.Abs(filepath.Join("..", "..", "deploy", "compose", "rules.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIMIT_RULES_FILE", rulesFile)
	rules, err := loadRules()
	if err != nil {
		t.Fatal(err)
	}
	algorithms := map[string]bool{}
	windowRules := map[string]bool{}
	windows := map[string]time.Duration{
		"benchmark-fixed-window":      time.Minute,
		"benchmark-sliding-window":    time.Minute,
		"fixed-window-demo":           time.Second,
		"sliding-window-counter-demo": time.Second,
	}
	for _, rule := range rules {
		algorithms[rule.Algorithm] = true
		if want, ok := windows[rule.Name]; ok {
			windowRules[rule.Name] = true
			if rule.Window != want {
				t.Errorf("%s window = %s, want %s", rule.Name, rule.Window, want)
			}
		}
	}
	for name := range windows {
		if !windowRules[name] {
			t.Errorf("rules file does not include window rule %q", name)
		}
	}
	for _, name := range []string{engine.AlgorithmTokenBucket, engine.AlgorithmFixedWindow, engine.AlgorithmSlidingWindowCounter} {
		if !algorithms[name] {
			t.Errorf("rules file does not exercise algorithm %q", name)
		}
	}
}

func TestLoadRulesRejectsUnsupportedAlgorithm(t *testing.T) {
	rulesFile := filepath.Join(t.TempDir(), "rules.json")
	content := `[{"name":"invalid","path":"/invalid","capacity":5,"refill_per_second":2,"key_ttl":"60s","algorithm":"unknown","identity":"api_key"}]`
	if err := os.WriteFile(rulesFile, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIMIT_RULES_FILE", rulesFile)
	if _, err := loadRules(); err == nil {
		t.Fatal("expected unknown algorithm to be rejected")
	}
}
