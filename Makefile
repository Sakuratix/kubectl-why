.PHONY: build test golden lint e2e demo
build:
	go build -o kubectl-why ./cmd/kubectl-why
test:
	go test ./...
golden: ## regenerate testdata/*/want.txt after an intended output change
	go test ./cmd/kubectl-why -run TestGolden -update
lint:
	gofmt -l . && go vet ./...
e2e: build ## needs a throwaway cluster, e.g. `kind create cluster`
	BIN=./kubectl-why test/e2e/run.sh
demo: ## regenerate docs/demo.svg from the deployment fixture
	go test ./cmd/kubectl-why -run 'TestGolden|TestDemo' -update
	python3 hack/ansi2svg.py "kubectl why deploy/api -n shop" < docs/demo.ansi > docs/demo.svg
