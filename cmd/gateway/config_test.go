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
	for _, rule := range rules {
		algorithms[rule.Algorithm] = true
		if rule.Algorithm == engine.AlgorithmFixedWindow || rule.Algorithm == engine.AlgorithmSlidingWindowCounter {
			if rule.Window != time.Second {
				t.Errorf("%s window = %s, want 1s", rule.Name, rule.Window)
			}
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
