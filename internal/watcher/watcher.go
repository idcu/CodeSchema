// Package watcher 是领域层对中性文件监听能力 fswatch 的薄封装。
//
// 历史签名 (root, scan, sched, ...) 保持不变，仅把变更路径通过 sched.Enqueue
// 接入领域调度器；scan 仅保留以兼容既有调用点，实际不参与监听逻辑。
// 真正的实现与解耦见 internal/fswatch。
package watcher

import (
	"time"

	"github.com/idcu/codeschema/internal/fswatch"
	"github.com/idcu/codeschema/internal/scanner"
	"github.com/idcu/codeschema/internal/scheduler"
)

// Watcher 是文件监听器的统一接口（别名 fswatch.Watcher）。
type Watcher = fswatch.Watcher

// PollWatcher 基于轮询的文件监听器（别名 fswatch.PollWatcher）。
type PollWatcher = fswatch.PollWatcher

// FsWatcher 基于 fsnotify 的原生监听器（别名 fswatch.FsWatcher）。
type FsWatcher = fswatch.FsWatcher

// NewPollWatcher 构造轮询监听器，并把变更路径上报给领域调度器 sched。
func NewPollWatcher(root string, scan *scanner.Scanner, sched *scheduler.Scheduler[string], interval time.Duration, ignoreDirs []string) *PollWatcher {
	return fswatch.NewPollWatcher(root, func(path string) {
		sched.Enqueue(path)
	}, interval, ignoreDirs)
}

// NewFsWatcher 构造 fsnotify 原生监听器，并把变更路径上报给领域调度器 sched。
func NewFsWatcher(root string, scan *scanner.Scanner, sched *scheduler.Scheduler[string], ignoreDirs []string) (*FsWatcher, error) {
	return fswatch.NewFsWatcher(root, func(path string) {
		sched.Enqueue(path)
	}, ignoreDirs)
}
