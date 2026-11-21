.PHONY: check golden demo
check:
	@test -z "$$(gofmt -l $$(find cmd internal -name '*.go'))" || (echo 'Run gofmt on Go sources'; exit 1)
	go vet ./...
	go test ./...
# -update belongs to the packages that write reviewed golden frames. Sending it
# to every test binary lets any new package turn this target into a flag error.
golden:
	go test ./internal/scene ./cmd/percolate -update
demo:
	./demo/generate.sh
