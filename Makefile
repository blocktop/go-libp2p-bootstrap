deps:
	go get -u github.com/whyrusleeping/gx
install:
	gx install
	go get ./...
deps_hack:
	gx-go rewrite
deps_hack_revert:
	gx-go uw
test:
	go test -cover