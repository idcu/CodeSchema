package ai

// Budget 管控 AI 增强层的调用次数硬限。
//
// 按作用域分为两类预算：
//   - 扫描期预算（perScan）：每次全量扫描的 AI 调用次数上限。
//   - 查询期预算（perQuery）：每次查询的 AI 调用次数上限。
//
// 计数器在每次扫描 / 查询开始时由调用方 Reset，超限后对应作用域的增强被跳过，
// 不影响主流程（索引始终可用）。limit 为负数表示不限制。
type Budget struct {
	scanLimit  int
	queryLimit int
	scanUsed   int
	queryUsed  int
}

// NewBudget 创建预算管控器。
// perScan / perQuery 为负表示对应作用域不限制调用次数。
func NewBudget(perScan, perQuery int) *Budget {
	return &Budget{scanLimit: perScan, queryLimit: perQuery}
}

// tryConsumeScan 尝试消费一次扫描期预算；成功返回 true。
func (b *Budget) tryConsumeScan() bool {
	if b.scanLimit < 0 {
		return true
	}
	if b.scanUsed >= b.scanLimit {
		return false
	}
	b.scanUsed++
	return true
}

// tryConsumeQuery 尝试消费一次查询期预算；成功返回 true。
func (b *Budget) tryConsumeQuery() bool {
	if b.queryLimit < 0 {
		return true
	}
	if b.queryUsed >= b.queryLimit {
		return false
	}
	b.queryUsed++
	return true
}

// ResetScan 重置扫描期计数（每次扫描开始时调用）。
func (b *Budget) ResetScan() {
	b.scanUsed = 0
}

// ResetQuery 重置查询期计数（每次查询开始时调用）。
func (b *Budget) ResetQuery() {
	b.queryUsed = 0
}

// ScanRemaining 返回扫描期剩余可用次数（不限时返回 -1）。
func (b *Budget) ScanRemaining() int {
	if b.scanLimit < 0 {
		return -1
	}
	return b.scanLimit - b.scanUsed
}

// QueryRemaining 返回查询期剩余可用次数（不限时返回 -1）。
func (b *Budget) QueryRemaining() int {
	if b.queryLimit < 0 {
		return -1
	}
	return b.queryLimit - b.queryUsed
}

// ScanExhausted 报告扫描期预算是否已耗尽。
func (b *Budget) ScanExhausted() bool {
	return b.scanLimit >= 0 && b.scanUsed >= b.scanLimit
}

// QueryExhausted 报告查询期预算是否已耗尽。
func (b *Budget) QueryExhausted() bool {
	return b.queryLimit >= 0 && b.queryUsed >= b.queryLimit
}
