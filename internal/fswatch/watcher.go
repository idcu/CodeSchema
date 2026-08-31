// Package fswatch 提供中性的文件系统变更监听能力。
//
// 与具体业务（扫描/索引/调度）解耦：监听器只负责"发现文件变更"并通过
// onChange 回调上报路径，由调用方决定后续动作（入队、重索引等）。
// 这样本包可作为通用原语被任意项目复用（档二本地公共化）。
//
// 提供两种实现：
//   - PollWatcher：基于轮询（mtime+size），无外部依赖，适合纯 Go 环境；
//   - FsWatcher：基于 fsnotify 的原生事件监听，延迟与 CPU 开销更低，适合生产。
package fswatch

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"gitee.com/idcu-go/recovery"
)

// Watcher 是文件监听器的统一接口。
type Watcher interface {
	// Start 启动监听，阻塞直到 context 取消或发生不可恢复错误。
	Start(ctx context.Context) error
	// Stop 停止监听。
	Stop() error
}

// DefaultIgnoreDirs 默认忽略的目录名（与主流工具链约定一致）。
var DefaultIgnoreDirs = []string{
	".git", "node_modules", "target", "build", "vendor",
	".idea", ".vscode", "__pycache__",
}

// fileState 缓存单个文件的变更特征，用于轮询检测。
type fileState struct {
	modTime time.Time
	size    int64
}

func buildIgnoreSet(extra []string) map[string]bool {
	ign := make(map[string]bool, len(DefaultIgnoreDirs)+len(extra))
	for _, d := range DefaultIgnoreDirs {
		ign[d] = true
	}
	for _, d := range extra {
		ign[d] = true
	}
	return ign
}

// ---------------------------------------------------------------------------
// PollWatcher — 基于轮询的简化文件监听器
// ---------------------------------------------------------------------------

// PollWatcher 基于轮询的简化文件监听器。
// 按 interval 周期扫描目录，检测文件变更（mtime + size）并通过 onChange 上报。
type PollWatcher struct {
	root       string
	onChange   func(path string)
	ignoreDirs map[string]bool
	interval   time.Duration

	// 缓存文件状态，用于检测变更
	fileStates map[string]fileState
}

// NewPollWatcher 创建轮询监听器。
//   - root: 监听根目录；
//   - onChange: 检测到文件新增/修改/删除时回调（path 为绝对/相对路径原样上报）；
//   - interval: 轮询间隔（<=0 时默认 1s）；
//   - ignoreDirs: 额外忽略的目录名（默认忽略集见 DefaultIgnoreDirs）。
func NewPollWatcher(root string, onChange func(string), interval time.Duration, ignoreDirs []string) *PollWatcher {
	if interval <= 0 {
		interval = 1 * time.Second
	}
	return &PollWatcher{
		root:       root,
		onChange:   onChange,
		ignoreDirs: buildIgnoreSet(ignoreDirs),
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
			recovery.SafeCall(func() {
				pw.poll(ctx)
			})
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
			pw.fileStates[path] = fileState{modTime: info.ModTime(), size: info.Size()}
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
				pw.onChange(path)
			}
		}
		return nil
	})

	// 检测已删除的文件
	for path := range pw.fileStates {
		if _, exists := current[path]; !exists {
			pw.onChange(path)
		}
	}

	pw.fileStates = current
}

// ---------------------------------------------------------------------------
// FsWatcher — 基于 fsnotify 的原生文件系统监听器
// ---------------------------------------------------------------------------

// FsWatcher 使用 fsnotify 实现原生文件系统事件监听。
// 自动递归监听所有子目录，当新目录创建时自动加入监听。
// 适合生产环境，比 PollWatcher 有更低的延迟和 CPU 开销。
type FsWatcher struct {
	root       string
	onChange   func(path string)
	ignoreDirs map[string]bool
	watcher    *fsnotify.Watcher
	done       chan struct{}
}

// NewFsWatcher 创建 fsnotify 原生监听器。
//   - root: 监听根目录；
//   - onChange: 检测到文件变更时回调；
//   - ignoreDirs: 额外忽略的目录名。
func NewFsWatcher(root string, onChange func(string), ignoreDirs []string) (*FsWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &FsWatcher{
		root:       root,
		onChange:   onChange,
		ignoreDirs: buildIgnoreSet(ignoreDirs),
		watcher:    w,
		done:       make(chan struct{}),
	}, nil
}

// Start 启动 fsnotify 原生监听，阻塞直到 context 取消。
func (fw *FsWatcher) Start(ctx context.Context) error {
	// 递归添加所有子目录到监听列表
	if err := fw.addRecursive(fw.root); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-fw.done:
			return nil
		case event, ok := <-fw.watcher.Events:
			if !ok {
				return nil
			}
			recovery.SafeCall(func() {
				fw.handleEvent(ctx, event)
			})
		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return nil
			}
			// 非致命错误，继续监听
			_ = err
		}
	}
}

// Stop 停止 fsnotify 监听。
func (fw *FsWatcher) Stop() error {
	select {
	case <-fw.done:
		// 已经关闭
	default:
		close(fw.done)
	}
	return fw.watcher.Close()
}

// addRecursive 递归添加目录及其子目录到 fsnotify 监听。
func (fw *FsWatcher) addRecursive(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 跳过无法访问的路径
		}
		if info.IsDir() {
			// 检查是否应该忽略此目录
			if fw.ignoreDirs[info.Name()] {
				return filepath.SkipDir
			}
			// 添加目录到 fsnotify 监听
			if err := fw.watcher.Add(path); err != nil {
				// 非致命错误，跳过此目录
				return nil
			}
		}
		return nil
	})
}

// handleEvent 处理 fsnotify 事件。
func (fw *FsWatcher) handleEvent(ctx context.Context, event fsnotify.Event) {
	// 检查文件是否在忽略目录下
	if fw.IsIgnored(event.Name) {
		return
	}

	// 如果是新创建的目录，递归添加到监听
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			if !fw.ignoreDirs[info.Name()] {
				// 非阻塞添加子目录监听
				fw.addRecursive(event.Name)
			}
		}
	}

	// 将所有变更事件上报
	fw.onChange(event.Name)
}

// IsIgnored 检查路径是否位于忽略目录下（含默认忽略集与构造时传入的额外目录）。
func (fw *FsWatcher) IsIgnored(path string) bool {
	dir := filepath.Dir(path)
	for {
		if fw.ignoreDirs[filepath.Base(dir)] {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir || parent == "." || parent == string(filepath.Separator) {
			break
		}
		dir = parent
	}
	return false
}
