import json
import numpy as np
import onnxruntime as ort
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
# sherpa-onnx's greedy RNNT decoder always passes one encoder/decoder frame.
# Run the joiner with those exact shapes to catch fixed-batch exports (e.g. 8).
options = ort.SessionOptions()
options.intra_op_num_threads = 1
options.inter_op_num_threads = 1
session = ort.InferenceSession('joiner.onnx', sess_options=options,
                              providers=['CPUExecutionProvider'])
expected_shapes = {'enc': [1, 768, 1], 'dec': [1, 320, 1]}
inputs = {item.name: item for item in session.get_inputs()}
if inputs.keys() != expected_shapes.keys():
    raise RuntimeError(f'joiner.onnx: unexpected inputs: {list(inputs)}')
for name, shape in expected_shapes.items():
    if inputs[name].shape != shape or inputs[name].type != 'tensor(float)':
        raise RuntimeError(
            f'joiner.onnx: {name} must be FP32 {shape}, got '
            f'{inputs[name].type} {inputs[name].shape}; re-export the model'
        )
outputs = session.run(None, {
    name: np.zeros(shape, dtype=np.float32)
    for name, shape in expected_shapes.items()
})
if len(outputs) != 1 or outputs[0].shape != (1, 1, 1, 1025) or not np.isfinite(outputs[0]).all():
    raise RuntimeError('joiner.onnx: invalid output for batch-1 RNNT inputs')
print('joiner.onnx: batch-1 inputs and inference checked')
metadata = {
    'model': 'v3_e2e_rnnt',
    'precision': 'fp32',
    'quantized': False,
    'export_version': 2,
    'joiner_batch_size': 1,
    'gigaam_revision': '7447938d791c4f3e643386ee22c33777004293a5',
}
pathlib.Path('precision.json').write_text(json.dumps(metadata, indent=2) + '\n')
