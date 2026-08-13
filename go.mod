module github.com/idcu/codeschema

go 1.25.2

require (
	github.com/fsnotify/fsnotify v1.10.1
	github.com/philippgille/chromem-go v0.0.0-00010101000000-000000000000
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/yalue/onnxruntime_go v1.32.1 // indirect
	golang.org/x/sys v0.13.0 // indirect
)

replace github.com/philippgille/chromem-go => ./down/chromem-go/chromem-go-main
