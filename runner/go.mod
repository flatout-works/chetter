module github.com/flatout-works/chetter/runner

go 1.26

toolchain go1.26.4

require (
	connectrpc.com/connect v1.19.2
	github.com/elazarl/goproxy v1.7.2
	github.com/flatout-works/chetter v0.0.0
	github.com/miekg/dns v1.1.72
	gopkg.in/yaml.v3 v3.0.1
)

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.11-20260415201107-50325440f8f2.1 // indirect
	golang.org/x/mod v0.35.0 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	golang.org/x/tools v0.44.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/flatout-works/chetter => ..
