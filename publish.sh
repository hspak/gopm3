#!/bin/bash

set -e

VERSION="$1"

if [[ -z "$VERSION" ]]; then
  echo "Need a version"
  exit 1
fi

GOOS_LIST="linux darwin"
GOARCH_LIST="amd64 arm64"

rm -rf npm/bin/*
for goos in $GOOS_LIST; do
  for goarch in $GOARCH_LIST; do
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -ldflags="-s -w -X main.Version=$VERSION" -o "npm/bin/gen-gopm3-${goos}-${goarch}"
  done
done

cat << EOF > npm/package.json
{
  "name": "@hspak/gopm3",
  "version": "$VERSION",
  "description": "A dumb process manager",
  "main": "src/index.js",
  "scripts": {
    "postinstall": "node install.js"
  },
  "bin": {
    "gopm3": "src/index.js"
  },
  "repository": {
    "type": "git",
    "url": "git+https://github.com/hspak/gopm3.git"
  },
  "author": "Hong Shick Pak",
  "license": "MIT",
  "bugs": {
    "url": "https://github.com/hspak/gopm3/issues"
  },
  "homepage": "https://github.com/hspak/gopm3#readme"
}
EOF

git add npm/package.json
git commit -m 'New version'
git tag "v${VERSION}" -f
git push --follow-tags
pushd npm
npm publish --access public .
popd
