package treesitter

import "testing"

// TestIsKeywordNoPerCallAlloc 负控（结构性问题 2 的归因结论）：
// GOROOT 全量扫描时 isKeyword 占进程累计分配 79%（9.65 GiB），原因是每次调用
// 都重建关键字 map。关键字集合必须包级复用，单次判定零分配。
func TestIsKeywordNoPerCallAlloc(t *testing.T) {
	if !isKeyword("return") {
		t.Fatal("isKeyword(return) = false, want true")
	}
	if isKeyword("definitelyNotAKeyword") {
		t.Fatal("isKeyword(definitelyNotAKeyword) = true, want false")
	}
	if n := testing.AllocsPerRun(200, func() { _ = isKeyword("return") }); n != 0 {
		t.Fatalf("isKeyword allocates %.1f objects per call, want 0", n)
	}
}
