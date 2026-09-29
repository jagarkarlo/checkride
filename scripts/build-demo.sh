#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

if [[ ! -d studio/node_modules ]]; then
  npm ci --prefix studio --registry=https://registry.npmjs.org
fi

npm run build --prefix studio -- --base=/demo/
mkdir -p site/public/demo
cp -R studio/dist/. site/public/demo/
GOOS=js GOARCH=wasm go build -o site/public/demo/checkride-browser.wasm ./cmd/checkride-browser
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" site/public/demo/wasm_exec.js