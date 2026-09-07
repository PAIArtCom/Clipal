#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR=${CLIPAL_ROOT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
MODEL_SPECS_REPO=${MODEL_SPECS_REPO:-https://github.com/PAIArtCom/model-specs.git}
MODEL_SPECS_REF=${MODEL_SPECS_REF:-}
if [[ ! "$MODEL_SPECS_REF" =~ ^[0-9a-fA-F]{40}$ ]]; then
    echo "MODEL_SPECS_REF must be an explicitly reviewed 40-character commit SHA" >&2
    exit 1
fi
TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/clipal-model-specs.XXXXXX")

cleanup() {
    rm -rf "$TEMP_DIR"
}
trap cleanup EXIT

git init -q "$TEMP_DIR/model-specs"
git -C "$TEMP_DIR/model-specs" remote add origin "$MODEL_SPECS_REPO"
git -C "$TEMP_DIR/model-specs" fetch --depth 1 origin "$MODEL_SPECS_REF"
FETCHED_OBJECT=$(git -C "$TEMP_DIR/model-specs" rev-parse FETCH_HEAD)
NORMALIZED_MODEL_SPECS_REF=$(printf '%s' "$MODEL_SPECS_REF" | tr '[:upper:]' '[:lower:]')
if [[ "$FETCHED_OBJECT" != "$NORMALIZED_MODEL_SPECS_REF" ]]; then
    echo "fetched model-specs commit does not match MODEL_SPECS_REF" >&2
    exit 1
fi
if [[ "$(git -C "$TEMP_DIR/model-specs" cat-file -t "$FETCHED_OBJECT")" != "commit" ]]; then
    echo "MODEL_SPECS_REF must identify a commit object" >&2
    exit 1
fi
MODEL_SPECS_COMMIT=$(git -C "$TEMP_DIR/model-specs" rev-parse "$FETCHED_OBJECT^{commit}")
mkdir -p "$TEMP_DIR/artifacts/dist"
git -C "$TEMP_DIR/model-specs" show "$MODEL_SPECS_COMMIT:dist/catalog.json" > "$TEMP_DIR/artifacts/dist/catalog.json"
git -C "$TEMP_DIR/model-specs" show "$MODEL_SPECS_COMMIT:dist/catalog.sha" > "$TEMP_DIR/artifacts/dist/catalog.sha"
git -C "$TEMP_DIR/model-specs" show "$MODEL_SPECS_COMMIT:LICENSE" > "$TEMP_DIR/artifacts/LICENSE"
CATALOG_PATH="$TEMP_DIR/artifacts/dist/catalog.json"

CATALOG_INFO=$(node -e 'const fs=require("fs"),crypto=require("crypto");const catalogPath=process.argv[1],shaPath=process.argv[2],c=JSON.parse(fs.readFileSync(catalogPath,"utf8")),sha=fs.readFileSync(shaPath,"utf8");const object=v=>v!==null&&typeof v==="object"&&!Array.isArray(v)&&Object.keys(v).length>0;const nonempty=v=>typeof v==="string"&&v.trim()!=="";if(!/^[0-9a-f]{12}\n$/.test(sha)||!object(c)||!nonempty(c.generated_at)||!object(c.upstream)||!nonempty(c.upstream.source)||!nonempty(c.upstream.sha)||!object(c.models)||!object(c.clients)||!Object.values(c.models).every(object)||!Object.values(c.clients).every(object)||typeof c.version!=="string"||!/^[0-9a-f]{12}$/.test(c.version))process.exit(2);const expected=sha.slice(0,-1),v=crypto.createHash("sha256").update(JSON.stringify({models:c.models,clients:c.clients})).digest("hex").slice(0,12);if(c.version!==expected||v!==expected)process.exit(3);process.stdout.write(v+" "+Object.keys(c.models).length)' "$CATALOG_PATH" "$TEMP_DIR/artifacts/dist/catalog.sha")
read -r JSON_VERSION MODEL_COUNT <<< "$CATALOG_INFO"
CATALOG_VERSION="$JSON_VERSION"
if [[ -z "$CATALOG_VERSION" ]]; then
    echo "model-specs catalog version validation failed" >&2
	exit 1
fi

CATALOG_INTEGRITY=$(node -e 'const fs=require("fs"),crypto=require("crypto");process.stdout.write(crypto.createHash("sha256").update(fs.readFileSync(process.argv[1])).digest("hex"))' "$CATALOG_PATH")
if [[ ! -s "$TEMP_DIR/artifacts/LICENSE" ]]; then
    echo "model-specs LICENSE must not be empty" >&2
    exit 1
fi

install -m 0644 "$CATALOG_PATH" "$ROOT_DIR/internal/pricing/catalog.json"
printf '%s\n' "$CATALOG_VERSION" > "$ROOT_DIR/internal/pricing/catalog.sha"
install -m 0644 "$TEMP_DIR/artifacts/LICENSE" "$ROOT_DIR/internal/pricing/model-specs.LICENSE"
printf '%s\n' "$MODEL_SPECS_COMMIT" > "$ROOT_DIR/internal/pricing/catalog.commit"
printf '%s\n' "$CATALOG_INTEGRITY" > "$ROOT_DIR/internal/pricing/catalog.integrity.sha256"

echo "Updated model-specs catalog to $CATALOG_VERSION ($MODEL_SPECS_COMMIT, $MODEL_COUNT models)"
