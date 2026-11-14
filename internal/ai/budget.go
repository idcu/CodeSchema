package ai

import "sync/atomic"

// Budget 管控 AI 增强层的调用次数硬限。
//
// 按作用域分为两类预算：
//   - 扫描期预算（perScan）：每次全量扫描的 AI 调用次数上限，经 ResetScan 归零。
//   - 查询期预算（perQuery）：由每次查询新建的 QueryBudget 独立持有（R6-2），
//     进程级 Budget 只保存限额模板，不持有跨请求的查询计数。
//
// 超限后对应作用域的增强被跳过，不影响主流程（索引始终可用）。
// limit 为负数表示不限制。
//
// 计数使用原子读写，保证并发调用下无数据竞争与超发。
type Budget struct {
	scanLimit  int64
	queryLimit int64
	scanUsed   atomic.Int64
}

// QueryBudget 单次查询独立的预算作用域（每次查询新建，计数互不影响）。
type QueryBudget struct {
	limit int64
	used  atomic.Int64
}

// NewBudget 创建预算管控器。
// perScan / perQuery 为负表示对应作用域不限制调用次数。
func NewBudget(perScan, perQuery int) *Budget {
	return &Budget{scanLimit: int64(perScan), queryLimit: int64(perQuery)}
}

// NewQueryBudget 为一次查询创建独立预算作用域（限额继承 Budget.queryLimit）。
func (b *Budget) NewQueryBudget() *QueryBudget {
	return &QueryBudget{limit: b.queryLimit}
}

// tryConsume 尝试消费一次查询预算；成功返回 true。并发安全（原子 CAS）。
func (q *QueryBudget) tryConsume() bool {
	if q.limit < 0 {
		return true
	}
	for {
		cur := q.used.Load()
		if cur >= q.limit {
			return false
		}
		if q.used.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

// Remaining 返回本次查询剩余可用次数（不限时返回 -1）。
func (q *QueryBudget) Remaining() int {
	if q.limit < 0 {
		return -1
	}
	return int(q.limit - q.used.Load())
}

// Exhausted 报告本次查询预算是否已耗尽。
func (q *QueryBudget) Exhausted() bool {
	return q.limit >= 0 && q.used.Load() >= q.limit
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

// ResetScan 重置扫描期计数（每次扫描开始时调用）。
func (b *Budget) ResetScan() {
	b.scanUsed.Store(0)
}

// ScanRemaining 返回扫描期剩余可用次数（不限时返回 -1）。
func (b *Budget) ScanRemaining() int {
	if b.scanLimit < 0 {
		return -1
	}
	return int(b.scanLimit - b.scanUsed.Load())
}

// ScanExhausted 报告扫描期预算是否已耗尽。
func (b *Budget) ScanExhausted() bool {
	return b.scanLimit >= 0 && b.scanUsed.Load() >= b.scanLimit
}
