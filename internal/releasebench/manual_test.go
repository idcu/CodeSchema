package releasebench

import (
	"sort"
	"strings"
	"testing"
)

func TestManualDatasetValid(t *testing.T) {
	ds, err := LoadManualDataset("testdata/manual-truth.json")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if ds.Version != 1 {
		t.Fatalf("version = %d, want 1", ds.Version)
	}
	if len(ds.Repos) < 2 {
		t.Fatalf("repos = %d, want >= 2 (go + java)", len(ds.Repos))
	}
	for name, repo := range ds.Repos {
		if repo.Language == "" {
			t.Fatalf("%s: language empty", name)
		}
		if len(repo.Entries) == 0 {
			t.Fatalf("%s: no entries", name)
		}
		for _, e := range repo.Entries {
			if e.Production == "" || len(e.TestFiles) == 0 || len(e.Tests) == 0 {
				t.Fatalf("%s: incomplete entry %+v", name, e)
			}
			seen := make(map[string]struct{}, len(e.Tests))
			for _, test := range e.Tests {
				if _, dup := seen[test]; dup {
					t.Fatalf("%s/%s: duplicate truth test %q", name, e.Production, test)
				}
				seen[test] = struct{}{}
				if !strings.Contains(test, ".") || strings.ContainsAny(test, " \t") {
					t.Fatalf("%s: malformed test FQN %q", name, test)
				}
			}
		}
	}
}

func TestScoreManualClosedScope(t *testing.T) {
	entries := []ManualEntry{
		{
			Production: "Service.Run",
			TestFiles:  []string{"service_test.go"},
			Tests:      []string{"service.TestRunA", "service.TestRunB"},
		},
	}
	// A 命中；B 漏召；C 是范围外测试（不计 FP）；D 是范围内误报。
	inScope := map[string]struct{}{
		"service.TestRunA": {},
		"service.TestRunB": {},
		"service.TestRunD": {},
	}
	query := func(string) ([]string, error) {
		return []string{"service.TestRunA", "service.TestRunC", "service.TestRunD", "service.TestRunA"}, nil
	}

	results, micro, predicted, err := scoreManual(entries, inScope, query)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	got := results[0]
	if got.TP != 1 || got.FP != 1 || got.FN != 1 {
		t.Fatalf("TP/FP/FN = %d/%d/%d, want 1/1/1", got.TP, got.FP, got.FN)
	}
	if predicted != 2 {
		t.Fatalf("predicted in scope = %d, want 2 (dedup, out-of-scope ignored)", predicted)
	}
	wantPred := []string{"service.TestRunA", "service.TestRunD"}
	if !sort.StringsAreSorted(got.Predicted) || strings.Join(got.Predicted, ",") != strings.Join(wantPred, ",") {
		t.Fatalf("predicted = %v, want %v", got.Predicted, wantPred)
	}
	if micro.TP != 1 || micro.Precision != 0.5 || micro.Recall != 0.5 || micro.F1 != 0.5 {
		t.Fatalf("micro = %+v, want TP1 P/R/F1 0.5", micro)
	}
}
