package store

import "testing"

func TestIntervalsOverlap(t *testing.T) {
	tests := []struct {
		name         string
		aStart, aEnd int
		bStart, bEnd int
		want         bool
	}{
		{"完全重叠", 1, 10, 1, 10, true},
		{"部分重叠", 1, 10, 5, 15, true},
		{"A 包含 B", 1, 20, 5, 10, true},
		{"B 包含 A", 5, 10, 1, 20, true},
		{"相邻不重叠", 1, 10, 11, 20, false},
		{"不相邻", 1, 5, 10, 20, false},
		{"A 为空", 5, 3, 1, 10, false},
		{"B 为空", 1, 10, 5, 3, false},
		{"单行重叠", 5, 5, 5, 5, true},
		{"单行相邻", 5, 5, 6, 6, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := intervalsOverlap(tt.aStart, tt.aEnd, tt.bStart, tt.bEnd)
			if got != tt.want {
				t.Errorf("intervalsOverlap(%d,%d, %d,%d) = %v, want %v",
					tt.aStart, tt.aEnd, tt.bStart, tt.bEnd, got, tt.want)
			}
		})
	}
}

func TestMatchEntity(t *testing.T) {
	tests := []struct {
		name             string
		oldStart, oldEnd int
		oldName          string
		hasOld           bool
		newStart, newEnd int
		newName          string
		hasNew           bool
		want             MatchResult
	}{
		{
			name:     "新增实体",
			hasOld:   false,
			hasNew:   true,
			newStart: 1, newEnd: 10, newName: "Foo",
			want: MatchInsert,
		},
		{
			name:     "删除实体",
			hasOld:   true,
			hasNew:   false,
			oldStart: 1, oldEnd: 10, oldName: "Foo",
			want: MatchDelete,
		},
		{
			name:   "更新实体（区间重叠+名称一致）",
			hasOld: true, hasNew: true,
			oldStart: 1, oldEnd: 10, oldName: "Foo",
			newStart: 1, newEnd: 12, newName: "Foo",
			want: MatchUpdate,
		},
		{
			name:   "重定位（区间不重叠+名称一致）",
			hasOld: true, hasNew: true,
			oldStart: 1, oldEnd: 10, oldName: "Foo",
			newStart: 50, newEnd: 60, newName: "Foo",
			want: MatchRelocate,
		},
		{
			name:   "同名不同实体（区间重叠但名称不同→新增）",
			hasOld: true, hasNew: true,
			oldStart: 1, oldEnd: 10, oldName: "Foo",
			newStart: 1, newEnd: 10, newName: "Bar",
			want: MatchInsert,
		},
		{
			name:   "两者都不存在",
			hasOld: false, hasNew: false,
			want: MatchUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchEntity(tt.oldStart, tt.oldEnd, tt.oldName, tt.hasOld,
				tt.newStart, tt.newEnd, tt.newName, tt.hasNew)
			if got != tt.want {
				t.Errorf("matchEntity = %v, want %v", got, tt.want)
			}
		})
	}
}
