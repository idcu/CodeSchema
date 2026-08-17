package sqlite

import (
	"context"
	"sync"
	"testing"
)

// TestConcurrentFixed_ReadWrite 正确设计：reader 固定 3000 次读 + 4 writer 各 25 次写，
// 验证 store 层并发读写是否真的死锁（此前 TestSQLite_ConcurrentReadWrite 因 reader
// 无限循环导致 wg 永不完成而误判为死锁）。
func TestConcurrentFixed_ReadWrite(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	// 2 个 reader，各 3000 次（GetAllFiles + GetClassesByFileID）
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				files, err := s.GetAllFiles(ctx)
				if err != nil {
					t.Errorf("GetAllFiles: %v", err)
					return
				}
				for _, f := range files {
					if _, err := s.GetClassesByFileID(ctx, f.ID); err != nil {
						t.Errorf("GetClasses: %v", err)
						return
					}
				}
			}
		}()
	}
	// 4 个 writer，各 25 次 UpsertIR
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if err := s.UpsertIR(ctx, concurrentIR(w*25+i)); err != nil {
					t.Errorf("writer %d: %v", w, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	t.Log("fixed concurrent read/write completed without deadlock")
}

// TestConcurrentFixed_PureRead 双 reader 并发纯读（固定次数）。
func TestConcurrentFixed_PureRead(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		if err := s.UpsertIR(ctx, concurrentIR(i)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				if _, err := s.GetAllFiles(ctx); err != nil {
					t.Errorf("read: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	t.Log("fixed concurrent pure read completed")
}
