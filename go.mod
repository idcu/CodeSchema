module codeschema

go 1.25.2

require (
	github.com/fsnotify/fsnotify v1.10.1
	gopkg.in/yaml.v3 v3.0.1
)

require golang.org/x/sys v0.13.0 // indirect

replace github.com/philippgille/chromem-go => ./down/chromem-go/chromem-go-main
