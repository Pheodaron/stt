# CUDA-образ

`mise run build:gpu` собирает `localhost/vks-stt-go:cuda` через Podman
из `Dockerfile.gpu`. Наличие GPU для сборки не требуется.

Go API и нативный sherpa закреплены на **1.13.8**. Используются ONNX Runtime
**1.28.2**, CUDA 12 и cuDNN 9, база
`docker.io/nvidia/cuda:12.8.1-cudnn-runtime-ubuntu24.04`.
Архив нативных библиотек проверяется SHA-256:
`2132af1ae8c84c84f86ac091e4311839287851d2bf5e660332ebce3898a1e0b2`.

Источник:
https://github.com/k2-fsa/sherpa-onnx/releases/download/v1.13.8/sherpa-onnx-v1.13.8-cuda-12.x-cudnn-9.x-onnxruntime1.28.2-linux-x64-gpu.tar.bz2

При развёртывании нужны Linux amd64, NVIDIA-драйвер для CUDA 12.8 и NVIDIA
Container Toolkit с CDI. Podman передаёт устройство через
`--device nvidia.com/gpu=...`. При SELinux NVIDIA рекомендует
`--security-opt label=disable`; `NVIDIA_VISIBLE_DEVICES=void` исключает
повторную инъекцию через legacy NVIDIA OCI hook.

Веса остаются FP32; `NVIDIA_TF32_OVERRIDE=0` отключает TF32.
При отсутствии CUDAExecutionProvider, драйвера или устройства сервер завершится
до готовности. Вспомогательные операции графа могут выполняться на CPU.
Разный порядок вычислений CPU/GPU требует проверки качества на ваших записях.

Документация NVIDIA по CDI:
https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/cdi-support.html
