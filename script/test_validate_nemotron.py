"""Run in the nemotron-tools stage; no checkpoints or network are needed."""
import hashlib
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest

import gguf
import numpy as np


spec = importlib.util.spec_from_file_location(
    'validate_nemotron', Path(__file__).with_name('validate-nemotron.py')
)
validator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(validator)


class ValidateNemotronTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.path = Path(directory.name) / 'model.gguf'

    def write_model(self, tensors, architecture='sortformer', speakers=8):
        writer = gguf.GGUFWriter(str(self.path), architecture)
        writer.add_uint32('sortformer.num_speakers', speakers)
        for name, dtype in tensors:
            data = np.ones((2, 32), dtype=np.float32)
            packed = gguf.quants.quantize(data, dtype)
            writer.add_tensor(name, packed, raw_dtype=dtype)
        writer.write_header_to_file()
        writer.write_kv_data_to_file()
        writer.write_tensors_to_file()
        writer.close()

    def validate(self):
        return validator.validate(self.path, 'sdk-revision', 'model-revision')

    def test_official_f32_profile_and_manifest(self):
        self.write_model([
            ('encoder.pre_encode.proj.weight', gguf.GGMLQuantizationType.F32),
            ('subpixel_upsample.weight', gguf.GGMLQuantizationType.F16),
            ('subpixel_upsample.bias', gguf.GGMLQuantizationType.F32),
        ])
        result = self.validate()
        self.assertEqual(result['precision'], 'f32_with_f16_conv')
        self.assertEqual(result['conversion_outtype'], 'f32')
        self.assertEqual(result['tensor_types'], {'F16': 1, 'F32': 2})
        self.assertEqual(result['f16_tensors'], ['subpixel_upsample.weight'])
        self.assertEqual(result['sdk_revision'], 'sdk-revision')
        self.assertEqual(result['model_revision'], 'model-revision')
        self.assertEqual(result['sha256'], hashlib.sha256(self.path.read_bytes()).hexdigest())
        self.assertEqual(json.loads(self.path.with_name('manifest.json').read_text()), result)

    def test_all_f32_remains_accepted(self):
        self.write_model([('subpixel_upsample.weight', gguf.GGMLQuantizationType.F32)])
        result = self.validate()
        self.assertEqual(result['precision'], 'f32')
        self.assertEqual(result['f16_tensors'], [])

    @unittest.skipUnless(Path('/opt/nemo-source/conversion/diarization.py').is_file(), 'requires pinned SDK converter')
    def test_official_converter_output(self):
        import torch
        import yaml

        sys.path.insert(0, '/opt/nemo-source')
        from conversion.diarization import convert

        # A small synthetic checkpoint exercises the real converter's dtype
        # dispatch. It is not a complete model and is never used for inference.
        config = dict(
            encoder=dict(d_model=8, n_layers=1, n_heads=2, feat_in=4,
                         self_attention_model='rope', subsampling='feature_stacking'),
            preprocessor=dict(features=4), high_resolution=True, max_num_of_spks=8,
            sortformer_modules=dict(use_learnable_sil_emb=True, tf_d_model=2),
        )
        self.path.with_name('model_config.yaml').write_text(yaml.safe_dump(config))
        torch.save({
            'preprocessor.featurizer.fb': torch.ones(1, 4, 257),
            'encoder.pre_encode.proj.weight': torch.ones(8, 32),
            'sortformer_modules.encoder_proj.weight': torch.ones(2, 8),
            'sortformer_modules.subpixel_upsample.weight': torch.ones(16, 2, 3),
            'sortformer_modules.subpixel_upsample.bias': torch.ones(16),
        }, self.path.with_name('model_weights.ckpt'))
        convert(self.path.parent, self.path, 'f32')
        result = self.validate()
        self.assertEqual(result['f16_tensors'], ['subpixel_upsample.weight'])
        self.assertEqual(result['tensor_types'], {'F16': 1, 'F32': 4})

    def test_reduced_precision_outside_conv_kernel_is_rejected(self):
        for name in ('encoder.pre_encode.proj.weight', 'subpixel_upsample.bias', 'other_conv.weight'):
            with self.subTest(name=name):
                self.write_model([(name, gguf.GGMLQuantizationType.F16)])
                with self.assertRaisesRegex(ValueError, name):
                    self.validate()

    def test_bf16_and_quantization_are_rejected_even_for_conv_kernel(self):
        for dtype in (gguf.GGMLQuantizationType.BF16, gguf.GGMLQuantizationType.Q8_0):
            with self.subTest(dtype=dtype):
                self.write_model([('subpixel_upsample.weight', dtype)])
                with self.assertRaisesRegex(ValueError, f'subpixel_upsample.weight={dtype.name}'):
                    self.validate()

    def test_wrong_model_is_rejected(self):
        for architecture, speakers in (('other', 8), ('sortformer', 4)):
            with self.subTest(architecture=architecture, speakers=speakers):
                self.write_model([('weight', gguf.GGMLQuantizationType.F32)], architecture, speakers)
                with self.assertRaisesRegex(ValueError, 'eight speaker'):
                    self.validate()

    def test_empty_model_is_rejected(self):
        self.write_model([])
        with self.assertRaisesRegex(ValueError, 'no tensors'):
            self.validate()


if __name__ == '__main__':
    unittest.main()
