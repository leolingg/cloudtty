#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail
set -ex

# For all commands, the working directory is the parent directory(repo root).
REPO_ROOT=$(git rev-parse --show-toplevel)
cd "${REPO_ROOT}"

echo "Generating deepcopy with controller-gen"
# zz_generated.deepcopy.go follows controller-gen's output (same as
# `make generate`, which `make build` runs on every build). deepcopy-gen
# produces a different header and would only add churn, so it is not used.
CONTROLLER_GEN=${CONTROLLER_GEN:-$(go env GOPATH)/bin/controller-gen}
[ -x "${CONTROLLER_GEN}" ] || GOBIN=$(go env GOPATH)/bin go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.8.0
"${CONTROLLER_GEN}" object:headerFile="hack/boilerplate.go.txt" paths="./..."

# The generators below resolve the output location via GOPATH by default, which only
# works when the repo is checked out at $GOPATH/src/github.com/cloudtty/cloudtty.
# Pin the output to the repo root instead, so codegen works in module mode
# regardless of where the repo is checked out.
MODULE_PATH=github.com/cloudtty/cloudtty

export GOPATH=$(go env GOPATH | awk -F ':' '{print $1}')
export PATH=$PATH:$GOPATH/bin

# NOTE: register-gen is intentionally not used: pkg/apis/cloudshell/v1alpha1/register.go
# is maintained by hand, it additionally registers addDefaultingFuncs which the
# generated zz_generated.register.go does not cover.

echo "Generating with client-gen"
GO111MODULE=on go install k8s.io/code-generator/cmd/client-gen
client-gen \
  --go-header-file hack/boilerplate.go.txt \
  --input-base="" \
  --input=github.com/cloudtty/cloudtty/pkg/apis/cloudshell/v1alpha1 \
  --output-package=github.com/cloudtty/cloudtty/pkg/generated/clientset \
  --clientset-name=versioned \
  --output-base=./ \
  --trim-path-prefix="${MODULE_PATH}" \

echo "Generating with lister-gen"
GO111MODULE=on go install k8s.io/code-generator/cmd/lister-gen
lister-gen \
  --go-header-file hack/boilerplate.go.txt \
  --input-dirs=github.com/cloudtty/cloudtty/pkg/apis/cloudshell/v1alpha1 \
  --output-package=github.com/cloudtty/cloudtty/pkg/generated/listers \
  --output-base=./ \
  --trim-path-prefix="${MODULE_PATH}" \

echo "Generating with informer-gen"
GO111MODULE=on go install k8s.io/code-generator/cmd/informer-gen
informer-gen \
  --go-header-file hack/boilerplate.go.txt \
  --input-dirs=github.com/cloudtty/cloudtty/pkg/apis/cloudshell/v1alpha1 \
  --versioned-clientset-package=github.com/cloudtty/cloudtty/pkg/generated/clientset/versioned \
  --listers-package=github.com/cloudtty/cloudtty/pkg/generated/listers \
  --output-package=github.com/cloudtty/cloudtty/pkg/generated/informers \
  --output-base=./ \
  --trim-path-prefix="${MODULE_PATH}" \
