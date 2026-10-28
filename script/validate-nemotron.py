"""Validate the pinned SDK's --outtype f32 profile before baking the model."""
from collections import Counter
import hashlib
import json
import sys
from pathlib import Path

import gguf

# conversion/diarization.py::_pick_dtype in the pinned official SDK emits this
# Conv1D kernel as F16 even with --outtype f32: ggml's CPU im2col requires F16.
# Do not permit reduced precision for any other tensor (including its bias).
F16_CONV_KERNEL = 'subpixel_upsample.weight'


def validate(path, sdk_revision, model_revision):
    path = Path(path)
    reader = gguf.GGUFReader(str(path))
    if not reader.tensors:
        raise ValueError('Nemotron GGUF contains no tensors')

    def field(name):
        return reader.fields[name].contents()

    if field('general.architecture') != 'sortformer' or int(field('sortformer.num_speakers')) != 8:
        raise ValueError('expected Nemotron-3 Sortformer with eight speaker channels')

    types = Counter()
    f16_tensors, invalid = [], []
    for tensor in reader.tensors:
        dtype = gguf.GGMLQuantizationType(tensor.tensor_type)
        types[dtype.name] += 1
        if dtype == gguf.GGMLQuantizationType.F32:
            continue
        if tensor.name == F16_CONV_KERNEL and dtype == gguf.GGMLQuantizationType.F16:
            f16_tensors.append(tensor.name)
        else:
            invalid.append(f'{tensor.name}={dtype.name}')
    if invalid:
        raise ValueError(
            f'Nemotron requires F32 except the SDK Conv1D kernel {F16_CONV_KERNEL} (F16); '
            f'unexpected tensor types: {", ".join(invalid)}'
        )

    with path.open('rb') as source:
        digest = hashlib.file_digest(source, 'sha256').hexdigest()
    manifest = dict(
        model='nvidia/Nemotron-3-Diarization', model_revision=model_revision,
        sdk_revision=sdk_revision, conversion_outtype='f32',
        precision='f32_with_f16_conv' if f16_tensors else 'f32',
        tensor_types=dict(sorted(types.items())), f16_tensors=sorted(f16_tensors),
        sha256=digest, bytes=path.stat().st_size,
    )
    path.with_name('manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    print(f'[validate] Nemotron tensor types: {manifest["tensor_types"]}; F16 kernels: {f16_tensors}')
    return manifest


if __name__ == '__main__':
    try:
        validate(sys.argv[1], sys.argv[2], sys.argv[3])
    except (KeyError, ValueError) as error:
        raise SystemExit(str(error)) from error
