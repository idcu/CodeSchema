package scip

import (
	"path/filepath"
	"strings"
)

// SCIP (.scip) 是 protobuf 二进制格式。为避免引入外部 protobuf 依赖
// （github.com/scip-code/scip 的 go.mod 含嵌套子模块，解析需直连 github，
// 本环境不通；且 scip-typescript 默认产出 protobuf 而非 JSON），
// 这里用极简的 protobuf wire 解码器解析 Index。
//
// 实测 schema（scip-typescript 0.4.0，2026-09-02 真实产物 wire 校验）：
//   Index      { Metadata metadata=1; repeated Document documents=2; }
//   Document   { string relative_path=1; repeated Occurrence occurrences=2;
//                repeated SymbolInformation symbols=3; Language language=4?(嵌套，可缺省) }
//   SymbolInfo { string symbol=1; ... }
//     —— 0.4.0 产物仅填 symbol（field 1）；kind/enclosing_symbol 不填；
//        documentation 文本（markdown）实测在 field 3，本解码器忽略。
//   Occurrence { repeated int32 range=1 [packed, 实测 3 元素 [line,startChar,endChar]];
//                string symbol=2; SymbolRole symbol_roles=3;
//                repeated int32 enclosing_range=7 [packed, [sLine,sCol,eLine,eCol]] }
// SymbolRole 位掩码（SCIP 协议）：Definition=1、Import=2、WriteAccess=4、
// ReadAccess=8、Generated=16、ForwardDefinition=64；普通标识符引用 = 0。
// Document.language 较新 SCIP 为嵌套 Language message（非字符串），
// 本解码器对其做 best-effort 提取，缺失时由 docLang 按文件扩展名兜底。

const (
	// scipRoleDefinition 为 SCIP SymbolRole.Definition 位（1）。
	// 注意：普通「引用」occurrence 的 symbol_roles 为 0（而非 1<<1）；
	// 1<<1 是 Import 角色，不代表引用。判定「定义以外皆引用」即可。
	scipRoleDefinition = 1
)

// readVarint 读取 protobuf varint；遇残缺（缓冲区提前结束）前进 1 字节避免越界 panic。
func readVarint(b []byte, pos int) (uint64, int) {
	var x uint64
	var s uint
	for i := 0; pos+i < len(b); i++ {
		c := b[pos+i]
		x |= uint64(c&0x7f) << s
		s += 7
		if c&0x80 == 0 {
			return x, pos + i + 1
		}
	}
	return x, pos + 1
}

// skipField 跳过指定 wire 类型的字段值；对无效/越界输入保证前进，
// 避免损坏输入导致死循环。
func skipField(b []byte, pos, wt int) int {
	switch wt {
	case 0:
		_, n := readVarint(b, pos)
		return n
	case 1:
		if pos+8 > len(b) {
			return len(b)
		}
		return pos + 8
	case 5:
		if pos+4 > len(b) {
			return len(b)
		}
		return pos + 4
	case 2:
		l, n := readVarint(b, pos)
		if n+int(l) > len(b) {
			return len(b)
		}
		return n + int(l)
	}
	// 未知 wire type（损坏输入）：保守前进，避免死循环。
	if pos+1 < len(b) {
		return pos + 1
	}
	return len(b)
}

// decodeIndex 解析顶层 Index，返回所有 Document。
// 顶层仅按 field 2（documents）切分；其余字段（metadata 等）跳过。
func decodeIndex(data []byte) []*SCIPDocument {
	var docs []*SCIPDocument
	pos := 0
	for pos < len(data) {
		tag, n := readVarint(data, pos)
		pos = n
		field := int(tag >> 3)
		wt := int(tag & 7)
		if field == 2 && wt == 2 {
			l, n2 := readVarint(data, pos)
			pos = n2
			end := pos + int(l)
			if end > len(data) {
				end = len(data)
			}
			docs = append(docs, decodeDocument(data[pos:end]))
			pos = end
		} else {
			pos = skipField(data, pos, wt)
		}
	}
	return docs
}

