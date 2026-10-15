# Зависимости и модели

Исходный проект пользователя не содержит LICENSE; этот перенос не назначает ему новую лицензию.
Нативные библиотеки и веса сохраняют лицензии своих авторов.

| Компонент | Лицензия и исходный код |
|---|---|
| sherpa-onnx / Go bindings | [Apache-2.0](https://github.com/k2-fsa/sherpa-onnx/blob/master/LICENSE) |
| ONNX Runtime (поставляется с sherpa) | [MIT](https://github.com/microsoft/onnxruntime/blob/main/LICENSE) |
| ONNX Runtime C API header v1.20.1 (проверка CUDA) | [MIT](internal/gigaam/ort/LICENSE), источник закреплён в internal/gigaam/ort/README.md |
| NVIDIA CUDA / cuDNN (GPU-образ) | [лицензии NVIDIA](https://docs.nvidia.com/cuda/eula/index.html); отдельны от лицензии проекта |
| GigaAM и экспорт модели | [MIT](https://github.com/salute-developers/GigaAM/blob/main/LICENSE), LICENSE также скачивается вместе с моделью |
| PyTorch / torchaudio (только однократный экспорт) | [PyTorch BSD](https://github.com/pytorch/pytorch/blob/main/LICENSE), [torchaudio BSD](https://github.com/pytorch/audio/blob/main/LICENSE) |
| Silero VAD v6.2.3 FP32 ONNX | [MIT](https://github.com/snakers4/silero-vad/blob/v6.2.3/LICENSE); `vad:prepare` сохраняет LICENSE рядом с весами |
| gRPC Go | [Apache-2.0](https://github.com/grpc/grpc-go/blob/master/LICENSE) |
| protobuf Go | [BSD-3-Clause](https://github.com/protocolbuffers/protobuf-go/blob/master/LICENSE) |
| Gorilla WebSocket | [BSD-2-Clause](https://github.com/gorilla/websocket/blob/main/LICENSE) |
| FFmpeg (системный пакет) | [зависит от сборки](https://ffmpeg.org/legal.html) |

При распространении нативных бинарников учитывайте также уведомления транзитивных компонентов
[sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx/tree/master/cmake) и
[ONNX Runtime](https://github.com/microsoft/onnxruntime/blob/main/ThirdPartyNotices.txt).
