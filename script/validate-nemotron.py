"""Validate the exact CPU baseline before baking it into the runtime image."""
import hashlib
import json
import sys
from pathlib import Path

import gguf

path = Path(sys.argv[1])
reader = gguf.GGUFReader(str(path))
if not reader.tensors or any(t.tensor_type != gguf.GGMLQuantizationType.F32 for t in reader.tensors):
    raise SystemExit('Nemotron must contain only F32 tensors')

def field(name):
    f = reader.fields[name]
    return f.contents()

if field('general.architecture') != 'sortformer' or int(field('sortformer.num_speakers')) != 8:
    raise SystemExit('expected Nemotron-3 Sortformer with eight speaker channels')
digest = hashlib.file_digest(path.open('rb'), 'sha256').hexdigest()
manifest = dict(model='nvidia/Nemotron-3-Diarization', model_revision=sys.argv[3],
    sdk_revision=sys.argv[2], precision='f32', sha256=digest, bytes=path.stat().st_size)
path.with_name('manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
