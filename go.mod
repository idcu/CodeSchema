module github.com/idcu/codeschema

go 1.25.2

require (
	github.com/fergusstrange/embedded-postgres v1.28.0
	github.com/fsnotify/fsnotify v1.10.1
	github.com/lib/pq v1.12.3
	github.com/philippgille/chromem-go v0.0.0-00010101000000-000000000000
	github.com/redis/go-redis/v9 v9.22.0
	github.com/smacker/go-tree-sitter v0.0.0-20240827094217-dd81d9e9be82
	github.com/yalue/onnxruntime_go v1.32.1
	golang.org/x/sys v0.47.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.56.0
)

require (
	gitee.com/idcu-go/pathsafe v0.1.0
	gitee.com/idcu-go/trim v0.1.0
	gitee.com/idcu-go/ttlcache v0.1.0
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/xi2/xz v0.0.0-20171230120015-48954b6210f8 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

replace github.com/philippgille/chromem-go => ./down/chromem-go/chromem-go-main

replace github.com/yalue/onnxruntime_go => ./third_party/onnxruntime_go_patch

replace gitee.com/idcu-go/trim => ../idcu-go/trim

replace gitee.com/idcu-go/ttlcache => ../idcu-go/ttlcache

replace gitee.com/idcu-go/pathsafe => ../idcu-go/pathsafe
