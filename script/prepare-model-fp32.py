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
    '        module=model.head.joint,': (
        '        module=model.head.joint,\n'
        '        inputs=model.head.joint.input_example(batch_size=1),'
    ),
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
print('Prepared FP32 exporter: quantization removed, CPU float32, decoder and joiner batch size 1.')
