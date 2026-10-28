# Зависимости и модели

Исходный проект пользователя не содержит LICENSE; этот перенос не назначает ему новую лицензию.
Нативные библиотеки и веса сохраняют лицензии своих авторов.

| Компонент | Лицензия и исходный код |
|---|---|
| sherpa-onnx / Go bindings | [Apache-2.0](https://github.com/k2-fsa/sherpa-onnx/blob/master/LICENSE) |
| ONNX Runtime (поставляется с sherpa) | [MIT](https://github.com/microsoft/onnxruntime/blob/main/LICENSE) |
| NVIDIA NeMo-Speech.cpp CPU SDK и публичные C ABI headers | [Apache-2.0](internal/diarization/sdk/LICENSE), commit закреплён в [nemo-version.env](script/nemo-version.env) |
| Nemotron-3-Diarization, GGUF F32 | [OpenMDW-1.1](internal/diarization/sdk/MODEL-LICENSE.OpenMDW-1.1); лицензия и оригинальная model card копируются рядом с весами |
| ggml / llama.cpp и SentencePiece (NeMo SDK) | MIT / Apache-2.0; SDK устанавливает notices, дополнительно включён copyright системной SentencePiece из build-stage |
| ONNX Runtime C API header v1.20.1 (проверка CUDA) | [MIT](internal/gigaam/ort/LICENSE), источник закреплён в internal/gigaam/ort/README.md |
| NVIDIA CUDA / cuDNN (GPU-образ) | [лицензии NVIDIA](https://docs.nvidia.com/cuda/eula/index.html); отдельны от лицензии проекта |
| GigaAM и экспорт модели | [MIT](https://github.com/salute-developers/GigaAM/blob/main/LICENSE), LICENSE также скачивается вместе с моделью |
| PyTorch / torchaudio (только этап сборки модели) | [PyTorch BSD](https://github.com/pytorch/pytorch/blob/main/LICENSE), [torchaudio BSD](https://github.com/pytorch/audio/blob/main/LICENSE) |
| Silero VAD v6.2.3 FP32 ONNX | [MIT](https://github.com/snakers4/silero-vad/blob/v6.2.3/LICENSE); этап `vad-model` включает LICENSE рядом с весами |
| gRPC Go | [Apache-2.0](https://github.com/grpc/grpc-go/blob/master/LICENSE) |
| protobuf Go | [BSD-3-Clause](https://github.com/protocolbuffers/protobuf-go/blob/master/LICENSE) |
| Gorilla WebSocket | [BSD-2-Clause](https://github.com/gorilla/websocket/blob/main/LICENSE) |
| FFmpeg 8.1.3 (статическая сборка без GPL/nonfree и внешних кодеков) | [LGPL-2.1-or-later](https://github.com/FFmpeg/FFmpeg/blob/n8.1.3/COPYING.LGPLv2.1); исходники и конфигурация закреплены в [script/build-ffmpeg.sh](script/build-ffmpeg.sh) |
| musl / GCC runtime (статический FFmpeg) | [MIT](https://musl.libc.org/COPYRIGHT) / [GCC Runtime Library Exception](https://www.gnu.org/licenses/gcc-exception-3.1.html); уведомления включены в `/app/licenses/ffmpeg/` |

При распространении нативных бинарников учитывайте также уведомления транзитивных компонентов
[sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx/tree/master/cmake) и
[ONNX Runtime](https://github.com/microsoft/onnxruntime/blob/main/ThirdPartyNotices.txt).
