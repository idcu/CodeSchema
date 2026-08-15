package server

import (
	"encoding/json"
	"net/http"
)

// openAPISpec OpenAPI 3.0 规范（静态定义，单源）。
var openapiJSON = []byte(`{
  "openapi": "3.0.3",
  "info": {
    "title": "CodeSchema HTTP API",
    "description": "代码元数据 KV/DB 系统 —— 面向 AI 辅助开发的代码元数据索引与上下文裁剪服务。提供符号查询、影响面分析、测试关联、双路检索（FTS + 向量语义）、标签分类等能力。",
    "version": "0.1.0"
  },
  "paths": {
    "/health": { "get": { "summary": "基础健康检查", "responses": { "200": { "description": "OK" } } } },
    "/health/db": { "get": { "summary": "存储层健康检查", "responses": { "200": { "description": "OK" } } } },
    "/health/kv": { "get": { "summary": "KV 缓存健康检查", "responses": { "200": { "description": "OK" } } } },
    "/health/vector": { "get": { "summary": "向量库健康检查", "responses": { "200": { "description": "OK" } } } },
    "/context": { "get": { "summary": "获取符号的精准裁剪上下文", "parameters": [ { "name": "symbol", "in": "query", "required": true, "schema": { "type": "string" } }, { "name": "context_lines", "in": "query", "schema": { "type": "integer", "default": 5 } } ], "responses": { "200": { "description": "上下文" }, "404": { "description": "符号未找到" } } } },
    "/impact": { "get": { "summary": "分析方法的调用影响面", "parameters": [ { "name": "method", "in": "query", "required": true, "schema": { "type": "string" } }, { "name": "depth", "in": "query", "schema": { "type": "integer", "default": 1 } } ], "responses": { "200": { "description": "影响面" } } } },
    "/tests": { "get": { "summary": "查询方法的关联单测", "parameters": [ { "name": "method", "in": "query", "required": true, "schema": { "type": "string" } }, { "name": "min_confidence", "in": "query", "schema": { "type": "number", "default": 60 } } ], "responses": { "200": { "description": "单测列表" } } } },
    "/search": { "get": { "summary": "双路检索（FTS + 向量语义）", "parameters": [ { "name": "q", "in": "query", "required": true, "schema": { "type": "string" } }, { "name": "mode", "in": "query", "schema": { "type": "string", "enum": ["exact", "semantic", "both"], "default": "both" } }, { "name": "limit", "in": "query", "schema": { "type": "integer", "default": 20 } } ], "responses": { "200": { "description": "搜索结果" } } } },
    "/tags": { "get": { "summary": "获取符号的标签", "parameters": [ { "name": "symbol", "in": "query", "required": true, "schema": { "type": "string" } } ], "responses": { "200": { "description": "标签" } } } },
    "/tags/search": { "get": { "summary": "按标签搜索符号", "parameters": [ { "name": "tag", "in": "query", "required": true, "schema": { "type": "string" } } ], "responses": { "200": { "description": "符号列表" } } } },
    "/tags/all": { "get": { "summary": "所有标签及分类", "responses": { "200": { "description": "标签统计" } } } },
    "/metrics": { "get": { "summary": "Prometheus 指标", "responses": { "200": { "description": "指标" } } } },
    "/openapi.json": { "get": { "summary": "OpenAPI 3.0 规范", "responses": { "200": { "description": "规范 JSON" } } } },
    "/docs": { "get": { "summary": "API 文档页（swagger-ui）", "responses": { "200": { "description": "HTML" } } } }
  }
}`)

// handleOpenAPI 返回 OpenAPI 3.0 规范 JSON（T4-2）。
func handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	var spec map[string]any
	if err := json.Unmarshal(openapiJSON, &spec); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "invalid openapi spec"})
		return
	}
	writeJSON(w, http.StatusOK, spec)
}

// handleAPIDocs 返回内置 API 文档页（HTML，内嵌 swagger-ui CDN 与规范加载）。
func handleAPIDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>CodeSchema API Docs</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body style="margin:0">
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
  window.onload = function() {
    window.ui = SwaggerUIBundle({
      url: "/openapi.json",
      dom_id: "#swagger-ui",
      deepLinking: true,
      presets: [SwaggerUIBundle.presets.apis]
    });
  };
</script>
</body>
</html>`))
}
