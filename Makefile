MODULE  := github.com/RidgetopAi/backstory
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BIN     := bin/backstory
GO      ?= go

QMLTESTRUNNER ?= qmltestrunner

.PHONY: install uninstall build fmt-check vet lint test integration panel-qml-test check release-check clean

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X $(MODULE)/internal/version.Version=$(VERSION)" -o $(BIN) ./cmd/backstory

# install: binary -> ~/.local/bin, user unit, hypr keybind file, Omarchy panel
# plugin; first install and upgrade alike (ops/install.sh). HOME and
# XDG_CONFIG_HOME pick the target; uninstall leaves the memory store alone.
install: build
	./ops/install.sh install $(BIN)

uninstall:
	./ops/install.sh uninstall

fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt: files need formatting:"; echo "$$out"; exit 1; fi

# -tags backstorytest (here and on lint/test/integration below) builds
# cmd/backstory's own test binary with daemon_procfs_backstorytest.go
# (BACKSTORY_TEST_FAKE_ANCESTRY support) instead of daemon_procfs_release.go
# — required because hook_test.go and hook_posttooluse_test.go reference
# that file's fakeAncestryEnvVar symbol directly and spawn daemon
# subprocesses built with the same tag (buildBackstory/buildBackstoryHarness
# in daemon_test.go and hook_posttooluse_test.go). `make build` above passes
# no tags, so the override never reaches a release binary (task fe7aee40).
vet:
	$(GO) vet -tags backstorytest ./...

lint:
	golangci-lint run --build-tags backstorytest ./...

test:
	$(GO) test -race -count=1 -tags backstorytest ./...

integration:
	$(GO) test -race -count=1 -tags backstorytest,integration ./...

# panel-qml-test is the QML half of task 4fe02e30's DONE WHEN clause (1):
# it loads every panel/*.qml against panel/qmltest/stubs's stand-ins for
# qs.Ui, qs.Commons, Quickshell, Quickshell.Io and Quickshell.Wayland, and
# runs panel/qmltest/tests under QtTest. QT_FATAL_WARNINGS=1 is this
# harness's "fails on ANY QML warning" gate — a QML runtime warning (a
# binding TypeError, a binding loop) aborts the whole run with a non-zero
# exit rather than a silently-passing QWARN line (see
# panel/qmltest/tests/tst_load_all.qml's own doc comment for why: no
# QQmlEngine.warnings hook is reachable from pure QML in this harness).
# This target FAILS (never skips) when qmltestrunner isn't on PATH — a
# Node-less/Qt-less machine must not appear to pass this gate silently, the
# same discipline js_runtime_test.go's requireNode already holds JS to.
panel-qml-test:
	@command -v $(QMLTESTRUNNER) >/dev/null 2>&1 || { \
		echo "panel-qml-test: qmltestrunner not found on PATH (set QMLTESTRUNNER=/path/to/qmltestrunner, or install qt6-declarative-dev-tools / your distro's Qt6 QML test tools)"; \
		exit 1; \
	}
	QT_QPA_PLATFORM=offscreen QML_XHR_ALLOW_FILE_READ=1 QT_FATAL_WARNINGS=1 \
		$(QMLTESTRUNNER) -import panel/qmltest/stubs -input panel/qmltest/tests

check: fmt-check vet lint test panel-qml-test

# release-check validates a version is ready to tag: semver, a CHANGELOG.md
# section, and a matching ops/aur/PKGBUILD pkgver. It builds nothing and
# publishes nothing; see RELEASING.md for what still needs Brian's approval.
release-check:
	VERSION=$(VERSION) ./ops/release-check.sh

clean:
	rm -rf bin
