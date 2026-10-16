#!/bin/sh
# Build-stage only: export validated FP32 ONNX files for the runtime image.
set -eu
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
curl --fail --location --retry 3 "https://raw.githubusercontent.com/k2-fsa/sherpa-onnx/$sherpa_revision/scripts/nemo/GigaAM/export-onnx-rnnt-v3-punct.py" -o "$temporary/upstream.py"
printf '%s  %s\n' 151cc4e11807116a5dd11673a68b80c02536204f2be1e5c269c34dddd9b04039 "$temporary/upstream.py" | sha256sum --check -
# Pinned upstream source is adapted without executing it during preparation.
python3 /opt/model-export/prepare-model-fp32.py "$temporary/upstream.py" "$temporary/export.py"
if [ "${EXPORT_PREPARE_ONLY:-0}" = 1 ]; then
  echo 'Preparation checked; weights were not downloaded or converted.'
  exit 0
fi
export GIGAAM_EXPORT_CHECKPOINTS=${GIGAAM_EXPORT_CHECKPOINTS:-/checkpoints}
mkdir -p "$GIGAAM_EXPORT_CHECKPOINTS"
(
  cd "$temporary"
  python3 export.py
  python3 /opt/model-export/validate-model-fp32.py
)
mkdir -p "$destination"
# Publish the version marker last so failed exports can be retried.
rm -f "$destination/export.version"
for file in encoder.onnx decoder.onnx joiner.onnx tokens.txt precision.json; do
  cp "$temporary/$file" "$destination/$file"
done
curl --fail --location --retry 3 "https://raw.githubusercontent.com/salute-developers/GigaAM/$gigaam_revision/LICENSE" -o "$destination/LICENSE"
chmod 755 "$destination"
chmod 644 "$destination/"*
printf '2\n' > "$destination/export.version"
chmod 644 "$destination/export.version"
echo "Unquantized GigaAM v3 E2E RNNT installed in $destination"
