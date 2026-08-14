package vector

// 内置 ONNX 语义模型注册表（E3 深化）。
//
// 目的：用户仅配置 `embedding_model` 即可自动下载已知模型——无需手填
// `model_download_url` 与 `model_sha256`；注册表外的模型仍走显式 URL 配置。
//
// 注册表条目结构：
//   - Name：模型名（对应 config.vector.embedding_model，如 bge-small-zh）
//   - DownloadURL：模型 tar.gz 制品地址（支持 {model} 占位，通常直接写死模型名）
//   - SHA256：制品校验和（安全分发；缺失时跳过校验但记录 WARN）
//
// 注意：模型制品托管于外部（本仓库不含大文件），URL 指向公开制品源。

// ModelRegistryEntry 注册表单条记录。
type ModelRegistryEntry struct {
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
	SHA256      string `json:"sha256,omitempty"`
}

// builtinModelRegistry 内置已知模型清单。
// 新增模型时在此追加；DownloadURL 指向实际发布的 tar.gz 制品。
var builtinModelRegistry = []ModelRegistryEntry{
	{
		Name:        "bge-small-zh",
		DownloadURL: "https://models.example.com/codeschema/bge-small-zh-v1.5.tar.gz",
		SHA256:      "",
	},
	{
		Name:        "bge-small-zh-v1.5",
		DownloadURL: "https://models.example.com/codeschema/bge-small-zh-v1.5.tar.gz",
		// 由 make models-pack MODEL=bge-small-zh-v1.5 生成的本地制品校验和
		// （build/models-bge-small-zh-v1.5.tar.gz）；制品托管后回填真实 URL。
		SHA256: "48b70f807905ede95483b4c204c5d59dc1ac5a665608149c3cff7d978e58b95f",
	},
	{
		Name:        "bge-base-zh",
		DownloadURL: "https://models.example.com/codeschema/bge-base-zh.tar.gz",
		SHA256:      "",
	},
}

// LookupModelRegistry 按模型名查注册表；未命中返回 (零值, false)。
func LookupModelRegistry(modelName string) (ModelRegistryEntry, bool) {
	for _, e := range builtinModelRegistry {
		if e.Name == modelName {
			return e, true
		}
	}
	return ModelRegistryEntry{}, false
}

// ResolveDownloadConfig 解析模型分发配置：
//   - 显式 URL 非空 → 直接使用（显式配置优先）；
//   - 否则查内置注册表 → 命中则回填 URL（可选 SHA256）；
//   - 否则返回空（调用方按「未配置远程源」降级）。
//
// 返回 (url, sha256, known)：known=false 表示注册表未命中且无显式 URL。
func ResolveDownloadConfig(modelName, explicitURL, explicitSHA string) (url, sha256 string, known bool) {
	if explicitURL != "" {
		return explicitURL, explicitSHA, true
	}
	if entry, ok := LookupModelRegistry(modelName); ok {
		return entry.DownloadURL, entry.SHA256, true
	}
	return "", "", false
}