// decodeDocument 解析单个 Document 消息。
// 实测字段序：relative_path=1、occurrences=2、symbols=3（language 缺省）。
// 兼容早期产物（language=2 字符串、symbols=3、occurrences=4）时，
// field 2 内容为可打印语言名（如 "TypeScript"）→ 按 language 处理；
// 否则按新版 occurrences 处理（occurrence 消息含控制字节，必非可打印文本）。
func decodeDocument(b []byte) *SCIPDocument {
	d := &SCIPDocument{}
	pos := 0
	for pos < len(b) {
		tag, n := readVarint(b, pos)
		pos = n
		field := int(tag >> 3)
		wt := int(tag & 7)
		switch {
		case field == 1 && wt == 2:
			l, n2 := readVarint(b, pos)
			pos = n2
			end := pos + int(l)
			d.RelativePath = string(b[pos:end])
			pos = end
		case field == 2 && wt == 2:
			l, n2 := readVarint(b, pos)
			pos = n2
			end := pos + int(l)
			seg := b[pos:end]
			pos = end
			// 新版：occurrences（二进制）；旧版：language 字符串（可打印短文本）
			if isLanguageText(seg) {
				d.Language = string(seg)
			} else {
				d.Occurrences = append(d.Occurrences, decodeOccurrence(seg))
			}
		case field == 3 && wt == 2:
			l, n2 := readVarint(b, pos)
			pos = n2
			end := pos + int(l)
			d.Symbols = append(d.Symbols, decodeSymbol(b[pos:end]))
			pos = end
		case field == 4 && wt == 2:
			// 新版：language（嵌套 Language message 或字符串）；旧版：occurrences
			l, n2 := readVarint(b, pos)
			pos = n2
			end := pos + int(l)
			seg := b[pos:end]
			pos = end
			if lang := extractLanguage(seg); lang != "" {
				d.Language = lang
			} else {
				d.Occurrences = append(d.Occurrences, decodeOccurrence(seg))
			}
		default:
			pos = skipField(b, pos, wt)
		}
	}
	return d
}

// isLanguageText 判断字段值是否为语言名字符串（可打印短文本）。
// occurrence 消息（field 2 新版）含控制字节（tag/len/packed），必非可打印文本。
func isLanguageText(b []byte) bool {
	if len(b) == 0 || len(b) > 64 {
		return false
	}
	return isPrintable(b)
}

// decodeSymbol 解析 SymbolInformation 消息。
// 实测（0.4.0）：仅 symbol（field 1）有值；kind/enclosing_symbol 不填；
// field 3 为 documentation 文本（markdown），本解码器忽略。
// 兼容早期产物的 kind（enum varint，field 2）。
func decodeSymbol(b []byte) *SCIPSymbol {
	s := &SCIPSymbol{}
	pos := 0
	for pos < len(b) {
		tag, n := readVarint(b, pos)
		pos = n
		field := int(tag >> 3)
		wt := int(tag & 7)
		switch {
		case field == 1 && wt == 2:
			l, n2 := readVarint(b, pos)
			pos = n2
			end := pos + int(l)
			s.ID = string(b[pos:end])
			pos = end
		case field == 2 && wt == 0:
			// 兼容早期 enum kind（0.4.0 不填）
			v, nn := readVarint(b, pos)
			pos = nn
			s.Kind = int(v)
		default:
			pos = skipField(b, pos, wt)
		}
	}
	return s
}

