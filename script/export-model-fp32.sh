#!/bin/sh
# Export GigaAM into the directory copied to the runtime image.
set -eu
script_dir=$(cd "$(dirname "$0")" && pwd)
destination=${1:-/model/gigaam-v3-fp32}
export GIGAAM_EXPORT_CHECKPOINTS=${GIGAAM_EXPORT_CHECKPOINTS:-/checkpoints}

mkdir -p "$destination" "$GIGAAM_EXPORT_CHECKPOINTS"
cd "$destination"
rm -f export.version

python3 "$script_dir/export-model-fp32.py"
python3 "$script_dir/validate-model-fp32.py"
cp "$script_dir/licenses/GigaAM.LICENSE" LICENSE
printf '2\n' > export.version
chmod 755 .
chmod 644 ./*
echo "GigaAM FP32 ready: $destination"
