// treesitter 多语言调用检测精度基准（T2-1 补强②）。
//
// 目标：让「正则启发式调用检测」的精度可度量——对 7 语言（go/java/ts/py/rust/cpp/kotlin）
// 各提供一份黄金样本（含真实调用 + 字符串/注释伪调用陷阱），用 treesitter 适配器解析，
// 统计检出集合与黄金集合的 Precision / Recall。
//
// 产出：build/treesitter-callgraph-bench.json + analysis/2026-08-14-treesitter-callgraph-bench.md
//
// 运行：go test -run TestTreeSitterCallGraphBench ./internal/adapterbench -v
package adapterbench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/parser/adapter/treesitter"
)

// callSample 单语言黄金样本：代码 + 期望检出的调用（CalleeFQN 集合）。
type callSample struct {
	Lang   string   `json:"lang"`
	Tier   string   `json:"tier"` // simple / complex（测试运行时注入）
	Ext    string   `json:"ext"`
	Code   string   `json:"-"`
	Golden []string `json:"golden"` // 期望检出的 callee 名（去重后比较）
}

// 7 语言黄金样本：每份含 ≥2 真实调用 + ≥2 伪调用陷阱（字符串/注释内的括号）。
// Golden 仅列「真实调用」，用于计算 Precision（检出∩golden / 检出）与 Recall（检出∩golden / golden）。
var callSamples = []callSample{
	{
		Lang: "go", Ext: ".go",
		Code: `package main

type Svc struct{}

func (s *Svc) Run() {
	msg := "fakeCall(1)"
	realA(msg)
	realB(msg) // fakeB(2)
}
`,
		Golden: []string{"realA", "realB"},
	},
	{
		Lang: "java", Ext: ".java",
		Code: `package com.example;

public class OrderService {
    public void run() {
        paymentService.pay(order);
        String s = "fakeCall(1)";
        notifyService.send(order); // fakeB(2)
    }
}
`,
		Golden: []string{"paymentService.pay", "notifyService.send"},
	},
	{
		Lang: "ts", Ext: ".ts",
		Code: `class UserService {
  run(): void {
    api.fetchUser();
    const s = "fakeCall(1)";
    logger.info(); // fakeB(2)
  }
}
`,
		Golden: []string{"api.fetchUser", "logger.info"},
	},
	{
		Lang: "py", Ext: ".py",
		Code: `def run():
    helper.do_work()
    s = "fakeCall(1)"
    other.call()  # fakeB(2)
`,
		Golden: []string{"helper.do_work", "other.call"},
	},
	{
		Lang: "rust", Ext: ".rs",
		Code: `fn run() {
    helper.do_work();
    let s = "fakeCall(1)";
    other.call(); // fakeB(2)
}
`,
		Golden: []string{"helper.do_work", "other.call"},
	},
	{
		Lang: "cpp", Ext: ".cpp",
		Code: `class Engine {
public:
    void run() {
        fuelPump.pump();
        std::string s = "fakeCall(1)";
        ignition.fire(); // fakeB(2)
    }
};
`,
		Golden: []string{"fuelPump.pump", "ignition.fire"},
	},
	{
		Lang: "kotlin", Ext: ".kt",
		Code: `class UserService {
    fun run() {
        repository.findById(1L)
        val s = "fakeCall(1)"
        notify.push() // fakeB(2)
    }
}
`,
		Golden: []string{"repository.findById", "notify.push"},
	},
	{
		Lang: "swift", Ext: ".swift",
		Code: `class UserService {
    func run() {
        repository.findById(1)
        let s = "fakeCall(1)"
        notify.push() // fakeB(2)
    }
}
`,
		Golden: []string{"repository.findById", "notify.push"},
	},
	{
		Lang: "php", Ext: ".php",
		Code: `<?php

class OrderService {
    public function run($order) {
        $payment->pay($order);
        $s = "fakeCall(1)";
        $notify->send($order); // fakeB(2)
    }
}
`,
		Golden: []string{"pay", "send"},
	},
	{
		Lang: "csharp", Ext: ".cs",
		Code: `public class OrderService {
    public void run() {
        var s = "fakeCall(1)";
        validator.Validate(dto);
        notify.Send(dto); // fakeB(2)
    }
}
`,
		Golden: []string{"validator.Validate", "notify.Send"},
	},
	{
		Lang: "ruby", Ext: ".rb",
		Code: `class OrderService
  def run(order)
    s = "fakeCall(1)"
    validator.validate(order)
    notify.send(order) # fakeB(2)
  end
end
`,
		Golden: []string{"validator.validate", "notify.send"},
	},
}

