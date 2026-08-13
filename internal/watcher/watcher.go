// Package watcher 提供文件系统变更监听能力。
//
// P0 阶段实现基于轮询的简化监听（PollWatcher），
// 不依赖 fsnotify 外部包，适合纯 Go 环境运行。
// P1 阶段可切换为 fsnotify 原生监听以获得更好的性能。
package watcher

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"codeschema/internal/scheduler"
	"codeschema/internal/scanner"
)

// Watcher 是文件监听器的统一接口。
type Watcher interface {
	// Start 启动监听，阻塞直到 context 取消或发生不可恢复错误。
	Start(ctx context.Context) error
	// Stop 停止监听。
	Stop() error
}

// PollWatcher 基于轮询的简化文件监听器。
// 每秒扫描目录，检测文件变更（mtime + size）并推入调度器。
type PollWatcher struct {
	root       string
	scanner    *scanner.Scanner
	scheduler  *scheduler.Scheduler
	ignoreDirs map[string]bool
	interval   time.Duration

	// 缓存文件状态，用于检测变更
	fileStates map[string]fileState
}

type fileState struct {
	modTime time.Time
	size    int64
}

// NewPollWatcher 创建轮询监听器。
// root: 监听根目录。
// scan: Scanner 实例，用于处理变更文件。
// sched: Scheduler 实例，用于防抖排队。
// interval: 轮询间隔（默认 1s）。
// ignoreDirs: 忽略的目录名列表。
func NewPollWatcher(root string, scan *scanner.Scanner, sched *scheduler.Scheduler, interval time.Duration, ignoreDirs []string) *PollWatcher {
	if interval <= 0 {
		interval = 1 * time.Second
	}
	ign := make(map[string]bool)
	for _, d := range ignoreDirs {
		ign[d] = true
	}
	// 默认忽略目录
	defaultIgnores := []string{".git", "node_modules", "target", "build", "vendor", ".idea", ".vscode", "__pycache__"}
	for _, d := range defaultIgnores {
		ign[d] = true
	}

	return &PollWatcher{
		root:       root,
		scanner:    scan,
		scheduler:  sched,
		ignoreDirs: ign,
		interval:   interval,
		fileStates: make(map[string]fileState),
	}
}

// Start 启动轮询监听，阻塞直到 context 取消。
func (pw *PollWatcher) Start(ctx context.Context) error {
	ticker := time.NewTicker(pw.interval)
	defer ticker.Stop()

	// 先初始化文件状态缓存
	pw.buildInitialState()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			pw.poll(ctx)
		}
	}
}

// Stop 停止监听。
func (pw *PollWatcher) Stop() error {
	return nil
}

// buildInitialState 构建初始文件状态缓存。
func (pw *PollWatcher) buildInitialState() {
	filepath.Walk(pw.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && pw.ignoreDirs[info.Name()] {
			return filepath.SkipDir
		}
		if !info.IsDir() {
			pw.fileStates[path] = fileState{
				modTime: info.ModTime(),
				size:    info.Size(),
			}
		}
		return nil
	})
}

// poll 执行一次轮询检测。
func (pw *PollWatcher) poll(ctx context.Context) {
	current := make(map[string]fileState)

	filepath.Walk(pw.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && pw.ignoreDirs[info.Name()] {
			return filepath.SkipDir
		}
		if !info.IsDir() {
			state := fileState{modTime: info.ModTime(), size: info.Size()}
			current[path] = state

			oldState, exists := pw.fileStates[path]
			if !exists || oldState.modTime != state.modTime || oldState.size != state.size {
				pw.scheduler.Enqueue(path)
			}
		}
		return nil
	})

	// 检测已删除的文件
	for path := range pw.fileStates {
		if _, exists := current[path]; !exists {
			// 文件已删除，通知扫描器
			pw.scheduler.Enqueue(path)
		}
	}

	pw.fileStates = current
}