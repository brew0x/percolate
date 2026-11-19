.PHONY: check golden demo
check:
	@test -z "$$(gofmt -l $$(find cmd internal -name '*.go'))" || (echo 'Run gofmt on Go sources'; exit 1)
	go vet ./...
	go test ./...
golden:
	go test ./... -update
demo:
	./demo/generate.sh
