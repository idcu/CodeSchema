//go:build redis

package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/idcu/codeschema/internal/config"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/store/redis"
)

// rebuildKVCmd 从基础存储全量重建 Redis L2 缓存（KV 反查层）。
//
// 适用场景：Redis 数据丢失/损坏（scan 写路径会 best-effort 重建，但已存在文件
// 不触发写入）；需从权威存储一次性灌回类/调用/文件→类索引。
// 用法：codeschema rebuild-kv [--store=<dir>]（需 config.storage.kv 配置 redis://）
func rebuildKVCmd(ctx context.Context, cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("rebuild-kv", flag.ExitOnError)
	storeDir := fs.String("store", cfg.Storage.DSN, "基础存储目录（默认 storage.dsn）")
	fs.Parse(args)

	if cfg.Storage.KV == "" {
		return fmt.Errorf("rebuild-kv requires storage.kv (redis://...) in config")
	}
	st, err := newStore(ctx, cfg, *storeDir)
	if err != nil {
		return fmt.Errorf("open base store: %w", err)
	}
	defer st.Close()

	cache, err := redis.NewRedisCacheFromURL(cfg.Storage.KV)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	defer cache.Close()

	if err := cache.HealthCheck(ctx); err != nil {
		return fmt.Errorf("redis health check: %w", err)
	}
	if err := cache.Flush(ctx); err != nil {
		return fmt.Errorf("flush redis: %w", err)
	}

	files, err := st.GetAllFiles(ctx)
	if err != nil {
		return fmt.Errorf("list files: %w", err)
	}
	classes, calls := 0, 0
	for _, f := range files {
		recs, err := st.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			return fmt.Errorf("classes of file %s: %w", f.AbsolutePath, err)
		}
		fqns := make([]string, 0, len(recs))
		for i := range recs {
			cls := classRecordToIR(&recs[i])
			fqns = append(fqns, cls.FullName)
			if err := cache.PutClass(ctx, cls); err != nil {
				log.Printf("[warn] redis 写入类 %s 失败：%v", cls.FullName, err)
				continue
			}
			classes++
		}
		if err := cache.PutFileClasses(ctx, f.AbsolutePath, fqns); err != nil {
			log.Printf("[warn] redis 写入文件→类索引 %s 失败：%v", f.AbsolutePath, err)
		}
		callRecs, err := st.GetCallsByFileID(ctx, f.ID)
		if err != nil {
			return fmt.Errorf("calls of file %s: %w", f.AbsolutePath, err)
		}
		for i := range callRecs {
			call := callRecordToIR(&callRecs[i])
			if err := cache.PutCall(ctx, call); err != nil {
				log.Printf("[warn] redis 写入调用 %s→%s 失败：%v", call.CallerFQN, call.CalleeFQN, err)
				continue
			}
			calls++
		}
	}
	log.Printf("rebuild-kv: 重建完成 files=%d classes=%d calls=%d", len(files), classes, calls)
	return nil
}

// classRecordToIR 将存储层的 ClassRecord 转为 Redis 缓存所需的 parser.ClassIR。
func classRecordToIR(r *store.ClassRecord) *parser.ClassIR {
	return &parser.ClassIR{
		Name:        r.Name,
		FullName:    r.FullName,
		Type:        r.Type,
		ParentFQNs:  r.ParentFQNs,
		StartLine:   r.StartLine,
		StartCol:    r.StartCol,
		EndLine:     r.EndLine,
		EndCol:      r.EndCol,
		Modifier:    r.Modifier,
		Doc:         r.Doc,
	}
}

// callRecordToIR 将存储层的 CallRecord 转为 Redis 缓存所需的 parser.CallIR。
func callRecordToIR(r *store.CallRecord) *parser.CallIR {
	return &parser.CallIR{
		CallerFQN:  r.CallerFQN,
		CalleeFQN:  r.CalleeFQN,
		CallType:   r.CallType,
		LineNumber: r.LineNumber,
	}
}
