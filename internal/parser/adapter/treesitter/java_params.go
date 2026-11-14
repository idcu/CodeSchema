package treesitter

import (
	"strings"

	"github.com/idcu/codeschema/internal/parser"
)

// parseJavaParams 从 Java 方法签名行解析参数列表（名称/类型/序号）。
//
// 正则路径只有单行签名；AST 路径优先按参数节点解析，文本回退时复用本函数。
func parseJavaParams(signature string) []parser.ParamIR {
	open := strings.Index(signature, "(")
	if open < 0 {
		return nil
	}
	end := strings.LastIndex(signature, ")")
	if end <= open {
		end = len(signature)
	}
	body := strings.TrimSpace(signature[open+1 : end])
	if body == "" {
		return nil
	}
	var params []parser.ParamIR
	for _, raw := range splitJavaArgs(body) {
		p := strings.TrimSpace(raw)
		// 去参数注解：@NotNull String name / @RequestParam("id") Integer id
		for strings.HasPrefix(p, "@") {
			sp := strings.IndexAny(p, " \t")
			if sp < 0 {
				p = ""
				break
			}
			p = strings.TrimSpace(p[sp+1:])
		}
		p = strings.TrimPrefix(p, "final ")
		sp := strings.LastIndexAny(p, " \t")
		if sp <= 0 {
			continue
		}
		typ := strings.TrimSpace(p[:sp])
		name := strings.TrimSpace(p[sp+1:])
		if typ == "" || name == "" || !isJavaIdent(name) {
			continue
		}
		params = append(params, parser.ParamIR{Name: name, Type: normalizeJavaType(typ), Index: len(params)})
	}
	return params
}

// splitJavaArgs 按顶层逗号拆分参数/实参（忽略泛型与注解括号内的逗号）。
func splitJavaArgs(s string) []string {
	var out []string
	depth := 0
	start := 0
	for i, r := range s {
		switch r {
		case '<', '(', '[':
			depth++
		case '>', ')', ']':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func isJavaIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && !(r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
		if i > 0 && !(r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// normalizeJavaType 去掉泛型实参与变长/数组后缀，得到可用于索引匹配的原始类名：
// `Page<Owner>` → `Page`，`String...` → `String`，`Pet[]` → `Pet`。
func normalizeJavaType(t string) string {
	t = strings.TrimSpace(t)
	for {
		i := strings.Index(t, "<")
		if i < 0 {
			break
		}
		j := strings.LastIndex(t, ">")
		if j < i {
			break
		}
		t = strings.TrimSpace(t[:i] + t[j+1:])
	}
	t = strings.TrimSuffix(t, "...")
	t = strings.TrimSuffix(t, "[]")
	return strings.TrimSpace(t)
}
