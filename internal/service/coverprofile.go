// Package service 提供覆盖率文件自动采集（T4-4）。
//
// 从真实 `go test -coverprofile=<file>` 产物解析覆盖率块，按行号区间匹配
// store 中的方法记录，自动构建「测试方法 → 覆盖的生产方法」映射并注入
// coverage 策略，使影响面分析包含真实单测清单——无需手写 JSON 注入。
package service

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/idcu/codeschema/internal/store"
)

// LoadGoCoverProfile 从 go test -coverprofile 产物自动采集覆盖率并注入。
//
// 格式（go tool cover 输出，mode: set/count）：
//
//	mode: set
//	github.com/foo/bar/service.go:10.1,12.2 1 1
//	github.com/foo/bar/service.go:15.1,20.2 1 0
//
// 每行 = 文件:起始行.起始列,结束行.结束列 语句数 计数（计数>0 表示被覆盖）。
// 解析后按行号区间匹配 store 方法记录，产出 coverage 映射：
// testMethodFQN → 该测试方法所在测试类覆盖的生产方法 FQN 列表（以测试文件分组）。
func (s *Service) LoadGoCoverProfile(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open coverprofile: %w", err)
	}
	defer f.Close()
	return s.ParseGoCoverProfile(ctx, f)
}

// ParseGoCoverProfile 解析覆盖率 reader 并注入 coverage 映射。
//
// go coverprofile 不直接记录「哪个测试方法覆盖了哪行」，只记录「哪些行被执行过」。
// 因此采用两层映射：
//  1. 被覆盖的生产方法集合（行号区间命中）；
//  2. 对每个测试类（isTestClass），按命名约定找到其源类（matchTestClass），
//     若源类的任一方法被覆盖 → 测试方法 FQN → 被覆盖的生产方法 FQN 列表。
// 语义：这些单测真实运行过并覆盖了这些方法（置信度 90），改动方法时应当重跑。
func (s *Service) ParseGoCoverProfile(ctx context.Context, r io.Reader) error {
	blocks, err := parseCoverProfile(r)
	if err != nil {
		return fmt.Errorf("parse coverprofile: %w", err)
	}
	if len(blocks) == 0 {
		return fmt.Errorf("coverprofile: no blocks found")
	}

	// 按文件路径分组命中块（相对路径优先匹配，绝对路径兜底）
	hitsByFile := make(map[string][]coverBlock)
	for _, b := range blocks {
		if b.Count > 0 {
			hitsByFile[b.File] = append(hitsByFile[b.File], b)
		}
	}

	// 加载全量类与方法（按文件分组）
	classesByFile, methodsByFile, err := s.loadEntitiesByFile(ctx)
	if err != nil {
		return err
	}

	// 1. 被覆盖的生产方法集合：方法行号区间 ∩ 命中块区间
	coveredProd := make(map[string]bool)      // prod method FQN
	coveredByFile := make(map[string][]string) // 文件路径 → 被覆盖方法 FQN（供测试类匹配）
	for file, hits := range hitsByFile {
		methods := methodsByFile[file]
		if len(methods) == 0 {
			// coverprofile 常用模块相对路径（internal/store/order.go），
			// store 登记为绝对路径：按路径后缀匹配
			for key, ms := range methodsByFile {
				if strings.HasSuffix(key, file) || strings.HasSuffix(file, key) {
					methods = append(methods, ms...)
				}
			}
		}
		for _, m := range methods {
			for _, h := range hits {
				if h.StartLine <= m.EndLine && h.EndLine >= m.StartLine {
					coveredProd[m.FullName] = true
					coveredByFile[file] = append(coveredByFile[file], m.FullName)
					break
				}
			}
		}
	}
	if len(coveredProd) == 0 {
		return fmt.Errorf("coverprofile: no production methods covered (all blocks count=0?)")
	}

	// 2. 测试类方法 → 覆盖其源类方法
	report := make(map[string][]string)
	for _, classes := range classesByFile {
		for _, cls := range classes {
			if !isTestClass(cls) {
				continue
			}
			// 该测试类的源类 FQN（命名约定）
			srcFQN := s.matchTestClass(cls)
			if srcFQN == "" {
				continue
			}
			// 源类是否有方法被覆盖（覆盖文件可能不同，用 FQN 匹配）
			srcCovered := false
			for _, covered := range coveredByFile {
				for _, fqn := range covered {
					if extractClassFQN(fqn) == srcFQN {
						srcCovered = true
						break
					}
				}
				if srcCovered {
					break
				}
			}
			if !srcCovered {
				continue
			}
			// 测试方法全部登记，覆盖其源类的被覆盖方法
			methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
			for _, tm := range methods {
				for _, fqn := range coveredByFile {
					for _, covered := range fqn {
						if extractClassFQN(covered) == srcFQN {
							report[tm.FullName] = append(report[tm.FullName], covered)
						}
					}
				}
			}
		}
	}

	// 合并到已有 coverage（不覆盖注入式 JSON）
	if s.coverage == nil {
		s.coverage = make(map[string][]string)
	}
	for k, v := range report {
		seen := make(map[string]bool)
		for _, item := range s.coverage[k] {
			seen[item] = true
		}
		for _, item := range v {
			if !seen[item] {
				s.coverage[k] = append(s.coverage[k], item)
				seen[item] = true
			}
		}
	}
	return nil
}

