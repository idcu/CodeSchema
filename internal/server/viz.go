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
	mux.HandleFunc("/viz/api/document", handler.apiDocument)
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

// apiDocument 返回单个文档的详细信息（含内容）。
func (h *VizHandler) apiDocument(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	docs, err := h.store.ListDocuments(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	for _, d := range docs {
		if d.ID == id {
			writeJSON(w, http.StatusOK, map[string]any{"document": d})
			return
		}
	}

	writeJSON(w, http.StatusNotFound, map[string]any{"error": "document not found"})
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
.header-top { display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 15px; }
.header h1 { font-size: 24px; }
.header .info { display: flex; gap: 20px; flex-wrap: wrap; }
.header .info-item { background: #f0f7ff; padding: 10px 15px; border-radius: 6px; font-size: 14px; }
.header .info-item .label { color: #666; font-size: 12px; }
.header .info-item .value { font-weight: 600; font-size: 18px; }
.btn-icon { background: #fff; border: 1px solid #ddd; border-radius: 6px; padding: 8px 14px; cursor: pointer; font-size: 14px; display: inline-flex; align-items: center; gap: 5px; }
.btn-icon:hover { background: #f0f7ff; border-color: #0066cc; color: #0066cc; }
.search-box { background: #fff; border-radius: 8px; padding: 15px; margin-bottom: 20px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); display: flex; gap: 10px; }
.search-box input { flex: 1; padding: 10px; font-size: 16px; border: 1px solid #ddd; border-radius: 4px; outline: none; }
.search-box input:focus { border-color: #0066cc; }
.search-box .btn-search { padding: 10px 20px; background: #0066cc; color: #fff; border: none; border-radius: 4px; cursor: pointer; font-size: 14px; }
.search-box .btn-search:hover { background: #0052a3; }
.doc-list { background: #fff; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); overflow: hidden; }
.doc-list table { width: 100%; border-collapse: collapse; }
.doc-list th { background: #f8f9fa; padding: 10px 15px; text-align: left; font-size: 13px; color: #666; border-bottom: 2px solid #eee; position: sticky; top: 0; z-index: 1; }
.doc-list td { padding: 10px 15px; border-bottom: 1px solid #eee; font-size: 13px; }
.doc-list tbody tr { cursor: pointer; }
.doc-list tbody tr:hover { background: #f0f7ff; }
.doc-list tbody tr.active { background: #e3f2fd; }
.doc-list .id { font-family: monospace; color: #0066cc; max-width: 300px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.doc-list .content-preview { color: #666; max-width: 400px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.doc-list .score { font-weight: 600; color: #28a745; }
.doc-list .pagination { display: flex; justify-content: center; align-items: center; gap: 10px; padding: 15px; }
.doc-list .pagination button { padding: 6px 12px; border: 1px solid #ddd; background: #fff; border-radius: 4px; cursor: pointer; }
.doc-list .pagination button:hover { background: #f0f7ff; }
.doc-list .pagination button:disabled { opacity: 0.5; cursor: default; }
.doc-detail { display: none; background: #fafafa; border-bottom: 1px solid #eee; }
.doc-detail.open { display: table-row; }
.doc-detail td { padding: 15px 20px; }
.doc-detail .detail-content { background: #fff; border: 1px solid #e0e0e0; border-radius: 4px; padding: 12px; font-family: 'SFMono-Regular', Consolas, monospace; font-size: 12px; line-height: 1.6; white-space: pre-wrap; word-break: break-all; max-height: 300px; overflow-y: auto; }
.doc-detail .detail-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; }
.doc-detail .detail-header .detail-id { font-weight: 600; font-family: monospace; color: #0066cc; }
.doc-detail .detail-header .detail-close { background: none; border: none; color: #999; cursor: pointer; font-size: 18px; padding: 0 4px; }
.doc-detail .detail-header .detail-close:hover { color: #333; }
.tag { display: inline-block; padding: 2px 8px; border-radius: 10px; font-size: 11px; font-weight: 500; }
.tag-persistent { background: #e3f2fd; color: #1565c0; }
.tag-memory { background: #fce4ec; color: #c62828; }
.loading { text-align: center; padding: 40px; color: #999; }
.error { color: #d32f2f; padding: 10px; }
.empty { text-align: center; padding: 40px; color: #999; }
.toast { position: fixed; bottom: 20px; right: 20px; background: #323232; color: #fff; padding: 12px 20px; border-radius: 6px; font-size: 14px; opacity: 0; transition: opacity 0.3s; z-index: 100; }
.toast.show { opacity: 1; }
@media (max-width: 768px) {
  .container { padding: 10px; }
  .header .info { gap: 10px; }
  .header .info-item { padding: 8px 12px; font-size: 12px; }
  .doc-list .id, .doc-list .content-preview { max-width: 150px; }
}
</style>
</head>
<body>
<div class="toast" id="toast"></div>
<div class="container" id="app">
  <div class="header">
    <div class="header-top">
      <h1>CodeSchema 向量索引可视化</h1>
      <button class="btn-icon" onclick="refreshAll()" title="刷新数据">↻ 刷新</button>
    </div>
    <div class="info" id="overview">
      <div class="info-item"><div class="label">文档数</div><div class="value" id="count">-</div></div>
      <div class="info-item"><div class="label">向量维度</div><div class="value" id="dim">-</div></div>
      <div class="info-item"><div class="label">模式</div><div class="value" id="mode">-</div></div>
      <div class="info-item"><div class="label">持久化路径</div><div class="value" id="path" style="font-size:13px">-</div></div>
    </div>
  </div>

  <div class="search-box">
    <input type="text" id="searchInput" placeholder="输入搜索文本，按 Enter 或点击搜索按钮查询..." />
    <button class="btn-search" onclick="doSearch()">搜索</button>
  </div>

  <div class="doc-list">
    <div id="resultsHeader" style="display:none; padding:10px 15px; background:#e8f5e9; font-size:14px; border-bottom:1px solid #c8e6c9; display:flex; justify-content:space-between; align-items:center;">
      <span id="resultsHeaderText"></span>
      <button class="btn-icon" onclick="clearSearch()" style="padding:4px 10px; font-size:12px;">✕ 清除</button>
    </div>
    <table>
      <thead><tr><th style="width:30%">文档 ID</th><th>内容预览</th><th style="width:100px">相似度</th></tr></thead>
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
let allDocs = []; // 缓存的文档列表，用于按 ID 查找内容

// --- Toast 通知 ---
function showToast(msg) {
  const t = document.getElementById('toast');
  t.textContent = msg;
  t.classList.add('show');
  setTimeout(function() { t.classList.remove('show'); }, 2500);
}

// --- 数据加载 ---
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
    allDocs = data.documents || [];
    renderDocuments(allDocs);
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
    searchResults = data.results || [];
    document.getElementById('resultsHeader').style.display = 'flex';
    document.getElementById('resultsHeaderText').textContent = '搜索 "' + query + '" 共 ' + data.count + ' 条结果';
    document.getElementById('pagination').style.display = 'none';
    renderSearchResults(searchResults);
  } catch(e) {
    tbody.innerHTML = '<tr><td colspan="3" class="error">搜索失败: ' + e.message + '</td></tr>';
  }
}

// --- 渲染 ---
function renderDocuments(docs) {
  const tbody = document.getElementById('docBody');
  if (!docs || docs.length === 0) {
    tbody.innerHTML = '<tr><td colspan="3" class="empty">暂无文档</td></tr>';
    return;
  }
  tbody.innerHTML = docs.map(function(d, i) {
    return '<tr onclick="toggleDetail(\'' + esc(d.ID) + '\', this)" title="点击展开内容">' +
      '<td class="id" title="' + esc(d.ID) + '">' + esc(d.ID) + '</td>' +
      '<td class="content-preview">' + esc(truncate(d.Content, 80)) + '</td>' +
      '<td>-</td></tr>' +
      '<tr class="doc-detail" id="detail-' + esc(d.ID) + '"><td colspan="3">' +
      '<div class="detail-header"><span class="detail-id">' + esc(d.ID) + '</span>' +
      '<button class="detail-close" onclick="event.stopPropagation(); closeDetail(\'' + esc(d.ID) + '\')">✕</button></div>' +
      '<div class="detail-content" id="content-' + esc(d.ID) + '">' + esc(d.Content) + '</div></td></tr>';
  }).join('');
}

function renderSearchResults(results) {
  const tbody = document.getElementById('docBody');
  if (!results || results.length === 0) {
    tbody.innerHTML = '<tr><td colspan="3" class="empty">无匹配结果</td></tr>';
    return;
  }
  tbody.innerHTML = results.map(function(r) {
    return '<tr onclick="fetchAndShowDetail(\'' + esc(r.ID) + '\', this)" title="点击展开内容">' +
      '<td class="id" title="' + esc(r.ID) + '">' + esc(r.ID) + '</td>' +
      '<td class="content-preview">-</td>' +
      '<td class="score">' + (r.Score * 100).toFixed(1) + '%</td></tr>' +
      '<tr class="doc-detail" id="detail-' + esc(r.ID) + '"><td colspan="3">' +
      '<div class="detail-header"><span class="detail-id">' + esc(r.ID) + '</span>' +
      '<button class="detail-close" onclick="event.stopPropagation(); closeDetail(\'' + esc(r.ID) + '\')">✕</button></div>' +
      '<div class="detail-content" id="content-' + esc(r.ID) + '">加载中...</div></td></tr>';
  }).join('');
}

// --- 展开/折叠详情 ---
function toggleDetail(id, row) {
  var detail = document.getElementById('detail-' + id);
  if (!detail) return;
  var isOpen = detail.classList.contains('open');
  closeAllDetails();
  if (!isOpen) {
    detail.classList.add('open');
    row.classList.add('active');
  }
}

function closeDetail(id) {
  var detail = document.getElementById('detail-' + id);
  if (!detail) return;
  detail.classList.remove('open');
  var rows = document.querySelectorAll('#docBody tr');
  for (var i = 0; i < rows.length; i++) {
    rows[i].classList.remove('active');
  }
}

function closeAllDetails() {
  var details = document.querySelectorAll('.doc-detail');
  for (var i = 0; i < details.length; i++) {
    details[i].classList.remove('open');
  }
  var rows = document.querySelectorAll('#docBody tr');
  for (var i = 0; i < rows.length; i++) {
    rows[i].classList.remove('active');
  }
}

async function fetchAndShowDetail(id, row) {
  var detail = document.getElementById('detail-' + id);
  if (!detail) return;
  var isOpen = detail.classList.contains('open');
  closeAllDetails();
  if (!isOpen) {
    detail.classList.add('open');
    row.classList.add('active');
    // 尝试从本地缓存获取内容
    var contentDiv = document.getElementById('content-' + id);
    if (contentDiv && contentDiv.textContent === '加载中...') {
      try {
        var r = await fetch('/viz/api/document?id=' + encodeURIComponent(id));
        var data = await r.json();
        if (data.document) {
          contentDiv.textContent = data.document.content || '（无内容）';
          // 更新内容预览列
          var preview = row.querySelector('.content-preview');
          if (preview) preview.textContent = truncate(data.document.content, 80);
        } else {
          contentDiv.textContent = '（未找到文档内容）';
        }
      } catch(e) {
        contentDiv.textContent = '加载失败: ' + e.message;
      }
    }
  }
}

// --- 工具函数 ---
function esc(s) { return (s || '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;'); }
function truncate(s, n) { return s && s.length > n ? s.substring(0, n) + '...' : s; }

// --- 分页 ---
function updatePagination(offset) {
  currentPage = offset / PAGE_SIZE;
  var totalPages = Math.ceil(totalDocs / PAGE_SIZE) || 1;
  document.getElementById('pageInfo').textContent = '第 ' + (currentPage + 1) + ' / ' + totalPages + ' 页 （共 ' + totalDocs + ' 条）';
  document.getElementById('prevBtn').disabled = currentPage <= 0;
  document.getElementById('nextBtn').disabled = currentPage >= totalPages - 1;
  document.getElementById('pagination').style.display = '';
}

function prevPage() {
  if (currentPage > 0) {
    searchResults = null;
    closeAllDetails();
    loadDocuments((currentPage - 1) * PAGE_SIZE);
  }
}

function nextPage() {
  searchResults = null;
  closeAllDetails();
  loadDocuments((currentPage + 1) * PAGE_SIZE);
}

// --- 搜索控制 ---
function doSearch() {
  var input = document.getElementById('searchInput');
  var q = input.value.trim();
  if (q) {
    closeAllDetails();
    searchDocuments(q);
  } else {
    clearSearch();
  }
}

function clearSearch() {
  document.getElementById('searchInput').value = '';
  searchResults = null;
  closeAllDetails();
  loadDocuments(0);
}

// --- 刷新 ---
function refreshAll() {
  closeAllDetails();
  loadOverview();
  if (searchResults) {
    var q = document.getElementById('searchInput').value.trim();
    if (q) searchDocuments(q);
  } else {
    loadDocuments(currentPage * PAGE_SIZE);
  }
  showToast('数据已刷新');
}

// --- 事件绑定 ---
document.getElementById('searchInput').addEventListener('keyup', function(e) {
  if (e.key === 'Enter') {
    doSearch();
  }
});

// 点击页面空白处关闭详情
document.addEventListener('click', function(e) {
  if (!e.target.closest('.doc-list')) {
    closeAllDetails();
  }
});

// --- 初始化 ---
loadOverview();
loadDocuments(0);
</script>
</body>
</html>`