# The list of files we want to go into core.js, which is concat'd and
# minified. These files should be either present in the project, brought
# into third_party/bower_components via bower, or in node_modules.
CORE_SOURCE_FILES = node_modules/native-promise-only/npo.js \
					third_party/bower_components/webcomponentsjs/webcomponents-lite.min.js \
          ../res/js/common.js

BOWER_DIR=third_party/bower_components

VULCANIZE1=true

.PHONY: build
build: web ptracequery
	go install -v ./go/skiaperf

ptracequery:
	go install -v ./go/ptracequery

.PHONY: web
web: clean_webtools elements_html core_js

.PHONY: release
release: build
	echo

.PHONY: test
test: testgo
	echo

.PHONY: testgo
testgo:
	go test ./go/...

.PHONY: push
push: build
	./build_release "`git log -n1 --format=%s`"
	go install -v ../push/go/pushcli
	pushcli skiaperfd skia-perf

.PHONY: logs
logs:
	go install -v ../logserver/go/logserver

.PHONY: tool
tool:
	go install -v ./go/tiletool

.PHONY: perf_migratedb
perf_migratedb:
	go install -v ./go/perf_migratedb

# Build debug versions of core.js and elements.html.
.PHONY: debug
debug: clean_webtools debug_core_js debug_elements_html

.PHONY: all
all: build tool logs perf_migratedb

.PHONY: tags
tags:
	-rm tags
	find . -name "*.go" -print -or -name "*.js" -or -name "*.html" | xargs ctags --append

include ../webtools/webtools.mk