// coverBlock 覆盖率块。
type coverBlock struct {
	File      string
	StartLine int
	EndLine   int
	Count     int
}

// parseCoverProfile 解析 go coverprofile 文本。
func parseCoverProfile(r io.Reader) ([]coverBlock, error) {
	var blocks []coverBlock
	sc := bufio.NewScanner(r)
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// 首行 mode: xxx，跳过
		if first {
			first = false
			if strings.HasPrefix(line, "mode:") {
				continue
			}
		}
		// 格式：file.go:startLine.startCol,endLine.endCol numStmt count
		colonIdx := strings.LastIndex(line, ":")
		if colonIdx < 0 {
			continue
		}
		file := line[:colonIdx]
		rest := line[colonIdx+1:]
		fields := strings.Fields(rest)
		if len(fields) < 3 {
			continue
		}
		pos := fields[0] // startLine.startCol,endLine.endCol
		numStmt, err1 := strconv.Atoi(fields[1])
		count, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil {
			continue
		}
		_ = numStmt
		startPart := strings.Split(pos, ",")[0]
		startLineStr := strings.Split(startPart, ".")[0]
		startLine, err := strconv.Atoi(startLineStr)
		if err != nil {
			continue
		}
		// 结束行：endLine.endCol 部分
		endPart := ""
		if idx := strings.Index(pos, ","); idx >= 0 {
			endPart = pos[idx+1:]
		}
		endLineStr := strings.Split(endPart, ".")[0]
		endLine := startLine
		if endLineStr != "" {
			if v, err := strconv.Atoi(endLineStr); err == nil {
				endLine = v
			}
		}
		blocks = append(blocks, coverBlock{
			File:      file,
			StartLine: startLine,
			EndLine:   endLine,
			Count:     count,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return blocks, nil
}

// loadEntitiesByFile 加载所有文件的类与方法记录，按文件路径分组
// （同时登记绝对路径与模块相对路径，coverprofile 通常用相对路径）。
func (s *Service) loadEntitiesByFile(ctx context.Context) (map[string][]store.ClassRecord, map[string][]store.MethodRecord, error) {
	classesByFile := make(map[string][]store.ClassRecord)
	methodsByFile := make(map[string][]store.MethodRecord)
	files, err := s.store.GetAllFiles(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("get all files: %w", err)
	}
	for _, f := range files {
		classes, err := s.store.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			continue
		}
		for _, cls := range classes {
			methods, err := s.store.GetMethodsByClassID(ctx, cls.ID)
			if err != nil {
				continue
			}
			// 绝对路径 + 相对路径都登记
			classesByFile[f.AbsolutePath] = append(classesByFile[f.AbsolutePath], cls)
			methodsByFile[f.AbsolutePath] = append(methodsByFile[f.AbsolutePath], methods...)
			if rel := relativeStorePath(f.AbsolutePath); rel != "" {
				classesByFile[rel] = append(classesByFile[rel], cls)
				methodsByFile[rel] = append(methodsByFile[rel], methods...)
			}
		}
	}
	return classesByFile, methodsByFile, nil
}

// relativeStorePath 尽力提取文件的模块相对路径（取仓库根后部分）。
// coverprofile 中文件路径通常为「模块根/相对路径」，这里做 best-effort 匹配。
func relativeStorePath(abs string) string {
	// 简单启发：从已知包目录向后截取（如 .../internal/store/x.go → internal/store/x.go）
	idx := strings.Index(abs, "/internal/")
	if idx >= 0 {
		return abs[idx+1:]
	}
	idx = strings.Index(abs, "/pkg/")
	if idx >= 0 {
		return abs[idx+1:]
	}
	return ""
}
