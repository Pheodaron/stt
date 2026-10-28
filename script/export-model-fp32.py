#!/usr/bin/env python3
# Copyright 2025 Xiaomi Corp. (author: Fangjun Kuang)
# SPDX-License-Identifier: Apache-2.0
# Adapted for CPU FP32 and sherpa's batch-1 RNNT decoder.
# Source: k2-fsa/sherpa-onnx, fa29eb91c80335fc70af869a7bb459578f90e5ad,
# scripts/nemo/GigaAM/export-onnx-rnnt-v3-punct.py.
import os

import gigaam
import onnx
import torch
from gigaam.utils import onnx_converter
from torch import Tensor


def add_meta_data(filename: str, meta_data: dict[str, str]):
    model = onnx.load(filename)
    while len(model.metadata_props):
        model.metadata_props.pop()

    for key, value in meta_data.items():
        meta = model.metadata_props.add()
        meta.key = key
        meta.value = str(value)

    onnx.save(model, filename)


class DecoderWrapper(torch.nn.Module):
    def __init__(self, m):
        super().__init__()
        self.m = m

    def forward(self, x: Tensor, unused_x_len: Tensor, h: Tensor, c: Tensor):
        emb = self.m.head.decoder.embed(x)
        g, (h, c) = self.m.head.decoder.lstm(emb.transpose(0, 1), (h, c))
        return g.permute(1, 2, 0), unused_x_len + 1, h, c

    def to_onnx(self, dir_path: str = "."):
        label, hidden_h, hidden_c = self.m.head.decoder.input_example(batch_size=1)
        label = label.to(torch.int32)
        label_len = torch.zeros(1, dtype=torch.int32)

        onnx_converter(
            model_name=f"{self.m.cfg.model_name}_decoder",
            out_dir=dir_path,
            module=self,
            dynamic_axes=self.m.encoder.dynamic_axes(),
            inputs=(label, label_len, hidden_h, hidden_c),
            input_names=["x", "unused_x_len.1", "h.1", "c.1"],
            output_names=["dec", "unused_x_len", "h", "c"],
        )


def main() -> None:
    model_name = "v3_e2e_rnnt"
    model = gigaam.load_model(
        model_name,
        device="cpu",
        fp16_encoder=False,
        download_root=os.environ["GIGAAM_EXPORT_CHECKPOINTS"],
    ).float().eval()

    sp = model.decoding.tokenizer.model
    with open("./tokens.txt", "w", encoding="utf-8") as f:
        for i in range(sp.vocab_size()):
            f.write(f"{sp.id_to_piece(i)} {i}\n")

        f.write(f"<blk> {i+1}\n")
        print("Saved to tokens.txt")

    with model.encoder.onnx_export_mode():
        onnx_converter(
            model_name=f"{model.cfg.model_name}_encoder",
            out_dir=".",
            module=model.encoder,
            dynamic_axes=model.encoder.dynamic_axes(),
        )
    DecoderWrapper(model).to_onnx(".")

    onnx_converter(
        model_name=f"{model.cfg.model_name}_joint",
        out_dir=".",
        module=model.head.joint,
        inputs=model.head.joint.input_example(batch_size=1),
    )
    meta_data = {
        "vocab_size": model.cfg["head"]["decoder"]["num_classes"] - 1,
        "pred_rnn_layers": model.cfg["head"]["decoder"]["pred_rnn_layers"],
        "pred_hidden": model.cfg["head"]["decoder"]["pred_hidden"],
        "normalize_type": "",
        "subsampling_factor": 4,
        "model_type": "EncDecRNNTBPEModel",
        "version": "3",
        "model_author": "https://github.com/salute-developers/GigaAM",
        "license": "https://github.com/salute-developers/GigaAM/blob/main/LICENSE",
        "language": "Russian",
        "comment": "v3",
        "is_giga_am": 1,
    }

    add_meta_data(f"./{model_name}_encoder.onnx", meta_data)

    os.rename(f"./{model_name}_decoder.onnx", "decoder.onnx")
    os.rename(f"./{model_name}_joint.onnx", "joiner.onnx")
    os.rename(f"./{model_name}_encoder.onnx", "encoder.onnx")


if __name__ == "__main__":
    main()
