package ai

import "sync/atomic"

// Budget 管控 AI 增强层的调用次数硬限。
//
// 按作用域分为两类预算：
//   - 扫描期预算（perScan）：每次全量扫描的 AI 调用次数上限。
//   - 查询期预算（perQuery）：每次查询的 AI 调用次数上限。
//
// 计数器在每次扫描 / 查询开始时由调用方 Reset，超限后对应作用域的增强被跳过，
// 不影响主流程（索引始终可用）。limit 为负数表示不限制。
//
// 计数使用原子读写，保证「并发化 enhanceTagsWithAI（worker pool 并发调用
// LLM）」下无数据竞争（P21 生产化）。tryConsume* 的「检查+递增」为原子操作，
// 并发消费不会超发预算。
type Budget struct {
	scanLimit  int64
	queryLimit int64
	scanUsed   atomic.Int64
	queryUsed  atomic.Int64
}

// NewBudget 创建预算管控器。
// perScan / perQuery 为负表示对应作用域不限制调用次数。
func NewBudget(perScan, perQuery int) *Budget {
	return &Budget{scanLimit: int64(perScan), queryLimit: int64(perQuery)}
}

// tryConsumeScan 尝试消费一次扫描期预算；成功返回 true。并发安全（原子 CAS）。
func (b *Budget) tryConsumeScan() bool {
	if b.scanLimit < 0 {
		return true
	}
	for {
		cur := b.scanUsed.Load()
		if cur >= b.scanLimit {
			return false
		}
		if b.scanUsed.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

// tryConsumeQuery 尝试消费一次查询期预算；成功返回 true。并发安全（原子 CAS）。
func (b *Budget) tryConsumeQuery() bool {
	if b.queryLimit < 0 {
		return true
	}
	for {
		cur := b.queryUsed.Load()
		if cur >= b.queryLimit {
			return false
		}
		if b.queryUsed.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

// ResetScan 重置扫描期计数（每次扫描开始时调用）。
func (b *Budget) ResetScan() {
	b.scanUsed.Store(0)
}

// ResetQuery 重置查询期计数（每次查询开始时调用）。
func (b *Budget) ResetQuery() {
	b.queryUsed.Store(0)
}

// ScanRemaining 返回扫描期剩余可用次数（不限时返回 -1）。
func (b *Budget) ScanRemaining() int {
	if b.scanLimit < 0 {
		return -1
	}
	return int(b.scanLimit - b.scanUsed.Load())
}

// QueryRemaining 返回查询期剩余可用次数（不限时返回 -1）。
func (b *Budget) QueryRemaining() int {
	if b.queryLimit < 0 {
		return -1
	}
	return int(b.queryLimit - b.queryUsed.Load())
}

// ScanExhausted 报告扫描期预算是否已耗尽。
func (b *Budget) ScanExhausted() bool {
	return b.scanLimit >= 0 && b.scanUsed.Load() >= b.scanLimit
}

// QueryExhausted 报告查询期预算是否已耗尽。
func (b *Budget) QueryExhausted() bool {
	return b.queryLimit >= 0 && b.queryUsed.Load() >= b.queryLimit
}
