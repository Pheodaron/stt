#!/bin/sh
# Build-time conversion only. Failure must prevent creation of a model-free image.
set -eu
. /opt/stt/nemo-version.env
model_dir=/model/nemotron-3-diarization-f32
mkdir -p "$model_dir"
cd /opt/nemo-source
python3 convert_model.py nvidia/Nemotron-3-Diarization \
  --architecture diarization --revision "$NEMOTRON_REVISION" \
  --cache-dir /checkpoints/nemotron --outtype f32 --outfile "$model_dir/model.gguf"
python3 /opt/stt/validate-nemotron.py "$model_dir/model.gguf" "$NEMO_REVISION" "$NEMOTRON_REVISION"
# The pinned model repository declares OpenMDW-1.1 but has no LICENSE file.
cp /opt/stt/MODEL-LICENSE.OpenMDW-1.1 "$model_dir/LICENSE"
# Preserve the original model card, including notices and usage limitations.
python3 - "$model_dir" <<'PY'
import shutil, sys
from huggingface_hub import hf_hub_download
from pathlib import Path
scope = {}
for line in Path('/opt/stt/nemo-version.env').read_text().splitlines():
    if line and not line.startswith('#'):
        key, value = line.split('=', 1)
        scope[key] = value
card_path = hf_hub_download('nvidia/Nemotron-3-Diarization', 'README.md',
    revision=scope['NEMOTRON_REVISION'], cache_dir='/checkpoints/nemotron')
shutil.copyfile(card_path, Path(sys.argv[1]) / 'MODEL_CARD.md')
PY
chmod 755 "$model_dir"
chmod 644 "$model_dir/"*
