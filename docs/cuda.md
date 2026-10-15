# CPU и NVIDIA CUDA с mise и Podman

Оба режима используют один Go-сервер и GigaAM v3 E2E RNNT FP32 внутри процесса.
Mise закрепляет Go 1.25.1 и запускает задачи из `mise.toml`. Docker daemon не нужен.
Podman, системный компилятор и FFmpeg устанавливаются отдельно.

## CPU

```bash
cp .env.example .env
mise trust
mise install
mise run model
mise run build:image-cpu
mise run run:cpu
```

`Dockerfile.cpu`: штатные CPU-библиотеки sherpa-onnx Go 1.13.8 и Debian bookworm.
Сборка предусмотрена для Linux amd64/arm64; arm64 здесь не проверялся.
Прямые задачи используют Podman и не зависят от внешнего Compose provider.

## NVIDIA CDI

Требуются Podman 5+, Linux amd64, NVIDIA GPU, драйвер для CUDA 12.8 и NVIDIA
Container Toolkit с CDI. CUDA/cuDNN находятся в образе; драйвер предоставляет хост.
Rootless Podman поддерживает CDI, если у пользователя есть доступ к устройствам GPU.

Проверьте хост перед запуском:

```bash
nvidia-smi
nvidia-ctk cdi list
```

Список CDI должен содержать, например, `nvidia.com/gpu=0`. В новых установках
Toolkit спецификации обновляет сервис `nvidia-cdi-refresh`. Если автоматическое
обновление не настроено и спецификаций нет, их можно создать вручную:

```bash
sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml
nvidia-ctk cdi list
```

Не создавайте вторую спецификацию тех же устройств поверх автоматически
сгенерированной. После смены GPU/драйвера обновите спецификацию выбранным способом.

В `.env` задайте `GPU_DEVICE_ID=0`, другой индекс или GPU UUID из списка CDI.
`all` открывает все GPU, но сервер использует CUDA device 0 среди видимых устройств.

```bash
mise run build:image-gpu
mise run run:gpu
```

Задача выполняет `podman run --device nvidia.com/gpu=...`. Для GPU используется
`--security-opt label=disable` по рекомендации NVIDIA для SELinux; CPU сохраняет
обычное разделение меток. `NVIDIA_VISIBLE_DEVICES=void` предотвращает повторную
инъекцию через старый NVIDIA OCI hook. Модели монтируются с `:ro,z`; `:U` не
используется, поэтому владельцы файлов модели на хосте не меняются.

Из другого терминала:

```bash
mise run logs:gpu
mise run stop:gpu
```

Переключение CPU → CUDA: `mise run stop:cpu`, затем `mise run run:gpu`.
Порт, protobuf, WebSocket JSON-RPC и файлы FP32 остаются общими.
Незавершённые сессии при перезапуске не сохраняются.

## Compose по желанию

`mise run run:compose-cpu` и `mise run run:compose-gpu` вызывают `podman compose`.
Он требует внешний provider (например, актуальный podman-compose) с поддержкой
CDI в `devices`. GPU reservations Docker в этих файлах не используются.
При несовместимости provider используйте основные задачи `run:cpu` / `run:gpu`.

## Нативные библиотеки

Go API и native sherpa закреплены на **1.13.8**. GPU-образ содержит ONNX Runtime
**1.28.2**, CUDA 12 и cuDNN 9. Архив проверяется SHA-256 перед установкой:

https://github.com/k2-fsa/sherpa-onnx/releases/download/v1.13.8/sherpa-onnx-v1.13.8-cuda-12.x-cudnn-9.x-onnxruntime1.28.2-linux-x64-gpu.tar.bz2

SHA-256: `2132af1ae8c84c84f86ac091e4311839287851d2bf5e660332ebce3898a1e0b2`.

Для запуска на хосте с уже установленными CUDA 12/cuDNN 9:

```bash
mise run build:local-gpu
mise run run:local-gpu
```

`SHERPA_CUDA_ARCHIVE=/path/runtime.tar.bz2 mise run build:local-gpu` использует
локальный архив с тем же SHA-256. `mise run build` восстанавливает CPU-библиотеки.

## Качество и замеры

CUDA-режим сохраняет FP32 и отключает TF32 через `NVIDIA_TF32_OVERRIDE=0`.
При отсутствии CUDAExecutionProvider, драйвера или устройства сервер завершится
до готовности. Вспомогательные операции графа могут выполняться на CPU.
Разный порядок вычислений CPU/GPU требует сравнения качества на ваших записях.

Поместите тестовую запись в `models/test.wav`:

```bash
mise run bench:gpu -- -file /app/models/test.wav -threads 1,2,4 -runs 5
# Аналогичный CPU-замер:
mise run bench:cpu -- -file /app/models/test.wav -threads 1,2,4 -runs 5
```

JSONL содержит provider, precision, время загрузки, итерации и RTF.
Числа CPU/INT8 из предыдущих замеров не являются измерениями CUDA/FP32.
Фактически выполненные проверки: [validation.md](validation.md).

Документация NVIDIA по CDI:
https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/cdi-support.html

Документация sherpa CUDA:
https://k2-fsa.github.io/sherpa/onnx/install/linux.html
