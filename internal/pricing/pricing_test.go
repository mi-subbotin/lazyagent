package pricing

import (
	"math"
	"testing"
)

func TestCost_KnownModel(t *testing.T) {
	got, ok := Cost(Usage{
		Model:        "claude-sonnet-4-6",
		InputTokens:  1_000_000,
		OutputTokens: 100_000,
	})
	if !ok {
		t.Fatal("Cost should succeed for known model")
	}
	want := 3.0 + 1.5 // 1M input @ $3 + 100k output @ $15/M
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("Cost = %f, want %f", got, want)
	}
}

func TestCost_DateSuffixStripping(t *testing.T) {
	got, ok := Cost(Usage{
		Model:        "claude-3-5-sonnet-20241022",
		InputTokens:  1_000_000,
		OutputTokens: 0,
	})
	if !ok {
		t.Fatal("Cost should resolve dated model name to base entry")
	}
	if math.Abs(got-3.0) > 1e-9 {
		t.Errorf("Cost = %f, want 3.0", got)
	}
}

func TestCost_UnknownModel(t *testing.T) {
	if _, ok := Cost(Usage{Model: "future-model-9000", InputTokens: 1000}); ok {
		t.Error("unknown model should return ok=false")
	}
	if _, ok := Cost(Usage{InputTokens: 1000}); ok {
		t.Error("empty model should return ok=false")
	}
}

func TestCost_CacheTokensAnthropic(t *testing.T) {
	// claude-sonnet-4-6 cache_read = 0.30/M; cache_create = 3.75/M
	got, ok := Cost(Usage{
		Model:             "claude-sonnet-4-6",
		CacheReadTokens:   1_000_000,
		CacheCreateTokens: 1_000_000,
	})
	if !ok {
		t.Fatal("Cost should succeed for cached tokens")
	}
	want := 0.30 + 3.75
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("cached cost = %f, want %f", got, want)
	}
}

func TestCost_CacheTokensFallbackToInput(t *testing.T) {
	// gpt-5 has no cache rates → cache tokens billed at input rate (1.25/M)
	got, ok := Cost(Usage{
		Model:           "gpt-5",
		CacheReadTokens: 1_000_000,
	})
	if !ok {
		t.Fatal("Cost should succeed even without cache fields")
	}
	if math.Abs(got-1.25) > 1e-9 {
		t.Errorf("fallback cost = %f, want 1.25", got)
	}
}

func TestUsageTotal(t *testing.T) {
	u := Usage{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 10}
	if u.Total() != 160 {
		t.Errorf("Total() = %d, want 160", u.Total())
	}
}

func TestModelsListNonEmpty(t *testing.T) {
	if len(Models()) == 0 {
		t.Error("Models() should be non-empty after embed")
	}
}
