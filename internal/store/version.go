package store

import "sync/atomic"

// ChangeClock 是存储变更版本的单调递增计数器，供各 Store 后端嵌入。
//
// 派生缓存（调用图、查询缓存、测试关联快照、检索文档）的正确性以该版本为准：
// 任何一次写入都必须先推进版本，缓存条目版本不符即重建。显式失效（如
// Service.InvalidateDerivedCaches）只是提前重建的优化，不再是正确性前提。
//
// 版本在写方法入口推进而非成功后推进：失败路径中已经落库的部分同样要作废
// 派生缓存，宁可多重建一次，也不能让缓存永久陈旧。
type ChangeClock struct {
	v atomic.Uint64
}

// Touch 推进一次变更版本，返回推进后的版本号。
func (c *ChangeClock) Touch() uint64 { return c.v.Add(1) }

// ChangeVersion 返回当前变更版本（零值表示尚未发生写入）。
func (c *ChangeClock) ChangeVersion() uint64 { return c.v.Load() }
