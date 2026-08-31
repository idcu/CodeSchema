// Package lock 提供目录级跨进程排他锁（flock 语义），作为通用原语复用。
//
// 设计定位：机制中立，不绑定任何领域语义（store.json / 数据库 / 缓存均可复用）。
// 仅单消费者（code-schema/internal/store）时按 idcu-go《通用模块标准》档二登记为
// planned，待第二真实消费方出现再升 idcu-go 独立仓。
package lock

import "fmt"

// defaultName 默认锁文件名。沿用历史 store.lock，避免遗留锁文件孤儿化，
// 且与 scanner 的忽略清单（internal/scanner/scanner.go）保持一致。
const defaultName = "store.lock"

// Option 调整 Acquire 行为。
type Option func(*options)

type options struct {
	name string
}

// WithLockName 覆盖锁文件名（默认 store.lock）。
func WithLockName(n string) Option {
	return func(o *options) { o.name = n }
}

// Lock 是目录级跨进程排他锁，防止多进程并发写坏同一目录下的数据文件。
type Lock struct {
	dir  string
	name string
	h    handle
}

// Acquire 获取 dir 的排他锁。默认锁文件名为 store.lock；可用 WithLockName 覆盖。
// Unix 基于 flock(2) 排他锁；Windows 基于独占创建锁文件近似互斥。
func Acquire(dir string, opts ...Option) (*Lock, error) {
	o := options{name: defaultName}
	for _, opt := range opts {
		opt(&o)
	}
	if dir == "" {
		return nil, fmt.Errorf("lock: empty dir")
	}
	h, err := acquire(dir, o.name)
	if err != nil {
		return nil, err
	}
	return &Lock{dir: dir, name: o.name, h: h}, nil
}

// Release 释放锁。重复调用或 nil 接收者均安全。
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	return l.h.release()
}
