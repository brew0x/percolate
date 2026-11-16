.PHONY: check golden
check:
	@test -z "$$(gofmt -l $$(find cmd internal -name '*.go'))" || (echo 'Run gofmt on Go sources'; exit 1)
	go vet ./...
	go test ./...
golden:
	go test ./... -update
