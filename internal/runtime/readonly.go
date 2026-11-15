package runtime

import (
	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/store"
)

// BuildReadOnlyService 为离线基准/只读分析工具装配最小 Service。
//
// 只读工具没有增量写入路径，不需要 Searcher / IndexBuilder / watcher，因此不
// 走 BuildRuntime（那会构建全量检索索引）。运行时组件的构造仍集中在 runtime
// 包内：仓内其他包不得直接调用 service.NewService。
func BuildReadOnlyService(st store.Store) *service.Service {
	return service.NewService(st)
}
