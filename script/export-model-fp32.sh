#!/bin/sh
# Build-stage only: export validated FP32 ONNX files for the runtime image.
set -eu
script_dir=$(cd "$(dirname "$0")" && pwd)
destination=${1:-/model/gigaam-v3-fp32}
complete=1
for file in encoder.onnx decoder.onnx joiner.onnx tokens.txt precision.json LICENSE export.version; do
  if [ ! -s "$destination/$file" ]; then
    complete=0
    break
  fi
done
if [ "$complete" = 1 ]; then
  IFS= read -r export_version < "$destination/export.version" || export_version=
  [ "$export_version" = 2 ] || complete=0
fi
if [ "$complete" = 1 ]; then
  echo "FP32 model already exists in $destination"
  exit 0
fi
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT HUP INT TERM
sherpa_revision=fa29eb91c80335fc70af869a7bb459578f90e5ad
gigaam_revision=7447938d791c4f3e643386ee22c33777004293a5
# Fetch a single file through GitHub's Git transport. A partial, shallow fetch
# keeps the checkout small and avoids requests to raw.githubusercontent.com.
fetch_pinned_file() (
  source_repo=$1
  source_revision=$2
  source_file=$3
  checkout_dir=$4
  output_file=$5
  git init --quiet "$checkout_dir"
  git -C "$checkout_dir" remote add origin "$source_repo"
  git -C "$checkout_dir" config remote.origin.promisor true
  git -C "$checkout_dir" config remote.origin.partialclonefilter blob:none
  git -C "$checkout_dir" fetch --quiet --depth 1 --filter=blob:none origin "$source_revision"
  if [ "$(git -C "$checkout_dir" rev-parse FETCH_HEAD)" != "$source_revision" ]; then
    echo "Source revision mismatch: $source_repo" >&2
    exit 1
  fi
  git -C "$checkout_dir" sparse-checkout set --no-cone "/$source_file"
  git -C "$checkout_dir" -c advice.detachedHead=false checkout --quiet --detach FETCH_HEAD
  cp "$checkout_dir/$source_file" "$output_file"
)
fetch_pinned_file https://github.com/k2-fsa/sherpa-onnx.git "$sherpa_revision" \
  scripts/nemo/GigaAM/export-onnx-rnnt-v3-punct.py "$temporary/sherpa-source" "$temporary/upstream.py"
printf '%s  %s\n' 151cc4e11807116a5dd11673a68b80c02536204f2be1e5c269c34dddd9b04039 "$temporary/upstream.py" | sha256sum --check -
# Fetch the license before expensive model conversion so network failures fail
# during preparation, not after a successful ONNX export.
fetch_pinned_file https://github.com/salute-developers/GigaAM.git "$gigaam_revision" \
  LICENSE "$temporary/gigaam-source" "$temporary/LICENSE"
# Pinned upstream source is adapted without executing it during preparation.
python3 "$script_dir/prepare-model-fp32.py" "$temporary/upstream.py" "$temporary/export.py"
if [ "${EXPORT_PREPARE_ONLY:-0}" = 1 ]; then
  echo 'Preparation checked; weights were not downloaded or converted.'
  exit 0
fi
export GIGAAM_EXPORT_CHECKPOINTS=${GIGAAM_EXPORT_CHECKPOINTS:-/checkpoints}
mkdir -p "$GIGAAM_EXPORT_CHECKPOINTS"
(
  cd "$temporary"
  python3 export.py
  python3 "$script_dir/validate-model-fp32.py"
)
mkdir -p "$destination"
# Publish the version marker last so failed exports can be retried.
rm -f "$destination/export.version"
for file in encoder.onnx decoder.onnx joiner.onnx tokens.txt precision.json; do
  cp "$temporary/$file" "$destination/$file"
done
cp "$temporary/LICENSE" "$destination/LICENSE"
chmod 755 "$destination"
chmod 644 "$destination/"*
printf '2\n' > "$destination/export.version"
chmod 644 "$destination/export.version"
echo "Unquantized GigaAM v3 E2E RNNT installed in $destination"