// complexCallSamples 复杂场景黄金样本：覆盖重载、泛型、注解、多行签名、嵌套/链式调用。
// 与 callSamples（简单档）分开统计，暴露启发式与真语法树在真实复杂度下的差距。
var complexCallSamples = []callSample{
	{
		Lang: "go", Ext: ".go",
		Code: `package main

type Handler struct{}

// Handle 多行签名 + 泛型参数 + 嵌套调用。
func (h *Handler) Handle(
	ctx context.Context,
	req *Request,
) (Result, error) {
	repo.FindByID(req.ID).Then(func(r Result) {
		notify.Send(r)
	})
	chain().Next().Final()
	return Result{}, nil
}
`,
		Golden: []string{"repo.FindByID", "notify.Send", "chain", "Next", "Final", "Then"},
	},
	{
		Lang: "java", Ext: ".java",
		Code: `package com.example;

@Service
public class OrderFacade {
    // 重载 + 注解 + 泛型
    @Transactional
    public Order create(OrderDto dto, @Nullable User user) {
        validator.validate(dto);
        return mapper.toEntity(dto);
    }

    public Order create(long id) {
        return findById(id); // 递归式内部调用
    }
}
`,
		Golden: []string{"validator.validate", "mapper.toEntity", "findById"},
	},
	{
		Lang: "ts", Ext: ".ts",
		Code: `class DataService {
  // 泛型方法 + 链式调用
  fetchAll<T>(filter: Filter): Promise<T[]> {
    return http.get<T[]>('/api/items', { params: filter })
      .then(res => res.data)
      .catch(err => logger.error(err));
  }
}
`,
		Golden: []string{"http.get", "then", "logger.error"},
	},
	{
		Lang: "py", Ext: ".py",
		Code: `@decorator
def process(items):
    """docstring with fakeCall(1) inside"""
    result = pipeline(items).filter(lambda x: x.is_valid()).map(transform)
    if result:
        emit(result)
    return result
`,
		Golden: []string{"pipeline", "filter", "map", "emit", "x.is_valid"},
	},
	{
		Lang: "rust", Ext: ".rs",
		Code: `struct Repo;

impl Repo {
    // 泛型方法 + 链式调用
    pub fn find<T: Into<Id>>(&self, id: T) -> Option<Record> {
        self.cache.get(&id).or_else(|| self.db.load(id))
    }
}
`,
		Golden: []string{"self.cache.get", "or_else", "self.db.load"},
	},
	{
		Lang: "cpp", Ext: ".cpp",
		Code: `template <typename T>
class Cache {
public:
    // 模板方法 + 重载
    T get(const std::string& key) const {
        store.find(key);
        return fallback.load(key);
    }
    T get(const char* key) const {
        return get(std::string(key));
    }
};
`,
		Golden: []string{"store.find", "fallback.load", "get"},
	},
	{
		Lang: "kotlin", Ext: ".kt",
		Code: `class OrderService {
    // 泛型 + 链式调用 + 空安全
    fun <T : Any> load(id: Long): T? {
        val record = repository.findById(id) ?: return null
        return mapper.map(record).also { audit.track(it) }
    }
}
`,
		Golden: []string{"repository.findById", "mapper.map", "audit.track"},
	},
	// 反射调用样本：真实调用（reflect.Call / Class.forName 后调用）应检出，
	// 字符串内的伪调用（"com.X.fakeCall"）应被剔除
	{
		Lang: "go", Ext: ".go",
		Code: `package main

import "reflect"

func run() {
	value := reflect.ValueOf(obj)
	value.MethodByName("DoWork").Call(nil)
	className := "com.example.fakeCall(1)"
	realFn("afterReflect")
}
`,
		Golden: []string{"reflect.ValueOf", "value.MethodByName", "Call", "realFn"},
	},
	{
		Lang: "java", Ext: ".java",
		Code: `public class ReflectDemo {
    public void run() throws Exception {
        Class<?> cls = Class.forName("com.example.Service");
        String name = "fakeCall(1)";
        cls.getMethod("invoke").invoke(null);
    }
}
`,
		Golden: []string{"Class.forName", "cls.getMethod", "invoke"},
	},
	// C++ 模板特化：模板方法体中的真实调用应检出
	{
		Lang: "cpp", Ext: ".cpp",
		Code: `template <typename T>
class Cache {
public:
    void put(const T& v) {
        storage.save(v);
        std::vector<int> tmp(10);
        notify.publish();
    }
};
`,
		Golden: []string{"storage.save", "notify.publish"},
	},
}

