#!/usr/bin/env bash
# Builds release archives for all platforms into dist/.
# usage: .github/build.sh VERSION      (e.g. v0.1.0; CI uses the commit for test builds)
set -euo pipefail
version=${1:?version}
rm -rf dist && mkdir -p dist
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os=${target%/*} arch=${target#*/}
  name=zotero-backup-${version}-${os}-${arch}
  exe=zotero-backup; [ "$os" = windows ] && exe=zotero-backup.exe
  mkdir -p "dist/$name"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$version" -o "dist/$name/$exe" .
  cp README.md LICENSE "dist/$name/"
  if [ "$os" = windows ]; then
    (cd dist && zip -qr "$name.zip" "$name")
  else
    tar -C dist -czf "dist/$name.tar.gz" "$name"
  fi
  rm -rf "dist/$name"
done
(cd dist && shasum -a 256 *.tar.gz *.zip > SHA256SUMS)
ls -l dist
