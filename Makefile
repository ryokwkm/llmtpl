# 配置先は BIN で差し替えられる（配布スクリプトから `make update BIN=...` で叩ける）
BIN ?= $(HOME)/.local/bin/llmtpl

.PHONY: help build test vet fmt install install-completion update

help: ## このヘルプを表示
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## バイナリを $(BIN) へビルド
	go build -o $(BIN) .

test: ## 全テスト
	go test ./...

vet: ## go vet
	go vet ./...

fmt: ## gofmt -w
	gofmt -w .

install: test build install-completion ## テスト → ビルド → 配置（+ zsh 補完）
	@echo "installed: $(BIN)"

# zsh 補完をベストエフォートで配置する（dq と同じ方式）。
# brew zsh の site-functions が書き込み可能ならそこへ（zsh が自動で拾う）、
# 取れなければ ~/.zsh/completion に置いて fpath 追加を案内する。
install-completion: ## zsh 補完スクリプトを配置する
	@BREW_PREFIX="$$(brew --prefix 2>/dev/null)"; \
	SITE_DIR="$$BREW_PREFIX/share/zsh/site-functions"; \
	if [ -n "$$BREW_PREFIX" ] && [ -d "$$SITE_DIR" ] && [ -w "$$SITE_DIR" ]; then \
		$(BIN) completion zsh > "$$SITE_DIR/_llmtpl" && \
		echo "completion: $$SITE_DIR/_llmtpl (新しいシェルから有効)"; \
	else \
		mkdir -p $(HOME)/.zsh/completion && \
		$(BIN) completion zsh > $(HOME)/.zsh/completion/_llmtpl && \
		echo "completion: $(HOME)/.zsh/completion/_llmtpl"; \
		echo "            → .zshrc の compinit より前に次を追記:"; \
		echo "                  fpath=(~/.zsh/completion \$$fpath)"; \
	fi

update: ## git pull してから install（設定リポジトリの同期スクリプトから呼ぶ用）
	git pull
	$(MAKE) install
