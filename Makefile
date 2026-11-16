# GH Manager —— 日常只需要 `make run`，其余操作都在网页中完成。

BIN      ?= bin/gh-manager
PORT     ?=
HOME_DIR ?=
OPEN     ?= 1

VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE     := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -s -w -X github.com/xiaoyumuxi/gh-manager/cmd.version=$(VERSION) \
            -X github.com/xiaoyumuxi/gh-manager/cmd.commit=$(COMMIT) \
            -X github.com/xiaoyumuxi/gh-manager/cmd.date=$(DATE)

RUN_FLAGS := $(if $(PORT),--port $(PORT)) $(if $(HOME_DIR),--home $(HOME_DIR)) $(if $(filter 1 true yes,$(OPEN)),--open)

.DEFAULT_GOAL := run
.PHONY: run build test fmt vet check clean help

run: build ## 构建并启动网页（PORT=8787 HOME_DIR=~/.config/gh-manager OPEN=0）
	@$(BIN) $(RUN_FLAGS)

build: ## 构建到 bin/
	@mkdir -p $(dir $(BIN))
	@CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) .

test: ## 运行测试
	go test ./...

fmt: ## 格式化 Go 代码
	gofmt -w cmd internal main.go

vet: ## 静态检查
	go vet ./...

check: vet test ## vet + test + 前端语法检查
	@if command -v node >/dev/null 2>&1; then for f in internal/web/static/*.js; do node --check $$f || exit 1; done; echo "前端脚本语法 OK"; fi

clean: ## 清理构建产物
	rm -rf bin dist

help: ## 显示可用命令
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'