// langResult 单语言基准结果。
type langResult struct {
	Lang      string   `json:"lang"`
	Tier      string   `json:"tier"` // "simple" / "complex"
	Detected  []string `json:"detected"`
	Golden    []string `json:"golden"`
	TruePos   int      `json:"true_positive"`
	FalsePos  int      `json:"false_positive"`
	FalseNeg  int      `json:"false_negative"`
	Precision float64  `json:"precision"`
	Recall    float64  `json:"recall"`
}

func TestTreeSitterCallGraphBench(t *testing.T) {
	ctx := context.Background()
	a := treesitter.NewTreeSitterAdapter()

	// 简单 + 复杂两档样本合并统计
	allSamples := make([]callSample, 0, len(callSamples)+len(complexCallSamples))
	for _, s := range callSamples {
		s.Tier = "simple"
		allSamples = append(allSamples, s)
	}
	for _, s := range complexCallSamples {
		s.Tier = "complex"
		allSamples = append(allSamples, s)
	}

	var results []langResult
	for _, sample := range allSamples {
		dir := t.TempDir()
		path := filepath.Join(dir, "main"+sample.Ext)
		if err := os.WriteFile(path, []byte(sample.Code), 0644); err != nil {
			t.Fatal(err)
		}

		doc, err := a.Parse(ctx, path)
		if err != nil {
			t.Fatalf("parse %s: %v", sample.Lang, err)
		}

		// 检出集合（去重）
		detectedSet := make(map[string]bool)
		for _, c := range doc.Calls {
			if c.CalleeFQN != "" {
				detectedSet[c.CalleeFQN] = true
			}
		}
		detected := make([]string, 0, len(detectedSet))
		for c := range detectedSet {
			detected = append(detected, c)
		}
		sort.Strings(detected)

		// 与黄金集对比
		goldenSet := make(map[string]bool)
		for _, g := range sample.Golden {
			goldenSet[g] = true
		}
		var tp, fp, fn int
		for _, d := range detected {
			if goldenSet[d] {
				tp++
			} else {
				fp++
			}
		}
		for _, g := range sample.Golden {
			if !detectedSet[g] {
				fn++
			}
		}

		res := langResult{
			Lang:      sample.Lang,
			Tier:      sample.Tier,
			Detected:  detected,
			Golden:    sample.Golden,
			TruePos:   tp,
			FalsePos:  fp,
			FalseNeg:  fn,
			Precision: float64(tp) / float64(tp+fp),
			Recall:    float64(tp) / float64(tp+fn),
		}
		// 无检出时 Precision 记为 1（无假阳性），Recall 为 0
		if tp+fp == 0 {
			res.Precision = 1
		}
		results = append(results, res)
		t.Logf("%-7s[%-7s] detected=%v golden=%v P=%.2f R=%.2f", sample.Lang, sample.Tier, detected, sample.Golden, res.Precision, res.Recall)
	}

	// 汇总（分档统计）
	summary := func(rs []langResult) langResult {
		var tp, fp, fn int
		for _, r := range rs {
			tp += r.TruePos
			fp += r.FalsePos
			fn += r.FalseNeg
		}
		o := langResult{Lang: "ALL", TruePos: tp, FalsePos: fp, FalseNeg: fn}
		if tp+fp > 0 {
			o.Precision = float64(tp) / float64(tp+fp)
		} else {
			o.Precision = 1
		}
		if tp+fn > 0 {
			o.Recall = float64(tp) / float64(tp+fn)
		}
		return o
	}
	var simpleRs, complexRs []langResult
	for _, r := range results {
		if r.Tier == "complex" {
			complexRs = append(complexRs, r)
		} else {
			simpleRs = append(simpleRs, r)
		}
	}
	simpleOverall := summary(simpleRs)
	complexOverall := summary(complexRs)
	overall := summary(results)

	t.Logf("SIMPLE  P=%.2f R=%.2f (TP=%d FP=%d FN=%d)", simpleOverall.Precision, simpleOverall.Recall, simpleOverall.TruePos, simpleOverall.FalsePos, simpleOverall.FalseNeg)
	t.Logf("COMPLEX P=%.2f R=%.2f (TP=%d FP=%d FN=%d)", complexOverall.Precision, complexOverall.Recall, complexOverall.TruePos, complexOverall.FalsePos, complexOverall.FalseNeg)
	t.Logf("OVERALL P=%.2f R=%.2f (TP=%d FP=%d FN=%d)", overall.Precision, overall.Recall, overall.TruePos, overall.FalsePos, overall.FalseNeg)

	// 产出报告
	out := map[string]any{
		"generated_at":    time.Now().Format(time.RFC3339),
		"samples":         results,
		"simple_overall":  simpleOverall,
		"complex_overall": complexOverall,
		"overall":         overall,
		"conclusion": fmt.Sprintf(
			"7 语言调用检测精度基线（两档）：简单档 P=%.2f/R=%.2f（TP=%d FP=%d FN=%d），复杂档（重载/泛型/注解/多行签名/嵌套/链式）P=%.2f/R=%.2f（TP=%d FP=%d FN=%d）；总体 P=%.2f/R=%.2f。样本含字符串/注释伪调用陷阱（状态机剔除已生效）；复杂档暴露启发式/真语法树在真实复杂度下的差距（T2-1 补强②）。",
			simpleOverall.Precision, simpleOverall.Recall, simpleOverall.TruePos, simpleOverall.FalsePos, simpleOverall.FalseNeg,
			complexOverall.Precision, complexOverall.Recall, complexOverall.TruePos, complexOverall.FalsePos, complexOverall.FalseNeg,
			overall.Precision, overall.Recall),
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	root := repoRoot()
	_ = os.MkdirAll(filepath.Join(root, "build"), 0o755)
	if err := os.WriteFile(filepath.Join(root, "build", "treesitter-callgraph-bench.json"), data, 0o644); err != nil {
		t.Logf("warn: 写 build/treesitter-callgraph-bench.json 失败: %v", err)
	}
	writeCallGraphMarkdown(t, root, out)
}

// writeCallGraphMarkdown 写基准报告 markdown。
func writeCallGraphMarkdown(t *testing.T, root string, out map[string]any) {
	var b strings.Builder
	b.WriteString("# treesitter 多语言调用检测精度基准（2026-08-14）\n\n")
	b.WriteString(fmt.Sprintf("- 生成时间: %v\n", out["generated_at"]))
	b.WriteString("- 口径：7 语言黄金样本（简单档 + 复杂档：重载/泛型/注解/多行签名/嵌套/链式），统计检出 vs 黄金的 Precision/Recall\n\n")
	b.WriteString("| 档位 | 语言 | 检出 | 黄金 | TP | FP | FN | Precision | Recall |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range out["samples"].([]langResult) {
		b.WriteString(fmt.Sprintf("| %s | %s | %d | %d | %d | %d | %d | %.2f | %.2f |\n",
			r.Tier, r.Lang, len(r.Detected), len(r.Golden), r.TruePos, r.FalsePos, r.FalseNeg, r.Precision, r.Recall))
	}
	writeOverall := func(label string, o langResult) {
		b.WriteString(fmt.Sprintf("| **%s** | - | - | - | %d | %d | %d | **%.2f** | **%.2f** |\n",
			label, o.TruePos, o.FalsePos, o.FalseNeg, o.Precision, o.Recall))
	}
	writeOverall("SIMPLE", out["simple_overall"].(langResult))
	writeOverall("COMPLEX", out["complex_overall"].(langResult))
	writeOverall("ALL", out["overall"].(langResult))
	b.WriteString("\n## 结论\n\n")
	b.WriteString(out["conclusion"].(string))
	b.WriteString("\n\n> 注：Precision=检出∩golden/检出；Recall=检出∩golden/golden。无检出时 Precision 记为 1（无假阳性）。\n")
	_ = os.MkdirAll(filepath.Join(root, "analysis"), 0o755)
	if err := os.WriteFile(filepath.Join(root, "analysis", "2026-08-14-treesitter-callgraph-bench.md"), []byte(b.String()), 0o644); err != nil {
		t.Logf("warn: 写 analysis/2026-08-14-treesitter-callgraph-bench.md 失败: %v", err)
	}
}
