package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// fakeVizStore 是 viz 分页用例的最小 VizStore 实现。
type fakeVizStore struct{ docs []VizDocInfo }

func (f *fakeVizStore) Size() int { return len(f.docs) }

func (f *fakeVizStore) ListDocuments(ctx context.Context) ([]VizDocInfo, error) {
	return f.docs, nil
}

// TestVizAPIDocuments_PaginationBounds 是 P3-1 负控：offset/limit 为负数或越界时，
// 旧实现直接切片 docs[offset:end]，触发 slice bounds out of range panic（线上被
// recovery 兜成 500）。修复后一律夹取到 [0,len] 并返回 200。
func TestVizAPIDocuments_PaginationBounds(t *testing.T) {
	h := NewVizHandler(&fakeVizStore{docs: []VizDocInfo{{ID: "a"}, {ID: "b"}, {ID: "c"}}}, nil, 3, "")

	cases := []struct {
		url      string
		wantDocs int
	}{
		{"/viz/api/documents?limit=-1", 0},
		{"/viz/api/documents?offset=-2", 3},
		{"/viz/api/documents?offset=-2&limit=-3", 0},
		{"/viz/api/documents?offset=99&limit=10", 0},
		{"/viz/api/documents?offset=1&limit=1", 1},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.url, nil)
			rec := httptest.NewRecorder()
			h.apiDocuments(rec, req)

			if rec.Code != 200 {
				t.Fatalf("status want 200, got %d body=%s", rec.Code, rec.Body.String())
			}
			var resp struct {
				Total     int          `json:"total"`
				Offset    int          `json:"offset"`
				Limit     int          `json:"limit"`
				Documents []VizDocInfo `json:"documents"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Total != 3 {
				t.Errorf("total want 3, got %d", resp.Total)
			}
			if len(resp.Documents) != tc.wantDocs {
				t.Errorf("documents want %d, got %d (offset=%d limit=%d)",
					tc.wantDocs, len(resp.Documents), resp.Offset, resp.Limit)
			}
			if resp.Offset < 0 || resp.Limit < 0 {
				t.Errorf("分页参数应夹取为非负：offset=%d limit=%d", resp.Offset, resp.Limit)
			}
		})
	}
}
