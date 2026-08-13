package store

// intervalsOverlap 判断两个行号区间是否重叠。
// 区间为闭区间 [start, end]。
// 若任一区间为空（start > end），返回 false。
func intervalsOverlap(aStart, aEnd, bStart, bEnd int) bool {
	if aStart > aEnd || bStart > bEnd {
		return false
	}
	return aStart <= bEnd && bStart <= aEnd
}

// MatchResult 描述一个实体在增量更新中的匹配结果。
type MatchResult int

const (
	// MatchUnknown 未匹配（默认值）
	MatchUnknown MatchResult = iota
	// MatchUpdate 同一实体，行号区间重叠 + 名称一致 → UPDATE
	MatchUpdate
	// MatchDelete 旧库有但新 IR 无 → DELETE
	MatchDelete
	// MatchInsert 新 IR 有但旧库无 → INSERT
	MatchInsert
	// MatchRelocate 区间不重叠但全限定名一致 → 重定位，UPDATE 行号
	MatchRelocate
)

// matchEntity 根据行号区间和名称判定新旧实体的匹配关系。
//
// 匹配判定表：
//   - 行号区间重叠 + 名称一致 → MatchUpdate
//   - 旧库有 / 新 IR 无        → MatchDelete
//   - 新 IR 有 / 旧库无        → MatchInsert
//   - 区间不重叠但全限定名一致  → MatchRelocate
func matchEntity(oldStart, oldEnd int, oldName string, hasOld bool,
	newStart, newEnd int, newName string, hasNew bool) MatchResult {

	switch {
	case !hasOld && hasNew:
		return MatchInsert
	case hasOld && !hasNew:
		return MatchDelete
	case !hasOld && !hasNew:
		return MatchUnknown
	}

	// 两者都存在：按区间和名称判断
	overlap := intervalsOverlap(oldStart, oldEnd, newStart, newEnd)
	nameMatch := oldName == newName

	if overlap && nameMatch {
		return MatchUpdate
	}
	if !overlap && nameMatch {
		return MatchRelocate
	}
	// 区间重叠但名称不同 → 视为 INSERT + DELETE（不同实体在相同位置）
	return MatchInsert
}