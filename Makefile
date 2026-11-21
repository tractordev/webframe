NAME			?= webframe
VERSION 		?= 0.2-dev
GOARGS			?=
GOOS			?= $(shell go env GOOS)
GOARCH			?= $(shell go env GOARCH)
DIST_DIR		?= dist
DIST_OS			?= darwin windows linux linux-musl
DIST_ARCH		?= arm64 amd64
LDFLAGS			:= -s -w -X main.Version=$(VERSION)
# macOS signing, for `make dist` on a Mac. With SIGN_IDENTITY empty, the
# darwin binaries are left as Go links them (ad hoc signed on arm64, unsigned
# on amd64), which Gatekeeper blocks for browser downloads. A "Developer ID
# Application: ..." identity signs them for distribution, and NOTARY_PROFILE
# (a keychain profile saved with `xcrun notarytool store-credentials`) has
# `make dist` notarize the darwin zips.
SIGN_IDENTITY	?=
NOTARY_PROFILE	?=

# No cgo: glaze binds the system web view at runtime via purego, so binaries
# cross-compile anywhere and need no C toolchain or bundled libraries.
export CGO_ENABLED := 0

# Except linux-musl (Alpine): purego without cgo only works with glibc, so it
# is built with cgo against musl, dynamically linked so it can load WebKitGTK.
# Cross-compiled with zig.
MUSL_ARCH_arm64	:= aarch64
MUSL_ARCH_amd64	:= x86_64
musl_env = CGO_ENABLED=1 CC="zig cc -target $(MUSL_ARCH_$(1))-linux-musl -dynamic"
MUSL_LDFLAGS	:= -linkmode=external -extldflags=-dynamic

## Build binary (OS=linux-musl for the musl build)
OS				?= $(GOOS)
MUSL			= $(filter linux-musl,$(OS))
build:
	GOOS=$(if $(MUSL),linux,$(OS)) GOARCH=$(GOARCH) $(if $(MUSL),$(call musl_env,$(GOARCH))) \
	go build -trimpath -ldflags="$(LDFLAGS)$(if $(MUSL), $(MUSL_LDFLAGS))" $(GOARGS) ./cmd/webframe
.PHONY: build

## Clean distribution directory
clean:
	rm -rf $(DIST_DIR)
.PHONY: clean

## Serve examples directory
examples:
	python3 -m http.server --directory examples
.PHONY: examples

DIST_TARGETS	:= $(foreach os, $(DIST_OS), $(foreach arch, $(DIST_ARCH), $(DIST_DIR)/$(NAME)_v$(VERSION)_$(os)_$(arch).zip))

## Build distribution zips
dist: $(DIST_TARGETS)
.PHONY: dist

# A zip that failed notarization isn't left behind looking finished.
.DELETE_ON_ERROR:

$(DIST_TARGETS): $(DIST_DIR)/%.zip:
	$(eval DIST_OS_ := $(word 3, $(subst _, ,$*)))
	$(eval DIST_BIN := $(DIST_DIR)/$*/$(NAME)$(if $(filter windows,$(DIST_OS_)),.exe))
ifneq ($(NOTARY_PROFILE),)
	@test "$(DIST_OS_)" != darwin || test -n "$(SIGN_IDENTITY)" || { echo "NOTARY_PROFILE needs SIGN_IDENTITY (a Developer ID identity)" >&2; exit 1; }
endif
	$(MAKE) build OS=$(DIST_OS_) GOARCH=$(word 4, $(subst _, ,$*)) GOARGS="$(GOARGS) -o $(DIST_BIN)"
ifneq ($(SIGN_IDENTITY),)
	@if [ "$(DIST_OS_)" = darwin ]; then \
		echo "signing $(DIST_BIN)"; \
		codesign --force --sign "$(SIGN_IDENTITY)" --options runtime --timestamp $(DIST_BIN) || exit 1; \
		codesign --verify --strict --verbose=2 $(DIST_BIN) || exit 1; \
	fi
endif
	rm -f $@
	zip -j $@ $(DIST_BIN)
	rm -rf $(DIST_DIR)/$*
ifneq ($(NOTARY_PROFILE),)
	@if [ "$(DIST_OS_)" = darwin ]; then \
		echo "notarizing $@"; \
		xcrun notarytool submit $@ --keychain-profile "$(NOTARY_PROFILE)" --wait > $@.notary.log 2>&1; \
		cat $@.notary.log; \
		grep -q 'status: Accepted' $@.notary.log || { echo "notarization failed; see xcrun notarytool log" >&2; exit 1; }; \
		rm -f $@.notary.log; \
	fi
endif
