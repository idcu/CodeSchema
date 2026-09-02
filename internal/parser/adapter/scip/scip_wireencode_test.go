package scip

// 测试内 protobuf wire 编码器：构造与真实 scip-typescript 0.4.0 产物同构的
// 二进制 .scip fixture（decode 侧 schema 见 scipwire.go 头注释，字段号一致）。
// 解码正确性由 TestSCIPAdapter_RealScipTypeScript（真实 scip-typescript 产物）
// 兜底；此处编码仅用于驱动 loadIndex/背压/幂等/convertDocument 等逻辑测试，
// 避免把 JSON 方言当 .scip（历史缺陷：fixture 与真实 schema 脱节）。

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// wireBytesField 写 length-delimited 字段（wt=2）。
func wireBytesField(b []byte, field int, payload []byte) []byte {
	b = appendVarint(b, uint64(field<<3|2))
	b = appendVarint(b, uint64(len(payload)))
	return append(b, payload...)
}

// wireVarintField 写 varint 字段（wt=0）。
func wireVarintField(b []byte, field int, v uint64) []byte {
	b = appendVarint(b, uint64(field<<3))
	return appendVarint(b, v)
}

func packedInt32(vals []int) []byte {
	var p []byte
	for _, v := range vals {
		p = appendVarint(p, uint64(v))
	}
	return p
}

// encSymbol 编码 SymbolInformation（真实产物仅 symbol=1 有值）。
func encSymbol(id string) []byte {
	return wireBytesField(nil, 1, []byte(id))
}

// encOccurrence 编码 Occurrence（实测字段序：range=1 packed、symbol=2、
// symbol_roles=3、enclosing_range=7 packed）。
func encOccurrence(symbol string, roles int, rng, enc []int) []byte {
	var b []byte
	if len(rng) > 0 {
		b = wireBytesField(b, 1, packedInt32(rng))
	}
	b = wireBytesField(b, 2, []byte(symbol))
	if roles != 0 {
		b = wireVarintField(b, 3, uint64(roles))
	}
	if len(enc) > 0 {
		b = wireBytesField(b, 7, packedInt32(enc))
	}
	return b
}

// encDocument 编码 Document（实测字段序：relative_path=1、occurrences=2、symbols=3）。
func encDocument(rel string, symIDs []string, occs ...*SCIPOccurrence) []byte {
	var b []byte
	b = wireBytesField(b, 1, []byte(rel))
	for _, o := range occs {
		b = wireBytesField(b, 2, encOccurrence(o.Symbol, o.SymbolRole, o.Range, o.EnclosingRange))
	}
	for _, id := range symIDs {
		b = wireBytesField(b, 3, encSymbol(id))
	}
	return b
}

// encIndex 编码顶层 Index（documents=2）。
func encIndex(docs ...[]byte) []byte {
	var b []byte
	for _, d := range docs {
		b = wireBytesField(b, 2, d)
	}
	return b
}