// decodeOccurrence 解析 Occurrence 消息。
// 实测字段序：range=1（packed int32，[line,startChar,endChar]）、
// symbol=2、symbol_roles=3（varint）、enclosing_range=7（packed，方法体区间）。
func decodeOccurrence(b []byte) *SCIPOccurrence {
	o := &SCIPOccurrence{}
	pos := 0
	for pos < len(b) {
		tag, n := readVarint(b, pos)
		pos = n
		field := int(tag >> 3)
		wt := int(tag & 7)
		switch {
		case field == 1 && wt == 2:
			// range：packed repeated int32（实测 3 元素 [line,startChar,endChar]）
			l, n2 := readVarint(b, pos)
			pos = n2
			end := pos + int(l)
			packed := b[pos:end]
			pos = end
			o.Range = append(o.Range, unpackInt32(packed)...)
		case field == 2 && wt == 2:
			l, n2 := readVarint(b, pos)
			pos = n2
			end := pos + int(l)
			o.Symbol = string(b[pos:end])
			pos = end
		case field == 3 && wt == 0:
			v, nn := readVarint(b, pos)
			pos = nn
			o.SymbolRole = int(v)
		case field == 7 && wt == 2:
			// enclosing_range：packed repeated int32（方法体 [sLine,sCol,eLine,eCol]）
			l, n2 := readVarint(b, pos)
			pos = n2
			end := pos + int(l)
			packed := b[pos:end]
			pos = end
			o.EnclosingRange = append(o.EnclosingRange, unpackInt32(packed)...)
		default:
			pos = skipField(b, pos, wt)
		}
	}
	return o
}

// unpackInt32 解包 packed repeated int32（varint 序列）。
func unpackInt32(packed []byte) []int {
	var out []int
	pp := 0
	for pp < len(packed) {
		v, nn2 := readVarint(packed, pp)
		pp = nn2
		out = append(out, int(v))
	}
	return out
}

// isPrintable 判断字节切片是否为可打印 ASCII（允许常见空白）。
func isPrintable(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < 0x20 && c != '\n' && c != '\t' && c != '\r' {
			return false
		}
	}
	return true
}

// extractLanguage best-effort 提取 Document.language：
// 旧版 SCIP 该字段为字符串；较新版为嵌套 Language message（field 1 为语言字符串）。
func extractLanguage(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if isPrintable(b) {
		return string(b)
	}
	pos := 0
	for pos < len(b) {
		tag, n := readVarint(b, pos)
		pos = n
		field := int(tag >> 3)
		wt := int(tag & 7)
		if field == 1 && wt == 2 {
			l, n2 := readVarint(b, pos)
			pos = n2
			end := pos + int(l)
			inner := b[pos:end]
			if isPrintable(inner) {
				return string(inner)
			}
			pos = end
		} else {
			pos = skipField(b, pos, wt)
		}
	}
	return ""
}

// docLang 返回文档语言：优先用 index 中解析出的 language，缺失时按文件扩展名兜底。
func docLang(doc *SCIPDocument) string {
	if doc.Language != "" {
		return doc.Language
	}
	ext := strings.ToLower(filepath.Ext(doc.RelativePath))
	switch ext {
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	case ".go":
		return "go"
	case ".java":
		return "java"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".cpp", ".cc", ".cxx", ".c":
		return "cpp"
	default:
		return ""
	}
}

// classifySymbol 从 SCIP 符号字符串解析出类/方法身份与展示名。
// 格式：<scheme> <pkgmgr> <package> <path>#<descriptor>。
// 描述符约定：空=类/模块；含 "()"=方法；含 "().("=参数。
func classifySymbol(raw string) (isClass, isMethod bool, name string) {
	s := strings.ReplaceAll(raw, "`", "")
	hashIdx := strings.LastIndex(s, "#")
	if hashIdx < 0 {
		return false, false, ""
	}
	descriptor := s[hashIdx+1:]
	pathPart := s[:hashIdx]
	lastSeg := pathPart
	if i := strings.LastIndex(pathPart, "/"); i >= 0 {
		lastSeg = pathPart[i+1:]
	}
	if descriptor == "" {
		ext := strings.ToLower(filepath.Ext(lastSeg))
		switch ext {
		case ".ts", ".tsx", ".js", ".jsx", ".go", ".java", ".py", ".rs", ".cpp", ".cc", ".c", ".cxx":
			return false, false, "" // 模块/文档符号，跳过
		}
		return true, false, lastSeg // 类/类型
	}
	if strings.Contains(descriptor, "().(") {
		return false, false, "" // 参数，跳过
	}
	if i := strings.Index(descriptor, "()"); i >= 0 {
		return false, true, descriptor[:i] // 方法
	}
	return false, false, "" // 字段/其他
}
