// Package server 提供 HTTP API 和可视化工具。
package server

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"
)

// VizDocInfo 可视化工具中文档信息。
type VizDocInfo struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

// VizSearchResult 可视化工具中搜索结果的精简信息。
type VizSearchResult struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

// VizStore 可视化工具所需的存储接口。
type VizStore interface {
	Size() int
	ListDocuments(ctx context.Context) ([]VizDocInfo, error)
}

// VizSearcher 可视化工具所需的搜索接口。
type VizSearcher interface {
	QueryText(ctx context.Context, query string, k int) ([]VizSearchResult, error)
}

// VizHandler 向量索引可视化工具 HTTP 处理器。
type VizHandler struct {
	store    VizStore
	searcher VizSearcher
	dim      int
	path     string
	mu       sync.RWMutex
}

// NewVizHandler 创建可视化工具处理器。
//   - store: 实现 VizStore 接口的向量存储
//   - searcher: 实现 VizSearcher 接口的搜索器（可选，为 nil 时禁用搜索）
//   - dim: 向量维度
//   - path: 持久化路径（为空时表示内存模式）
func NewVizHandler(store VizStore, searcher VizSearcher, dim int, path string) *VizHandler {
	return &VizHandler{
		store:    store,
		searcher: searcher,
		dim:      dim,
		path:     path,
	}
}

// RegisterVizRoutes 注册可视化工具路由到指定的 HTTP mux。
func RegisterVizRoutes(mux *http.ServeMux, handler *VizHandler) {
	mux.HandleFunc("/viz", handler.dashboard)
	mux.HandleFunc("/viz/", handler.dashboard)
	mux.HandleFunc("/viz/api/overview", handler.apiOverview)
	mux.HandleFunc("/viz/api/documents", handler.apiDocuments)
	mux.HandleFunc("/viz/api/search", handler.apiSearch)
}

// dashboard 渲染可视化仪表盘页面。
func (h *VizHandler) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/viz" && r.URL.Path != "/viz/" {
		http.NotFound(w, r)
		return
	}

	tmpl := template.Must(template.New("viz").Parse(defaultVizHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, nil)
}

// apiOverview 返回集合概览信息。
func (h *VizHandler) apiOverview(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	docs, err := h.store.ListDocuments(ctx)
	count := 0
	if err == nil {
		count = len(docs)
	}

	resp := map[string]any{
		"collection_name": "codeschema",
		"dimension":       h.dim,
		"document_count":  count,
		"persist_path":    h.path,
		"mode":            map[bool]string{true: "persistent", false: "memory"}[h.path != ""],
		"search_enabled":  h.searcher != nil,
	}

	writeJSON(w, http.StatusOK, resp)
}

// apiDocuments 返回文档列表。
func (h *VizHandler) apiDocuments(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	docs, err := h.store.ListDocuments(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	// 支持分页
	offset := 0
	limit := len(docs)
	if o := r.URL.Query().Get("offset"); o != "" {
		fmt.Sscanf(o, "%d", &offset)
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	end := offset + limit
	if end > len(docs) {
		end = len(docs)
	}
	if offset > len(docs) {
		offset = len(docs)
	}

	resp := map[string]any{
		"total":  len(docs),
		"offset": offset,
		"limit":  limit,
		"documents": docs[offset:end],
	}
	writeJSON(w, http.StatusOK, resp)
}

// apiSearch 执行文本搜索并返回结果。
func (h *VizHandler) apiSearch(w http.ResponseWriter, r *http.Request) {
	if h.searcher == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "search not available"})
		return
	}

	q := r.URL.Query().Get("q")
	if strings.TrimSpace(q) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "query required"})
		return
	}

	k := 20
	if kStr := r.URL.Query().Get("k"); kStr != "" {
		fmt.Sscanf(kStr, "%d", &k)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	results, err := h.searcher.QueryText(ctx, q, k)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	resp := map[string]any{
		"query":   q,
		"results": results,
		"count":   len(results),
	}
	writeJSON(w, http.StatusOK, resp)
}

const defaultVizHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>CodeSchema 向量索引可视化</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #f5f5f5; color: #333; }
.container { max-width: 1200px; margin: 0 auto; padding: 20px; }
.header { background: #fff; border-radius: 8px; padding: 20px; margin-bottom: 20px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
.header h1 { font-size: 24px; margin-bottom: 10px; }
.header .info { display: flex; gap: 20px; flex-wrap: wrap; }
.header .info-item { background: #f0f7ff; padding: 10px 15px; border-radius: 6px; font-size: 14px; }
.header .info-item .label { color: #666; font-size: 12px; }
.header .info-item .value { font-weight: 600; font-size: 18px; }
.search-box { background: #fff; border-radius: 8px; padding: 15px; margin-bottom: 20px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
.search-box input { width: 100%; padding: 10px; font-size: 16px; border: 1px solid #ddd; border-radius: 4px; outline: none; }
.search-box input:focus { border-color: #0066cc; }
.doc-list { background: #fff; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); overflow: hidden; }
.doc-list table { width: 100%; border-collapse: collapse; }
.doc-list th { background: #f8f9fa; padding: 10px 15px; text-align: left; font-size: 13px; color: #666; border-bottom: 2px solid #eee; }
.doc-list td { padding: 10px 15px; border-bottom: 1px solid #eee; font-size: 13px; }
.doc-list tr:hover { background: #f0f7ff; }
.doc-list .id { font-family: monospace; color: #0066cc; max-width: 300px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.doc-list .score { font-weight: 600; color: #28a745; }
.doc-list .pagination { display: flex; justify-content: center; gap: 10px; padding: 15px; }
.doc-list .pagination button { padding: 6px 12px; border: 1px solid #ddd; background: #fff; border-radius: 4px; cursor: pointer; }
.doc-list .pagination button:hover { background: #f0f7ff; }
.doc-list .pagination button:disabled { opacity: 0.5; cursor: default; }
.tag { display: inline-block; padding: 2px 8px; border-radius: 10px; font-size: 11px; font-weight: 500; }
.tag-persistent { background: #e3f2fd; color: #1565c0; }
.tag-memory { background: #fce4ec; color: #c62828; }
.loading { text-align: center; padding: 40px; color: #999; }
.error { color: #d32f2f; padding: 10px; }
.empty { text-align: center; padding: 40px; color: #999; }
</style>
</head>
<body>
<div class="container" id="app">
  <div class="header">
    <h1>CodeSchema 向量索引可视化</h1>
    <div class="info" id="overview">
      <div class="info-item"><div class="label">文档数</div><div class="value" id="count">-</div></div>
      <div class="info-item"><div class="label">向量维度</div><div class="value" id="dim">-</div></div>
      <div class="info-item"><div class="label">模式</div><div class="value" id="mode">-</div></div>
      <div class="info-item"><div class="label">持久化路径</div><div class="value" id="path" style="font-size:13px">-</div></div>
    </div>
  </div>

  <div class="search-box">
    <input type="text" id="searchInput" placeholder="输入搜索文本，按 Enter 查询..." />
  </div>

  <div class="doc-list">
    <div id="resultsHeader" style="display:none; padding:10px 15px; background:#e8f5e9; font-size:14px; border-bottom:1px solid #c8e6c9;"></div>
    <table>
      <thead><tr><th style="width:40%">文档 ID</th><th>内容</th><th style="width:100px">相似度</th></tr></thead>
      <tbody id="docBody"><tr><td colspan="3" class="loading">加载中...</td></tr></tbody>
    </table>
    <div class="pagination" id="pagination">
      <button id="prevBtn" onclick="prevPage()" disabled>上一页</button>
      <span id="pageInfo" style="line-height:32px">第 1 页</span>
      <button id="nextBtn" onclick="nextPage()">下一页</button>
    </div>
  </div>
</div>

<script>
const PAGE_SIZE = 20;
let currentPage = 0;
let totalDocs = 0;
let searchResults = null;

async function loadOverview() {
  try {
    const r = await fetch('/viz/api/overview');
    const data = await r.json();
    document.getElementById('count').textContent = data.document_count;
    document.getElementById('dim').textContent = data.dimension;
    document.getElementById('mode').innerHTML = '<span class="tag ' + (data.mode === 'persistent' ? 'tag-persistent' : 'tag-memory') + '">' + data.mode + '</span>';
    document.getElementById('path').textContent = data.persist_path || '（内存模式）';
    totalDocs = data.document_count;
  } catch(e) {
    document.getElementById('overview').innerHTML = '<div class="error">加载概览失败: ' + e.message + '</div>';
  }
}

async function loadDocuments(offset) {
  const tbody = document.getElementById('docBody');
  tbody.innerHTML = '<tr><td colspan="3" class="loading">加载中...</td></tr>';

  try {
    const r = await fetch('/viz/api/documents?offset=' + offset + '&limit=' + PAGE_SIZE);
    const data = await r.json();
    if (data.error) {
      tbody.innerHTML = '<tr><td colspan="3" class="error">' + data.error + '</td></tr>';
      return;
    }
    renderDocuments(data.documents);
    updatePagination(offset);
    document.getElementById('resultsHeader').style.display = 'none';
  } catch(e) {
    tbody.innerHTML = '<tr><td colspan="3" class="error">加载失败: ' + e.message + '</td></tr>';
  }
}

async function searchDocuments(query) {
  const tbody = document.getElementById('docBody');
  tbody.innerHTML = '<tr><td colspan="3" class="loading">搜索中...</td></tr>';

  try {
    const r = await fetch('/viz/api/search?q=' + encodeURIComponent(query) + '&k=20');
    const data = await r.json();
    if (data.error) {
      tbody.innerHTML = '<tr><td colspan="3" class="error">' + data.error + '</td></tr>';
      return;
    }
    searchResults = data.results;
    document.getElementById('resultsHeader').style.display = 'block';
    document.getElementById('resultsHeader').textContent = '搜索 "' + query + '" 共 ' + data.count + ' 条结果';
    document.getElementById('pagination').style.display = 'none';
    renderSearchResults(data.results);
  } catch(e) {
    tbody.innerHTML = '<tr><td colspan="3" class="error">搜索失败: ' + e.message + '</td></tr>';
  }
}

function renderDocuments(docs) {
  const tbody = document.getElementById('docBody');
  if (!docs || docs.length === 0) {
    tbody.innerHTML = '<tr><td colspan="3" class="empty">暂无文档</td></tr>';
    return;
  }
  tbody.innerHTML = docs.map(d => '<tr><td class="id" title="' + esc(d.ID) + '">' + esc(d.ID) + '</td><td>' + esc(truncate(d.Content, 80)) + '</td><td>-</td></tr>').join('');
}

function renderSearchResults(results) {
  const tbody = document.getElementById('docBody');
  if (!results || results.length === 0) {
    tbody.innerHTML = '<tr><td colspan="3" class="empty">无匹配结果</td></tr>';
    return;
  }
  tbody.innerHTML = results.map(r => '<tr><td class="id" title="' + esc(r.ID) + '">' + esc(r.ID) + '</td><td>-</td><td class="score">' + (r.Score * 100).toFixed(1) + '%</td></tr>').join('');
}

function esc(s) { return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;'); }
function truncate(s, n) { return s && s.length > n ? s.substring(0, n) + '...' : s; }

function updatePagination(offset) {
  currentPage = offset / PAGE_SIZE;
  const totalPages = Math.ceil(totalDocs / PAGE_SIZE) || 1;
  document.getElementById('pageInfo').textContent = '第 ' + (currentPage + 1) + ' / ' + totalPages + ' 页';
  document.getElementById('prevBtn').disabled = currentPage <= 0;
  document.getElementById('nextBtn').disabled = currentPage >= totalPages - 1;
  document.getElementById('pagination').style.display = '';
}

function prevPage() {
  if (currentPage > 0) {
    searchResults = null;
    loadDocuments((currentPage - 1) * PAGE_SIZE);
  }
}

function nextPage() {
  searchResults = null;
  loadDocuments((currentPage + 1) * PAGE_SIZE);
}

document.getElementById('searchInput').addEventListener('keyup', function(e) {
  if (e.key === 'Enter') {
    const q = this.value.trim();
    if (q) searchDocuments(q);
    else { searchResults = null; loadDocuments(0); }
  }
});

loadOverview();
loadDocuments(0);
</script>
</body>
</html>`