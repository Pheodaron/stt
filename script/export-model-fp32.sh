#!/bin/sh
# One-time model conversion only. The Go server does not use Python.
set -eu
cd "$(dirname "$0")/.."
project_dir=$(pwd)
destination=${1:-models/gigaam-v3-fp32}
case "$destination" in
  /*) ;;
  *) destination="$project_dir/$destination" ;;
esac
complete=1
for file in encoder.onnx decoder.onnx joiner.onnx tokens.txt precision.json LICENSE; do
  if [ ! -s "$destination/$file" ]; then
    complete=0
    break
  fi
done
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
python3 - "$temporary/upstream.py" "$temporary/export.py" <<'PY'
import ast
import pathlib
import sys

source = pathlib.Path(sys.argv[1]).read_text()
replacements = {
    'from onnxruntime.quantization import QuantType, quantize_dynamic\n': '',
    'model = gigaam.load_model(model_name)': (
        'model = gigaam.load_model('
        'model_name, device="cpu", fp16_encoder=False, '
        'download_root=os.environ["GIGAAM_EXPORT_CHECKPOINTS"]'
        ').float().eval()'
    ),
    'self.m.head.decoder.input_example()': 'self.m.head.decoder.input_example(batch_size=1)',
    '    EncoderWrapper(model).to_onnx(".")': '    with model.encoder.onnx_export_mode():\n        EncoderWrapper(model).to_onnx(".")',
    '''    quantize_dynamic(
        model_input=f"./{model_name}_encoder.onnx",
        model_output="./encoder.int8.onnx",
        weight_type=QuantType.QUInt8,
    )''': '',
    'os.remove(f"./{model_name}_encoder.onnx")': 'os.rename(f"./{model_name}_encoder.onnx", "encoder.onnx")',
}
for old, new in replacements.items():
    if source.count(old) != 1:
        raise RuntimeError(f"Pinned exporter does not match expected source: {old!r}")
    source = source.replace(old, new)
tree = ast.parse(source)
assert not any(isinstance(node, ast.Name) and node.id == 'quantize_dynamic' for node in ast.walk(tree))
assert 'fp16_encoder=False' in source and 'device="cpu"' in source
pathlib.Path(sys.argv[2]).write_text(source)
print('Prepared FP32 exporter: quantization removed, CPU float32, decoder batch size 1.')
PY
if [ "${EXPORT_PREPARE_ONLY:-0}" = 1 ]; then
  echo 'Preparation checked; weights were not downloaded or converted.'
  exit 0
fi
tool_dir="$project_dir/.tools/gigaam-export"
if [ ! -x "$tool_dir/bin/python" ]; then
  python3 -m venv "$tool_dir"
fi
"$tool_dir/bin/python" -m pip install 'torch==2.8.0' 'torchaudio==2.8.0' --index-url https://download.pytorch.org/whl/cpu
"$tool_dir/bin/python" -m pip install "gigaam @ git+https://github.com/salute-developers/GigaAM.git@$gigaam_revision"
export GIGAAM_EXPORT_CHECKPOINTS=${GIGAAM_CACHE_DIR:-"$project_dir/.tools/gigaam-checkpoints"}
mkdir -p "$GIGAAM_EXPORT_CHECKPOINTS"
(
  cd "$temporary"
  "$tool_dir/bin/python" export.py
  "$tool_dir/bin/python" - <<'PY'
import json
import onnx
import pathlib

quantized = {
    'DynamicQuantizeLinear', 'QuantizeLinear', 'DequantizeLinear',
    'DeQuantizeLinear', 'MatMulInteger', 'ConvInteger',
    'QLinearMatMul', 'QLinearConv',
}
for name in ['encoder.onnx', 'decoder.onnx', 'joiner.onnx']:
    model = onnx.load(name)
    bad_nodes = sorted({
        node.op_type for node in model.graph.node
        if node.op_type in quantized or 'quant' in node.op_type.lower()
    })
    low_precision = [
        tensor.name for tensor in model.graph.initializer
        if tensor.data_type in (onnx.TensorProto.FLOAT16, onnx.TensorProto.BFLOAT16)
    ]
    if bad_nodes or low_precision:
        raise RuntimeError(f'{name}: quantized or reduced precision tensors found: {bad_nodes}, {low_precision[:3]}')
    print(f'{name}: no quantized operators or FP16/BF16 initializers')
    del model
metadata = {
    'model': 'v3_e2e_rnnt',
    'precision': 'fp32',
    'quantized': False,
    'gigaam_revision': '7447938d791c4f3e643386ee22c33777004293a5',
}
pathlib.Path('precision.json').write_text(json.dumps(metadata, indent=2) + '\n')
PY
)
mkdir -p "$destination"
for file in encoder.onnx decoder.onnx joiner.onnx tokens.txt precision.json; do
  cp "$temporary/$file" "$destination/$file"
done
curl --fail --location --retry 3 "https://raw.githubusercontent.com/salute-developers/GigaAM/$gigaam_revision/LICENSE" -o "$destination/LICENSE"
chmod 755 models "$destination"
chmod 644 "$destination/"*
echo "Unquantized GigaAM v3 E2E RNNT installed in $destination"
