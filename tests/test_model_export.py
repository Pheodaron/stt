"""Run inside the model-tools build target; no checkpoints or network are needed."""
import ast
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest

import numpy as np
import onnx
import onnxruntime as ort
import torch
from gigaam.decoder import RNNTJoint
from gigaam.utils import onnx_converter


ROOT = Path(__file__).resolve().parents[1]
UPSTREAM = ROOT / 'tests/fixtures/export-onnx-rnnt-v3-punct.py'


class ModelExportTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        previous = Path.cwd()
        os.chdir(self.temporary.name)
        self.addCleanup(os.chdir, previous)
        # Small stand-ins for the unchanged encoder/decoder precision checks.
        for name in ['encoder.onnx', 'decoder.onnx']:
            info = onnx.helper.make_tensor_value_info
            graph = onnx.helper.make_graph(
                [onnx.helper.make_node('Identity', ['x'], ['y'])], 'placeholder',
                [info('x', onnx.TensorProto.FLOAT, [1])],
                [info('y', onnx.TensorProto.FLOAT, [1])],
            )
            onnx.save(onnx.helper.make_model(graph), name)

    def export_joiner(self, source):
        # Exercise the actual joiner call in the upstream/adapted main(),
        # using the real RNNTJoint with random weights instead of a checkpoint.
        tree = ast.parse(source)
        main = next(node for node in tree.body
                    if isinstance(node, ast.FunctionDef) and node.name == 'main')
        calls = [node for node in ast.walk(main)
                 if isinstance(node, ast.Call)
                 and isinstance(node.func, ast.Name)
                 and node.func.id == 'onnx_converter']
        self.assertEqual(len(calls), 1)
        torch.manual_seed(0)
        joint = RNNTJoint(enc_hidden=768, pred_hidden=320,
                          joint_hidden=320, num_classes=1025).eval()
        model = SimpleNamespace(head=SimpleNamespace(joint=joint),
                                cfg=SimpleNamespace(model_name='v3_e2e_rnnt'))
        eval(compile(ast.Expression(calls[0]), '<joiner-export>', 'eval'),
             {'model': model, 'onnx_converter': onnx_converter})
        Path('v3_e2e_rnnt_joint.onnx').rename('joiner.onnx')
        return joint

    def validate(self):
        return subprocess.run(
            [sys.executable, str(ROOT / 'script/validate-model-fp32.py')],
            capture_output=True, text=True,
        )

    def test_batch_one_export_runs_and_matches_pytorch(self):
        self.assertEqual(hashlib.sha256(UPSTREAM.read_bytes()).hexdigest(),
                         '151cc4e11807116a5dd11673a68b80c02536204f2be1e5c269c34dddd9b04039')
        subprocess.run([sys.executable, str(ROOT / 'script/prepare-model-fp32.py'),
                        str(UPSTREAM), 'export.py'], check=True)
        joint = self.export_joiner(Path('export.py').read_text())
        session = ort.InferenceSession('joiner.onnx', providers=['CPUExecutionProvider'])
        self.assertEqual([item.shape for item in session.get_inputs()],
                         [[1, 768, 1], [1, 320, 1]])
        enc, dec = joint.input_example(batch_size=1)
        with torch.no_grad():
            expected = joint(enc, dec).numpy()
        actual = session.run(None, {'enc': enc.numpy(), 'dec': dec.numpy()})[0]
        np.testing.assert_allclose(actual, expected, rtol=1e-5, atol=1e-6)
        result = self.validate()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        metadata = json.loads(Path('precision.json').read_text())
        self.assertEqual(metadata['export_version'], 2)
        self.assertEqual(metadata['joiner_batch_size'], 1)

    def test_original_batch_eight_reproduces_failure_and_is_rejected(self):
        self.export_joiner(UPSTREAM.read_text())
        session = ort.InferenceSession('joiner.onnx', providers=['CPUExecutionProvider'])
        with self.assertRaisesRegex(ort.capi.onnxruntime_pybind11_state.InvalidArgument,
                                    'Got: 1 Expected: 8'):
            session.run(None, {'enc': np.zeros((1, 768, 1), np.float32),
                               'dec': np.zeros((1, 320, 1), np.float32)})
        result = self.validate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('enc must be FP32 [1, 768, 1]', result.stderr)
        self.assertFalse(Path('precision.json').exists())

    def test_changed_upstream_is_rejected(self):
        Path('changed.py').write_text(UPSTREAM.read_text().replace(
            '        module=model.head.joint,', '        module=model.head.other,'))
        result = subprocess.run(
            [sys.executable, str(ROOT / 'script/prepare-model-fp32.py'),
             'changed.py', 'export.py'], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Pinned exporter does not match expected source', result.stderr)


if __name__ == '__main__':
    unittest.main()
